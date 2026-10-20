package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coulof/ovn-topology/pkg/api"
	"github.com/coulof/ovn-topology/pkg/diag"
	"github.com/coulof/ovn-topology/pkg/graph"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

type viewMode int

const (
	viewTree viewMode = iota
	viewL2
	viewChassis
	viewMatrix
)

// RouterPortConnection represents an L3 interconnection between an LR port and an LS.
type RouterPortConnection struct {
	RouterID      string
	RouterName    string
	VPCName       string
	PortName      string
	MAC           string
	Networks      []string
	SwitchID      string
	SwitchName    string
	PeerPortName  string
	ActiveGateway string
	IsShared      bool
}

type diagStep int

const (
	diagStepMenu diagStep = iota
	diagStepPickDest
	diagStepResult
)

// TraceDestination represents an interactive target candidate for ovn-trace.
type TraceDestination struct {
	Label    string
	IP       string
	Subnet   string
	Category string
}

// TraceProtocolOption defines a selectable protocol and port for ovn-trace.
type TraceProtocolOption struct {
	Name     string
	Protocol string
	Port     int
	Label    string
}

var AvailableTraceProtocols = []TraceProtocolOption{
	{Name: "icmp", Protocol: "icmp", Port: 0, Label: "ICMP (Ping)"},
	{Name: "ssh", Protocol: "tcp", Port: 22, Label: "TCP 22 (SSH)"},
	{Name: "http", Protocol: "tcp", Port: 80, Label: "TCP 80 (HTTP)"},
	{Name: "https", Protocol: "tcp", Port: 443, Label: "TCP 443 (HTTPS)"},
	{Name: "dns", Protocol: "udp", Port: 53, Label: "UDP 53 (DNS)"},
}

var TraceCategories = []string{
	"All",
	"VM",
	"Pod",
	"Gateway",
	"Node",
	"Public DNS",
}

type model struct {
	daemonURL     string
	fixturePath   string
	graphResp     *api.GraphResponse
	mode          viewMode
	themeIndex    int
	theme         Theme
	styles        ThemeStyles
	selectedIdx   int
	nodeList      []api.GraphNode
	vpcNames      []string
	vpcGroups     map[string][]api.GraphNode
	sharedNodes   []api.GraphNode
	externalNodes []api.GraphNode
	portList      []RouterPortConnection
	routerPorts   map[string][]RouterPortConnection
	switchPorts   map[string][]RouterPortConnection
	nodeMap       map[string]api.GraphNode
	err           error
	status        string
	width         int
	height        int

	// L2 & Chassis Views (M2)
	l2Resp           *api.GraphResponse
	l2Nodes          []api.GraphNode
	focusedLS        string
	focusedLSName    string
	chassisResp      *api.GraphResponse
	chassisNodes     []api.GraphNode
	expandedSwitches map[string]bool
	switchWorkloads  map[string][]api.GraphNode

	// Tier 1 Diagnostics (ADR-0001)
	diagMode          bool
	diagAvailable     bool
	diagReason        string
	k8sNamespace      string
	k8sClient         kubernetes.Interface
	diagExecutor      diag.Executor
	diagLocator       *diag.PodLocator
	diagCentralPod    diag.PodRef
	diagModal         bool
	diagStep          diagStep
	diagRunning       bool
	diagAction        string
	diagTitle         string
	diagTraceRes      *diag.TraceResult
	diagTraceParams   string
	traceDestinations []TraceDestination
	selectedDestIdx   int
	traceSearchFilter string
	traceCategoryIdx  int
	traceProtoIdx     int
	traceProtocol     string
	tracePort         int
	diagRawOutput     string
	diagRawView       bool
	diagScrollOffset  int
	diagError         error
}

type graphLoadedMsg struct {
	resp *api.GraphResponse
	err  error
}

type l2LoadedMsg struct {
	lsID string
	resp *api.GraphResponse
	err  error
}

type chassisLoadedMsg struct {
	resp *api.GraphResponse
	err  error
}

type diagProbeMsg struct {
	available  bool
	reason     string
	centralPod diag.PodRef
}

type diagExecResultMsg struct {
	action      string
	title       string
	traceRes    *diag.TraceResult
	traceParams string
	rawOutput   string
	err         error
}

func initialModel(daemonURL, fixturePath, themeName string, diagMode bool, kubeconfigPath, k8sNamespace string) model {
	theme, idx := GetThemeByName(themeName)
	if k8sNamespace == "" {
		k8sNamespace = "kube-system"
	}

	m := model{
		daemonURL:    daemonURL,
		fixturePath:  fixturePath,
		status:       "Connecting...",
		mode:         viewTree,
		themeIndex:   idx,
		theme:        theme,
		styles:       CompileStyles(theme),
		diagMode:     diagMode,
		k8sNamespace:     k8sNamespace,
		nodeMap:          make(map[string]api.GraphNode),
		routerPorts:      make(map[string][]RouterPortConnection),
		switchPorts:      make(map[string][]RouterPortConnection),
		expandedSwitches: make(map[string]bool),
		switchWorkloads:  make(map[string][]api.GraphNode),
		traceProtocol:    AvailableTraceProtocols[0].Protocol,
		tracePort:        AvailableTraceProtocols[0].Port,
		traceProtoIdx:    0,
		traceCategoryIdx: 0,
	}

	if diagMode {
		var k8sConfig *rest.Config
		var err error
		if kubeconfigPath != "" {
			k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		} else {
			loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
			configOverrides := &clientcmd.ConfigOverrides{}
			kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
			k8sConfig, err = kubeConfig.ClientConfig()
		}

		if err != nil {
			m.diagAvailable = false
			m.diagReason = fmt.Sprintf("Failed to load kubeconfig: %v", err)
		} else {
			clientset, err := kubernetes.NewForConfig(k8sConfig)
			if err != nil {
				m.diagAvailable = false
				m.diagReason = fmt.Sprintf("Failed to create k8s client: %v", err)
			} else {
				m.k8sClient = clientset
				m.diagExecutor = diag.NewKubeExecutor(clientset, k8sConfig)
				m.diagLocator = diag.NewPodLocator(clientset, k8sNamespace)
				m.diagReason = "Probing RBAC..."
			}
		}
	}

	return m
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.fetchGraphCmd()}
	if m.diagMode && m.diagExecutor != nil && m.diagLocator != nil {
		cmds = append(cmds, m.probeDiagCmd())
	}
	return tea.Batch(cmds...)
}

func (m model) probeDiagCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		centralPod, err := m.diagLocator.FindOvnCentralPod(ctx)
		if err != nil {
			return diagProbeMsg{
				available: false,
				reason:    fmt.Sprintf("ovn-central pod not found: %v", err),
			}
		}

		allowed, reason, err := diag.CheckExecAccess(ctx, m.diagExecutor, centralPod)
		if err != nil {
			return diagProbeMsg{
				available: false,
				reason:    fmt.Sprintf("probe error: %v", err),
			}
		}

		return diagProbeMsg{
			available:  allowed,
			reason:     reason,
			centralPod: centralPod,
		}
	}
}

func (m model) fetchGraphCmd() tea.Cmd {
	return func() tea.Msg {
		if m.fixturePath != "" {
			snap := graph.LoadTestSnapshot(nil)
			resp, err := snap.BuildGraph("l3", "", 1, 1, 100)
			return graphLoadedMsg{resp: resp, err: err}
		}

		url := fmt.Sprintf("%s/graph?view=l3", strings.TrimRight(m.daemonURL, "/"))
		client := &http.Client{Timeout: 3 * time.Second}
		r, err := client.Get(url)
		if err != nil {
			return graphLoadedMsg{err: fmt.Errorf("failed to reach daemon at %s: %w", url, err)}
		}
		defer func() { _ = r.Body.Close() }()

		if r.StatusCode != http.StatusOK {
			return graphLoadedMsg{err: fmt.Errorf("daemon returned HTTP %d", r.StatusCode)}
		}

		var resp api.GraphResponse
		if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
			return graphLoadedMsg{err: err}
		}
		return graphLoadedMsg{resp: &resp}
	}
}

func (m model) fetchL2Cmd(focusLS string) tea.Cmd {
	return func() tea.Msg {
		if focusLS == "" {
			return l2LoadedMsg{lsID: focusLS}
		}
		if m.fixturePath != "" {
			snap := graph.LoadTestSnapshot(nil)
			resp, err := snap.BuildGraph("l2", focusLS, 1, 1, 100)
			return l2LoadedMsg{lsID: focusLS, resp: resp, err: err}
		}
		url := fmt.Sprintf("%s/graph?view=l2&focus=%s", strings.TrimRight(m.daemonURL, "/"), focusLS)
		client := &http.Client{Timeout: 3 * time.Second}
		r, err := client.Get(url)
		if err != nil {
			return l2LoadedMsg{lsID: focusLS, err: err}
		}
		defer func() { _ = r.Body.Close() }()
		var resp api.GraphResponse
		if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
			return l2LoadedMsg{lsID: focusLS, err: err}
		}
		return l2LoadedMsg{lsID: focusLS, resp: &resp}
	}
}

func (m model) fetchChassisCmd() tea.Cmd {
	return func() tea.Msg {
		if m.fixturePath != "" {
			snap := graph.LoadTestSnapshot(nil)
			resp, err := snap.BuildGraph("chassis", "", 1, 1, 100)
			return chassisLoadedMsg{resp: resp, err: err}
		}
		url := fmt.Sprintf("%s/graph?view=chassis", strings.TrimRight(m.daemonURL, "/"))
		client := &http.Client{Timeout: 3 * time.Second}
		r, err := client.Get(url)
		if err != nil {
			return chassisLoadedMsg{err: err}
		}
		defer func() { _ = r.Body.Close() }()
		var resp api.GraphResponse
		if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
			return chassisLoadedMsg{err: err}
		}
		return chassisLoadedMsg{resp: &resp}
	}
}

func (m *model) applyGraph(resp *api.GraphResponse) {
	m.err = nil
	m.graphResp = resp
	m.status = fmt.Sprintf("Revision %d | %d nodes, %d edges", resp.Revision, len(resp.Nodes), len(resp.Edges))

	m.nodeMap = make(map[string]api.GraphNode, len(resp.Nodes))
	for _, n := range resp.Nodes {
		m.nodeMap[n.Id] = n
	}

	// Group nodes by visual section
	m.vpcGroups = make(map[string][]api.GraphNode)
	m.sharedNodes = make([]api.GraphNode, 0)
	m.externalNodes = make([]api.GraphNode, 0)

	for _, n := range resp.Nodes {
		if n.Type == api.GraphNodeTypeVPC {
			continue
		}
		if n.Type == api.GraphNodeTypeExternal {
			m.externalNodes = append(m.externalNodes, n)
		} else if n.Parent != nil && *n.Parent != "" {
			vpcName := strings.TrimPrefix(*n.Parent, graph.PrefixVPC)
			m.vpcGroups[vpcName] = append(m.vpcGroups[vpcName], n)
		} else {
			m.sharedNodes = append(m.sharedNodes, n)
		}
	}

	// 1. Sort VPC names deterministically (default VPC first, then alphabetical)
	m.vpcNames = make([]string, 0, len(m.vpcGroups))
	for vpcName := range m.vpcGroups {
		m.vpcNames = append(m.vpcNames, vpcName)
	}
	sort.Slice(m.vpcNames, func(i, j int) bool {
		iDef := m.vpcNames[i] == "default" || m.vpcNames[i] == "ovn-cluster"
		jDef := m.vpcNames[j] == "default" || m.vpcNames[j] == "ovn-cluster"
		if iDef != jDef {
			return iDef
		}
		return m.vpcNames[i] < m.vpcNames[j]
	})

	// Build flat navigable list matching the EXACT visual top-to-bottom order on screen
	m.nodeList = make([]api.GraphNode, 0)
	for _, vpcName := range m.vpcNames {
		nodes := m.vpcGroups[vpcName]
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Type != nodes[j].Type {
				return nodes[i].Type == api.GraphNodeTypeLR // Router first, then Switches
			}
			return nodes[i].Name < nodes[j].Name
		})
		m.vpcGroups[vpcName] = nodes
		m.nodeList = append(m.nodeList, nodes...)
	}

	sort.Slice(m.sharedNodes, func(i, j int) bool {
		return m.sharedNodes[i].Name < m.sharedNodes[j].Name
	})
	m.nodeList = append(m.nodeList, m.sharedNodes...)

	sort.Slice(m.externalNodes, func(i, j int) bool {
		return m.externalNodes[i].Name < m.externalNodes[j].Name
	})
	m.nodeList = append(m.nodeList, m.externalNodes...)

	// Index all router-peer interconnections from Edges
	m.portList = make([]RouterPortConnection, 0)
	m.routerPorts = make(map[string][]RouterPortConnection)
	m.switchPorts = make(map[string][]RouterPortConnection)

	// Count attachments to identify shared/interconnect switches
	switchAttachmentCount := make(map[string]int)
	for _, e := range resp.Edges {
		if e.Type == api.RouterPeer {
			switchAttachmentCount[e.Target]++
		}
	}

	for _, e := range resp.Edges {
		if e.Type != api.RouterPeer {
			continue
		}
		srcNode, hasSrc := m.nodeMap[e.Source]
		tgtNode, hasTgt := m.nodeMap[e.Target]
		if !hasSrc || !hasTgt {
			continue
		}

		lrpName := ""
		lrpMac := ""
		var networks []string
		lspName := ""
		activeGw := ""

		if e.Properties != nil {
			props := *e.Properties
			if p, ok := props["lrp"].(string); ok {
				lrpName = p
			}
			if mac, ok := props["lrpMac"].(string); ok {
				lrpMac = mac
			}
			if l, ok := props["lsp"].(string); ok {
				lspName = l
			}
			if gw, ok := props["activeGateway"].(string); ok {
				activeGw = gw
			}
			if nets, ok := props["networks"].([]interface{}); ok {
				for _, it := range nets {
					if s, ok := it.(string); ok {
						networks = append(networks, s)
					}
				}
			}
		}
		if len(networks) == 0 && e.Label != nil {
			networks = strings.Split(*e.Label, ", ")
		}

		vpcName := srcNode.Name
		if srcNode.Parent != nil {
			vpcName = strings.TrimPrefix(*srcNode.Parent, graph.PrefixVPC)
		}

		isShared := switchAttachmentCount[e.Target] > 1

		conn := RouterPortConnection{
			RouterID:      srcNode.Id,
			RouterName:    srcNode.Name,
			VPCName:       vpcName,
			PortName:      lrpName,
			MAC:           lrpMac,
			Networks:      networks,
			SwitchID:      tgtNode.Id,
			SwitchName:    tgtNode.Name,
			PeerPortName:  lspName,
			ActiveGateway: activeGw,
			IsShared:      isShared,
		}

		m.portList = append(m.portList, conn)
		m.routerPorts[srcNode.Id] = append(m.routerPorts[srcNode.Id], conn)
		m.switchPorts[tgtNode.Id] = append(m.switchPorts[tgtNode.Id], conn)
	}

	sort.Slice(m.portList, func(i, j int) bool {
		if m.portList[i].RouterName != m.portList[j].RouterName {
			return m.portList[i].RouterName < m.portList[j].RouterName
		}
		return m.portList[i].PortName < m.portList[j].PortName
	})

	if m.selectedIdx >= len(m.nodeList) {
		m.selectedIdx = 0
	}

	if m.focusedLS == "" {
		for _, n := range m.nodeList {
			if n.Type == api.GraphNodeTypeLS {
				m.focusedLS = n.Id
				m.focusedLSName = n.Name
				break
			}
		}
	}

	// Pre-populate workloads if offline fixture
	if m.fixturePath != "" {
		snap := graph.LoadTestSnapshot(nil)
		for _, n := range resp.Nodes {
			if n.Type == api.GraphNodeTypeLS {
				l2, err := snap.BuildGraph("l2", n.Id, 1, 1, 100)
				if err == nil && l2 != nil {
					var lsps []api.GraphNode
					for _, item := range l2.Nodes {
						if item.Type == api.GraphNodeTypeLSP {
							lsps = append(lsps, item)
						}
					}
					m.switchWorkloads[n.Id] = lsps
				}
			}
		}
	}

	m.rebuildNodeList()
}

func (m *model) rebuildNodeList() {
	var list []api.GraphNode
	for _, vpcName := range m.vpcNames {
		nodes := m.vpcGroups[vpcName]
		list = append(list, nodes...)
	}
	list = append(list, m.sharedNodes...)
	list = append(list, m.externalNodes...)
	m.nodeList = list
	if m.selectedIdx >= len(m.nodeList) {
		m.selectedIdx = 0
	}
}

func (m model) switchList() []api.GraphNode {
	var switches []api.GraphNode
	for _, n := range m.nodeList {
		if n.Type == api.GraphNodeTypeLS {
			alreadyIn := false
			for _, s := range switches {
				if s.Id == n.Id {
					alreadyIn = true
					break
				}
			}
			if !alreadyIn {
				switches = append(switches, n)
			}
		}
	}
	return switches
}

func (m model) fetchAllSwitchesL2Cmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, n := range m.nodeList {
		if n.Type == api.GraphNodeTypeLS {
			cmds = append(cmds, m.fetchL2Cmd(n.Id))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case graphLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "Error loading graph"
			return m, nil
		}
		m.applyGraph(msg.resp)
		return m, tea.Batch(m.fetchAllSwitchesL2Cmd(), m.fetchChassisCmd())

	case l2LoadedMsg:
		if msg.err == nil && msg.resp != nil {
			var lsps []api.GraphNode
			for _, n := range msg.resp.Nodes {
				if n.Type == api.GraphNodeTypeLSP {
					lsps = append(lsps, n)
				}
			}
			if msg.lsID != "" {
				m.switchWorkloads[msg.lsID] = lsps
			}
			if msg.lsID == m.focusedLS || m.focusedLS == "" {
				m.l2Resp = msg.resp
				m.l2Nodes = lsps
				m.focusedLS = msg.lsID
			}
			m.rebuildNodeList()
		}
		return m, nil

	case chassisLoadedMsg:
		if msg.err == nil && msg.resp != nil {
			m.chassisResp = msg.resp
			m.chassisNodes = msg.resp.Nodes
		}
		return m, nil

	case diagProbeMsg:
		m.diagAvailable = msg.available
		m.diagReason = msg.reason
		m.diagCentralPod = msg.centralPod
		if msg.available {
			m.status = "Diag Ready: " + msg.reason
		} else {
			m.status = "Diag Degraded: " + msg.reason
		}
		return m, nil

	case diagExecResultMsg:
		m.diagRunning = false
		m.diagAction = msg.action
		m.diagTitle = msg.title
		m.diagTraceRes = msg.traceRes
		m.diagTraceParams = msg.traceParams
		m.diagRawOutput = msg.rawOutput
		m.diagScrollOffset = 0
		m.diagError = msg.err
		return m, nil

	case tea.KeyMsg:
		if m.diagModal {
			if m.diagStep == diagStepPickDest {
				switch msg.String() {
				case "esc":
					if m.traceSearchFilter != "" {
						m.traceSearchFilter = ""
						m.selectedDestIdx = 0
						return m, nil
					}
					m.diagStep = diagStepMenu
					m.selectedDestIdx = 0
					return m, nil
				case "ctrl+c":
					m.diagModal = false
					m.diagStep = diagStepMenu
					return m, nil
				case "tab":
					m.traceCategoryIdx = (m.traceCategoryIdx + 1) % len(TraceCategories)
					m.selectedDestIdx = 0
					return m, nil
				case "shift+tab":
					m.traceCategoryIdx = (m.traceCategoryIdx - 1 + len(TraceCategories)) % len(TraceCategories)
					m.selectedDestIdx = 0
					return m, nil
				case "ctrl+p":
					m.traceProtoIdx = (m.traceProtoIdx + 1) % len(AvailableTraceProtocols)
					m.traceProtocol = AvailableTraceProtocols[m.traceProtoIdx].Protocol
					m.tracePort = AvailableTraceProtocols[m.traceProtoIdx].Port
					return m, nil
				case "f1":
					m.traceProtoIdx = 0
					m.traceProtocol = AvailableTraceProtocols[0].Protocol
					m.tracePort = AvailableTraceProtocols[0].Port
					return m, nil
				case "f2":
					m.traceProtoIdx = 1
					m.traceProtocol = AvailableTraceProtocols[1].Protocol
					m.tracePort = AvailableTraceProtocols[1].Port
					return m, nil
				case "f3":
					m.traceProtoIdx = 2
					m.traceProtocol = AvailableTraceProtocols[2].Protocol
					m.tracePort = AvailableTraceProtocols[2].Port
					return m, nil
				case "f4":
					m.traceProtoIdx = 3
					m.traceProtocol = AvailableTraceProtocols[3].Protocol
					m.tracePort = AvailableTraceProtocols[3].Port
					return m, nil
				case "f5":
					m.traceProtoIdx = 4
					m.traceProtocol = AvailableTraceProtocols[4].Protocol
					m.tracePort = AvailableTraceProtocols[4].Port
					return m, nil
				case "backspace":
					if len(m.traceSearchFilter) > 0 {
						m.traceSearchFilter = m.traceSearchFilter[:len(m.traceSearchFilter)-1]
						m.selectedDestIdx = 0
					}
					return m, nil
				case "ctrl+u":
					m.traceSearchFilter = ""
					m.selectedDestIdx = 0
					return m, nil
				case "up", "ctrl+k":
					if m.selectedDestIdx > 0 {
						m.selectedDestIdx--
					}
					return m, nil
				case "down", "ctrl+j":
					filtered := m.filteredTraceDestinations()
					if m.selectedDestIdx < len(filtered)-1 {
						m.selectedDestIdx++
					}
					return m, nil
				case "enter":
					filtered := m.filteredTraceDestinations()
					if len(filtered) > 0 && m.selectedDestIdx < len(filtered) {
						chosenDest := filtered[m.selectedDestIdx]
						proto := m.traceProtocol
						port := m.tracePort
						if chosenDest.Category == "Custom" && strings.Contains(m.traceSearchFilter, ":") {
							parts := strings.Split(m.traceSearchFilter, ":")
							if len(parts) == 2 {
								if pNum, err := strconv.Atoi(parts[1]); err == nil && pNum > 0 {
									proto = "tcp"
									port = pNum
								}
							}
						}
						m.diagRunning = true
						m.diagStep = diagStepResult
						m.diagScrollOffset = 0
						return m, m.executeTrace(chosenDest.IP, proto, port)
					}
					return m, nil
				default:
					s := msg.String()
					if len(s) == 1 {
						ch := s[0]
						if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
							ch == '.' || ch == ':' || ch == '-' || ch == '_' || ch == '/' {
							m.traceSearchFilter += s
							m.selectedDestIdx = 0
							return m, nil
						}
					}
					return m, nil
				}
			}

			if m.diagStep == diagStepResult {
				switch msg.String() {
				case "esc":
					if m.diagAction == "trace" {
						m.diagStep = diagStepPickDest
					} else {
						m.diagStep = diagStepMenu
					}
					m.diagRawOutput = ""
					m.diagTraceRes = nil
					return m, nil
				case "x", "ctrl+c":
					m.diagModal = false
					m.diagStep = diagStepMenu
					return m, nil
				case "o":
					m.diagRawView = !m.diagRawView
					return m, nil
				case "down", "j":
					m.diagScrollOffset++
					return m, nil
				case "up", "k":
					if m.diagScrollOffset > 0 {
						m.diagScrollOffset--
					}
					return m, nil
				case "pgdown":
					m.diagScrollOffset += 8
					return m, nil
				case "pgup":
					if m.diagScrollOffset >= 8 {
						m.diagScrollOffset -= 8
					} else {
						m.diagScrollOffset = 0
					}
					return m, nil
				default:
					return m, nil
				}
			}

			// Main Diagnostics Menu (diagStepMenu)
			switch msg.String() {
			case "esc", "x", "ctrl+c":
				m.diagModal = false
				m.diagStep = diagStepMenu
				m.diagRawOutput = ""
				m.diagTraceRes = nil
				m.diagError = nil
				m.diagRunning = false
				m.diagScrollOffset = 0
				return m, nil
			case "1":
				m.diagStep = diagStepPickDest
				m.selectedDestIdx = 0
				m.traceSearchFilter = ""
				m.traceCategoryIdx = 0
				m.traceProtoIdx = 0
				m.traceProtocol = AvailableTraceProtocols[0].Protocol
				m.tracePort = AvailableTraceProtocols[0].Port
				var selNode *api.GraphNode
				if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
					selNode = &m.nodeList[m.selectedIdx]
				}
				srcIP := m.selectedTargetIP()
				m.traceDestinations = m.collectTraceDestinations(selNode, srcIP)
				return m, nil
			case "2":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagStep = diagStepResult
				m.diagError = nil
				m.diagScrollOffset = 0
				nodeName := m.selectedNodeName()
				portName := m.selectedPortName()
				targetIP := m.selectedTargetIP()
				return m, m.executeTcpdump("icmp or icmp6", nodeName, portName, targetIP)
			case "3":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagStep = diagStepResult
				m.diagError = nil
				m.diagScrollOffset = 0
				nodeName := m.selectedNodeName()
				portName := m.selectedPortName()
				targetIP := m.selectedTargetIP()
				return m, m.executeTcpdump("tcp", nodeName, portName, targetIP)
			case "4":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagStep = diagStepResult
				m.diagError = nil
				m.diagScrollOffset = 0
				nodeName := m.selectedNodeName()
				portName := m.selectedPortName()
				targetIP := m.selectedTargetIP()
				filter := "ip"
				if targetIP != "" && net.ParseIP(targetIP) != nil {
					filter = fmt.Sprintf("host %s", targetIP)
				}
				return m, m.executeTcpdump(filter, nodeName, portName, targetIP)
			case "5":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagStep = diagStepResult
				m.diagError = nil
				m.diagScrollOffset = 0
				nodeName := m.selectedNodeName()
				targetIP := m.selectedTargetIP()
				return m, m.executeNodeDiag("conntrack", nodeName, targetIP)
			case "6":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagStep = diagStepResult
				m.diagError = nil
				m.diagScrollOffset = 0
				nodeName := m.selectedNodeName()
				return m, m.executeNodeDiag("ovs-iface", nodeName, "")
			case "7":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagStep = diagStepResult
				m.diagError = nil
				m.diagScrollOffset = 0
				nodeName := m.selectedNodeName()
				return m, m.executeNodeDiag("logs", nodeName, "")
			default:
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "x":
			if !m.diagMode {
				m.status = "Run with --diag to enable Tier 1 diagnostics (ADR-0001)"
				return m, nil
			}
			if !m.diagAvailable {
				m.status = fmt.Sprintf("Diag unavailable: %s", m.diagReason)
				return m, nil
			}
			m.diagModal = true
			m.diagStep = diagStepMenu
			m.diagRawOutput = ""
			m.diagTraceRes = nil
			m.diagError = nil
			m.diagRunning = false
			m.diagRawView = false
			m.diagScrollOffset = 0
			return m, nil
		case "t":
			m.themeIndex = (m.themeIndex + 1) % len(AvailableThemes)
			m.theme = AvailableThemes[m.themeIndex]
			m.styles = CompileStyles(m.theme)
			m.status = fmt.Sprintf("Theme: %s", m.theme.Name)
			return m, nil
		case "tab":
			m.mode = (m.mode + 1) % 4
			m.selectedIdx = 0
			if m.mode == viewL2 {
				switches := m.switchList()
				if len(switches) > 0 {
					m.focusedLS = switches[0].Id
					m.focusedLSName = switches[0].Name
					if len(m.switchWorkloads[m.focusedLS]) == 0 {
						return m, m.fetchL2Cmd(m.focusedLS)
					}
					m.l2Nodes = m.switchWorkloads[m.focusedLS]
				}
			}
			if m.mode == viewChassis && (m.chassisResp == nil || len(m.chassisNodes) == 0) {
				return m, m.fetchChassisCmd()
			}
			return m, nil
		case "1":
			m.mode = viewTree
			m.selectedIdx = 0
			return m, nil
		case "2":
			m.mode = viewL2
			m.selectedIdx = 0
			switches := m.switchList()
			if len(switches) > 0 {
				m.focusedLS = switches[0].Id
				m.focusedLSName = switches[0].Name
				if len(m.switchWorkloads[m.focusedLS]) == 0 {
					return m, m.fetchL2Cmd(m.focusedLS)
				}
				m.l2Nodes = m.switchWorkloads[m.focusedLS]
			}
			return m, nil
		case "3":
			m.mode = viewChassis
			m.selectedIdx = 0
			if m.chassisResp == nil || len(m.chassisNodes) == 0 {
				return m, m.fetchChassisCmd()
			}
			return m, nil
		case "4":
			m.mode = viewMatrix
			m.selectedIdx = 0
			return m, nil
		case " ", "enter":
			if m.mode == viewTree && len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
				node := m.nodeList[m.selectedIdx]
				if node.Type == api.GraphNodeTypeLS {
					m.expandedSwitches[node.Id] = !m.expandedSwitches[node.Id]
					if m.expandedSwitches[node.Id] && len(m.switchWorkloads[node.Id]) == 0 {
						return m, m.fetchL2Cmd(node.Id)
					}
				}
			}
			return m, nil
		case "s":
			if m.mode == viewL2 {
				m.cycleFocusedSwitch()
				return m, m.fetchL2Cmd(m.focusedLS)
			}
			return m, nil
		case "r":
			m.status = "Refreshing..."
			return m, m.fetchGraphCmd()
		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
				if m.mode == viewL2 {
					switches := m.switchList()
					if m.selectedIdx < len(switches) {
						m.focusedLS = switches[m.selectedIdx].Id
						m.focusedLSName = switches[m.selectedIdx].Name
						if len(m.switchWorkloads[m.focusedLS]) == 0 {
							return m, m.fetchL2Cmd(m.focusedLS)
						}
						m.l2Nodes = m.switchWorkloads[m.focusedLS]
					}
				}
			}
		case "down", "j":
			maxIdx := 0
			switch m.mode {
			case viewTree:
				maxIdx = len(m.nodeList) - 1
			case viewL2:
				maxIdx = len(m.switchList()) - 1
			case viewChassis:
				maxIdx = len(m.chassisNodes) - 1
			case viewMatrix:
				maxIdx = len(m.portList) - 1
			}
			if m.selectedIdx < maxIdx {
				m.selectedIdx++
				if m.mode == viewL2 {
					switches := m.switchList()
					if m.selectedIdx < len(switches) {
						m.focusedLS = switches[m.selectedIdx].Id
						m.focusedLSName = switches[m.selectedIdx].Name
						if len(m.switchWorkloads[m.focusedLS]) == 0 {
							return m, m.fetchL2Cmd(m.focusedLS)
						}
						m.l2Nodes = m.switchWorkloads[m.focusedLS]
					}
				}
			}
		}
	}

	return m, nil
}

func extractCleanIP(raw string) string {
	parts := strings.Fields(raw)
	for _, part := range parts {
		if part == "router" || part == "unknown" || part == "dynamic" {
			continue
		}
		if parsed := net.ParseIP(part); parsed != nil {
			return parsed.String()
		}
		if strings.Contains(part, "/") {
			ipPart := strings.Split(part, "/")[0]
			if parsed := net.ParseIP(ipPart); parsed != nil {
				return parsed.String()
			}
		}
	}
	return ""
}

func extractCleanMAC(raw string) string {
	parts := strings.Fields(raw)
	for _, part := range parts {
		if _, err := net.ParseMAC(part); err == nil {
			return part
		}
	}
	return ""
}

func (m model) resolveNodeName(candidate string) string {
	if candidate == "" {
		return ""
	}
	// Check if candidate matches any chassis hostname or nodeName in chassisNodes
	for _, ch := range m.chassisNodes {
		if ch.Id == candidate || ch.Name == candidate || strings.TrimPrefix(ch.Id, graph.PrefixChassis) == candidate {
			if ch.Properties != nil {
				p := *ch.Properties
				if nodeName, ok := p["nodeName"].(string); ok && nodeName != "" {
					return nodeName
				}
				if host, ok := p["hostname"].(string); ok && host != "" {
					return host
				}
			}
			return ch.Name
		}
	}
	// Check if candidate matches nodeName in nodeList
	for _, n := range m.nodeList {
		if n.Type == api.GraphNodeTypeChassis {
			if n.Id == candidate || strings.TrimPrefix(n.Id, graph.PrefixChassis) == candidate {
				return n.Name
			}
		}
	}
	// Check in nodeMap
	if n, ok := m.nodeMap[graph.PrefixChassis+candidate]; ok {
		return n.Name
	}
	if n, ok := m.nodeMap[candidate]; ok && n.Type == api.GraphNodeTypeChassis {
		return n.Name
	}
	return candidate
}

func (m model) selectedNodeName() string {
	raw := m.rawSelectedNodeName()
	return m.resolveNodeName(raw)
}

func (m model) rawSelectedNodeName() string {
	if m.mode == viewMatrix && len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
		conn := m.portList[m.selectedIdx]
		if conn.ActiveGateway != "" {
			return conn.ActiveGateway
		}
	} else if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
		node := m.nodeList[m.selectedIdx]
		if node.Type == api.GraphNodeTypeLSP && node.K8s != nil && node.K8s.Node != nil && *node.K8s.Node != "" {
			return *node.K8s.Node
		}
		if node.Type == api.GraphNodeTypeLS {
			workloads := m.switchWorkloads[node.Id]
			for _, w := range workloads {
				if w.Properties != nil {
					p := *w.Properties
					if portType, _ := p["type"].(string); portType == "router" {
						continue
					}
				}
				if w.K8s != nil && w.K8s.Node != nil && *w.K8s.Node != "" {
					return *w.K8s.Node
				}
			}
			if len(workloads) > 0 && workloads[0].K8s != nil && workloads[0].K8s.Node != nil && *workloads[0].K8s.Node != "" {
				return *workloads[0].K8s.Node
			}
		}
	}
	for _, conn := range m.portList {
		if conn.ActiveGateway != "" {
			return conn.ActiveGateway
		}
	}
	return ""
}

func (m model) selectedTargetIP() string {
	if m.mode == viewMatrix && len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
		conn := m.portList[m.selectedIdx]
		if len(conn.Networks) > 0 {
			return extractCleanIP(conn.Networks[0])
		}
	} else if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
		node := m.nodeList[m.selectedIdx]
		if node.Type == api.GraphNodeTypeLSP && node.Properties != nil {
			p := *node.Properties
			if addrs, ok := p["addresses"].([]interface{}); ok {
				for _, a := range addrs {
					if s, ok := a.(string); ok {
						if ip := extractCleanIP(s); ip != "" {
							return ip
						}
					}
				}
			}
		}
		if node.Type == api.GraphNodeTypeLS {
			workloads := m.switchWorkloads[node.Id]
			for _, w := range workloads {
				if w.Properties != nil {
					p := *w.Properties
					if portType, _ := p["type"].(string); portType == "router" {
						continue
					}
					if addrs, ok := p["addresses"].([]interface{}); ok {
						for _, a := range addrs {
							if s, ok := a.(string); ok {
								if ip := extractCleanIP(s); ip != "" {
									return ip
								}
							}
						}
					}
				}
			}
		}
		if ports, ok := m.routerPorts[node.Id]; ok && len(ports) > 0 {
			if len(ports[0].Networks) > 0 {
				return extractCleanIP(ports[0].Networks[0])
			}
		}
	}
	return ""
}

func (m model) selectedPortName() string {
	if m.mode == viewMatrix && len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
		conn := m.portList[m.selectedIdx]
		if conn.PeerPortName != "" {
			return conn.PeerPortName
		}
		return conn.PortName
	} else if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
		node := m.nodeList[m.selectedIdx]
		if node.Type == api.GraphNodeTypeLSP {
			return node.Name
		}
		if node.Type == api.GraphNodeTypeLS {
			workloads := m.switchWorkloads[node.Id]
			for _, w := range workloads {
				if w.Properties != nil {
					p := *w.Properties
					if portType, _ := p["type"].(string); portType != "router" {
						return w.Name
					}
				}
			}
			if len(workloads) > 0 {
				return workloads[0].Name
			}
		}
		if ports, ok := m.routerPorts[node.Id]; ok && len(ports) > 0 {
			if ports[0].PeerPortName != "" {
				return ports[0].PeerPortName
			}
			return ports[0].PortName
		} else if ports, ok := m.switchPorts[node.Id]; ok && len(ports) > 0 {
			if ports[0].PeerPortName != "" {
				return ports[0].PeerPortName
			}
			return ports[0].PortName
		}
	}
	return ""
}

func (m *model) cycleFocusedSwitch() {
	var switches []api.GraphNode
	for _, n := range m.nodeList {
		if n.Type == api.GraphNodeTypeLS {
			switches = append(switches, n)
		}
	}
	if len(switches) == 0 {
		return
	}

	currentIdx := -1
	for i, s := range switches {
		if s.Id == m.focusedLS || s.Name == m.focusedLSName {
			currentIdx = i
			break
		}
	}

	nextIdx := (currentIdx + 1) % len(switches)
	m.focusedLS = switches[nextIdx].Id
	m.focusedLSName = switches[nextIdx].Name
	m.selectedIdx = 0
}

func (m model) collectTraceDestinations(sourceNode *api.GraphNode, sourceIP string) []TraceDestination {
	var dests []TraceDestination
	seenIPs := make(map[string]bool)
	if sourceIP != "" {
		seenIPs[sourceIP] = true
	}

	// 1. Workload Destinations (VMs and Pods across all subnets)
	for lsID, workloads := range m.switchWorkloads {
		swName := lsID
		if node, ok := m.nodeMap[lsID]; ok {
			swName = node.Name
		}
		for _, w := range workloads {
			if w.Properties != nil {
				p := *w.Properties
				portType, _ := p["type"].(string)
				if portType == "router" {
					continue
				}
				if addrs, ok := p["addresses"].([]interface{}); ok {
					for _, a := range addrs {
						if s, ok := a.(string); ok {
							ip := extractCleanIP(s)
							if ip != "" && !seenIPs[ip] {
								seenIPs[ip] = true
								kind := "Pod"
								category := "Pod"
								name := w.Name
								if w.K8s != nil {
									if w.K8s.WorkloadKind != nil && *w.K8s.WorkloadKind != "" {
										kind = *w.K8s.WorkloadKind
										if strings.EqualFold(kind, "VirtualMachine") || strings.EqualFold(kind, "VM") {
											category = "VM"
										}
									}
									if w.K8s.WorkloadName != nil && *w.K8s.WorkloadName != "" {
										name = *w.K8s.WorkloadName
									}
								}
								dests = append(dests, TraceDestination{
									Label:    fmt.Sprintf("[%s] %s (IP: %s on %s)", kind, name, ip, swName),
									IP:       ip,
									Subnet:   swName,
									Category: category,
								})
							}
						}
					}
				}
			}
		}
	}

	// 2. Gateway Destinations (Logical Router Ports)
	for _, conn := range m.portList {
		for _, netCIDR := range conn.Networks {
			ip := extractCleanIP(netCIDR)
			if ip != "" && !seenIPs[ip] {
				seenIPs[ip] = true
				category := "Subnet Gateway"
				role := conn.RouterName
				if conn.ActiveGateway != "" {
					category = "External Gateway"
					role = fmt.Sprintf("%s (Active: %s)", conn.RouterName, conn.ActiveGateway)
				}
				dests = append(dests, TraceDestination{
					Label:    fmt.Sprintf("%s: %s (IP: %s on %s)", category, role, ip, conn.SwitchName),
					IP:       ip,
					Subnet:   conn.SwitchName,
					Category: "Gateway",
				})
			}
		}
	}

	// 3. Cluster Nodes / Physical Hosts
	for _, ch := range m.chassisNodes {
		nodeName := ch.Name
		if ch.Properties != nil {
			p := *ch.Properties
			if n, ok := p["nodeName"].(string); ok && n != "" {
				nodeName = n
			} else if h, ok := p["hostname"].(string); ok && h != "" {
				nodeName = h
			}
			if encaps, ok := p["encaps"].([]interface{}); ok {
				for _, enc := range encaps {
					if encMap, ok := enc.(map[string]interface{}); ok {
						if encIP, ok := encMap["ip"].(string); ok && encIP != "" && !seenIPs[encIP] {
							seenIPs[encIP] = true
							dests = append(dests, TraceDestination{
								Label:    fmt.Sprintf("[Node] %s (IP: %s)", nodeName, encIP),
								IP:       encIP,
								Subnet:   "physical",
								Category: "Node",
							})
						}
					}
				}
			} else if encMaps, ok := p["encaps"].([]map[string]interface{}); ok {
				for _, encMap := range encMaps {
					if encIP, ok := encMap["ip"].(string); ok && encIP != "" && !seenIPs[encIP] {
						seenIPs[encIP] = true
						dests = append(dests, TraceDestination{
							Label:    fmt.Sprintf("[Node] %s (IP: %s)", nodeName, encIP),
							IP:       encIP,
							Subnet:   "physical",
							Category: "Node",
						})
					}
				}
			}
		}
	}

	// 4. Well-known Public Internet & DNS
	dests = append(dests,
		TraceDestination{
			Label:    "Cloudflare Public DNS: 1.1.1.1",
			IP:       "1.1.1.1",
			Subnet:   "external",
			Category: "Public DNS",
		},
		TraceDestination{
			Label:    "Google Public DNS: 8.8.8.8",
			IP:       "8.8.8.8",
			Subnet:   "external",
			Category: "Public DNS",
		},
		TraceDestination{
			Label:    "Quad9 Public DNS: 9.9.9.9",
			IP:       "9.9.9.9",
			Subnet:   "external",
			Category: "Public DNS",
		},
	)

	return dests
}

func (m model) filteredTraceDestinations() []TraceDestination {
	var list []TraceDestination
	cleanTyped := strings.TrimSpace(m.traceSearchFilter)
	query := strings.ToLower(cleanTyped)

	catFilter := "All"
	if m.traceCategoryIdx >= 0 && m.traceCategoryIdx < len(TraceCategories) {
		catFilter = TraceCategories[m.traceCategoryIdx]
	}

	// 1. If user typed an IP address, offer it as Custom Destination at the top
	typedHost := cleanTyped
	if strings.Contains(cleanTyped, ":") {
		typedHost = strings.Split(cleanTyped, ":")[0]
	}
	if parsed := net.ParseIP(typedHost); parsed != nil {
		list = append(list, TraceDestination{
			Label:    fmt.Sprintf("⚡ Custom IP Target: %s (press Enter to trace)", cleanTyped),
			IP:       parsed.String(),
			Subnet:   "custom",
			Category: "Custom",
		})
	}

	for _, d := range m.traceDestinations {
		if catFilter != "All" && d.Category != catFilter {
			continue
		}
		if query != "" {
			labelMatch := strings.Contains(strings.ToLower(d.Label), query)
			ipMatch := strings.Contains(d.IP, query)
			subMatch := strings.Contains(strings.ToLower(d.Subnet), query)
			catMatch := strings.Contains(strings.ToLower(d.Category), query)
			if !labelMatch && !ipMatch && !subMatch && !catMatch {
				continue
			}
		}
		list = append(list, d)
	}

	return list
}

func (m model) resolveTraceEndpoints(targetDstIP string, proto string, dstPort int) diag.TraceRequest {
	req := diag.TraceRequest{
		Datapath: "ovn-default",
		Inport:   "",
		SrcIP:    "10.244.0.1",
		DstIP:    targetDstIP,
		Protocol: proto,
		DstPort:  dstPort,
	}

	var conn *RouterPortConnection
	var selectedWorkload *api.GraphNode
	var switchID string
	var switchName string

	// 1. Identify what is selected in the current view
	if m.mode == viewMatrix && len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
		conn = &m.portList[m.selectedIdx]
	} else if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
		node := m.nodeList[m.selectedIdx]
		if node.Type == api.GraphNodeTypeLSP {
			selectedWorkload = &node
			// Find which switch this workload is on
			for lsID, workloads := range m.switchWorkloads {
				for _, w := range workloads {
					if w.Id == node.Id || w.Name == node.Name {
						switchID = lsID
						if sn, ok := m.nodeMap[lsID]; ok {
							switchName = sn.Name
						}
						break
					}
				}
				if switchID != "" {
					break
				}
			}
			if switchID != "" && len(m.switchPorts[switchID]) > 0 {
				conn = &m.switchPorts[switchID][0]
			}
		} else if node.Type == api.GraphNodeTypeLS {
			switchID = node.Id
			switchName = node.Name
			workloads := m.switchWorkloads[node.Id]
			for _, w := range workloads {
				if w.Properties != nil {
					p := *w.Properties
					if portType, _ := p["type"].(string); portType != "router" {
						selectedWorkload = &w
						break
					}
				}
			}
			if len(m.switchPorts[node.Id]) > 0 {
				conn = &m.switchPorts[node.Id][0]
			}
		} else if ports, ok := m.routerPorts[node.Id]; ok && len(ports) > 0 {
			conn = &ports[0]
		}
	}

	if conn == nil && len(m.portList) > 0 {
		conn = &m.portList[0]
	}

	// 2. Resolve Gateway MAC from conn
	var gwMAC string
	if conn != nil {
		gwMAC = extractCleanMAC(conn.MAC)
		if switchName == "" {
			switchName = conn.SwitchName
		}
	}

	// 3. If we have a source workload (VM or Pod):
	if selectedWorkload != nil {
		req.Inport = selectedWorkload.Name
		if switchName != "" {
			req.Datapath = switchName
		} else if conn != nil && conn.SwitchName != "" {
			req.Datapath = conn.SwitchName
		}

		if selectedWorkload.Properties != nil {
			p := *selectedWorkload.Properties
			if addrs, ok := p["addresses"].([]interface{}); ok {
				for _, a := range addrs {
					if s, ok := a.(string); ok {
						if mac := extractCleanMAC(s); mac != "" && req.SrcMAC == "" {
							req.SrcMAC = mac
						}
						if ip := extractCleanIP(s); ip != "" && (req.SrcIP == "" || req.SrcIP == "10.244.0.1") {
							req.SrcIP = ip
						}
					}
				}
			}
		}

		// Destination MAC:
		// Check if destination IP belongs to the same local subnet
		var sameSubnet bool
		if conn != nil && len(conn.Networks) > 0 {
			for _, netCIDR := range conn.Networks {
				if _, ipNet, err := net.ParseCIDR(netCIDR); err == nil {
					if ipNet.Contains(net.ParseIP(req.DstIP)) {
						sameSubnet = true
						break
					}
				}
			}
		}

		if sameSubnet {
			// Find peer workload on this switch
			if switchID != "" {
				for _, w := range m.switchWorkloads[switchID] {
					if w.Properties != nil {
						p := *w.Properties
						if addrs, ok := p["addresses"].([]interface{}); ok {
							for _, a := range addrs {
								if s, ok := a.(string); ok {
									if extractCleanIP(s) == req.DstIP {
										req.DstMAC = extractCleanMAC(s)
										break
									}
								}
							}
						}
					}
					if req.DstMAC != "" {
						break
					}
				}
			}
		} else {
			// Remote subnet or external internet: must route through the Subnet Gateway!
			req.DstMAC = gwMAC
		}
		if req.DstMAC == "" && gwMAC != "" {
			req.DstMAC = gwMAC
		}
	} else if conn != nil {
		// Pure router port or no workload on switch: trace from router datapath directly!
		req.Datapath = conn.RouterName
		req.Inport = conn.PortName
		req.SrcMAC = extractCleanMAC(conn.MAC)
		if len(conn.Networks) > 0 {
			req.SrcIP = extractCleanIP(conn.Networks[0])
		}
		req.DstMAC = req.SrcMAC // In OVN, lr_in_admission table 0 requires eth.dst == router_mac
	}

	if req.DstIP == "" {
		req.DstIP = "1.1.1.1"
	}
	if req.Protocol == "" {
		req.Protocol = "icmp"
	}

	return req
}

func (m model) executeTrace(targetDstIP string, proto string, dstPort int) tea.Cmd {
	return func() tea.Msg {
		if m.diagExecutor == nil || m.diagLocator == nil {
			return diagExecResultMsg{err: fmt.Errorf("diagnostics executor not initialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		centralPod, err := m.diagLocator.FindOvnCentralPod(ctx)
		if err != nil {
			return diagExecResultMsg{err: fmt.Errorf("failed to locate ovn-central leader pod: %w", err)}
		}

		req := m.resolveTraceEndpoints(targetDstIP, proto, dstPort)

		cmd, err := diag.BuildTraceCommand(req)
		if err != nil {
			return diagExecResultMsg{err: err}
		}

		stdout, stderr, err := m.diagExecutor.Exec(ctx, centralPod, cmd)
		if err != nil {
			errMsg := string(stderr)
			if errMsg == "" {
				errMsg = err.Error()
			}
			return diagExecResultMsg{err: fmt.Errorf("ovn-trace failed: %s", errMsg)}
		}

		traceRes := diag.ParseTraceOutput(string(stdout))
		traceParams := fmt.Sprintf("Source (From):      [Datapath %s] inport '%s' (IP: %s, MAC: %s)\nDestination (To):   IP: %s (MAC: %s)\nProtocol Match:     %s (ip.ttl=255, 4x --ct=new)",
			req.Datapath, req.Inport, req.SrcIP, req.SrcMAC, req.DstIP, req.DstMAC, strings.ToUpper(req.Protocol))

		return diagExecResultMsg{
			action:      "trace",
			title:       fmt.Sprintf("ovn-trace: %s -> %s via %s", req.SrcIP, req.DstIP, req.Inport),
			traceRes:    &traceRes,
			traceParams: traceParams,
			rawOutput:   string(stdout),
		}
	}
}

func (m model) executeNodeDiag(action string, nodeName string, targetIP string) tea.Cmd {
	return func() tea.Msg {
		if m.diagExecutor == nil || m.diagLocator == nil {
			return diagExecResultMsg{err: fmt.Errorf("diagnostics executor not initialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if nodeName == "" && m.k8sClient != nil {
			nodes, err := m.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
			if err == nil && len(nodes.Items) > 0 {
				nodeName = nodes.Items[0].Name
			}
		}
		if nodeName == "" {
			nodeName = "cluster-node"
		}

		pod, err := m.diagLocator.FindNodeOVSPod(ctx, nodeName)
		if err != nil {
			return diagExecResultMsg{err: fmt.Errorf("failed to locate OVS pod on node %s: %w", nodeName, err)}
		}

		var cmd []string
		var title string

		switch action {
		case "conntrack":
			cmd = diag.BuildConntrackDumpCommand(0)
			title = fmt.Sprintf("Conntrack Table on node %s (IP: %s)", nodeName, targetIP)
		case "ovs-iface":
			cmd = diag.BuildOvsVsctlShowCommand()
			title = fmt.Sprintf("OVS Bridges & Interfaces (ovs-vsctl show) on node %s", nodeName)
		case "logs":
			cmd = diag.BuildOvnControllerLogTail(50)
			title = fmt.Sprintf("ovn-controller logs on node %s", nodeName)
		default:
			return diagExecResultMsg{err: fmt.Errorf("unknown diagnostic action: %s", action)}
		}

		stdout, stderr, err := m.diagExecutor.Exec(ctx, pod, cmd)
		if err != nil {
			errMsg := string(stderr)
			if errMsg == "" {
				errMsg = err.Error()
			}
			return diagExecResultMsg{err: fmt.Errorf("%s failed: %s", action, errMsg)}
		}

		out := string(stdout)
		if action == "conntrack" {
			out = diag.FormatConntrackOutput(out, targetIP)
		}

		return diagExecResultMsg{
			action:    action,
			title:     title,
			rawOutput: out,
		}
	}
}

func (m model) executeTcpdump(filter string, hintNode string, portName string, portIP string) tea.Cmd {
	return func() tea.Msg {
		if m.diagExecutor == nil || m.diagLocator == nil {
			return diagExecResultMsg{err: fmt.Errorf("diagnostics executor not initialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		nodeName, iface, err := m.diagLocator.FindOVSInterface(ctx, m.diagExecutor, hintNode, portName)
		if err != nil || nodeName == "" {
			if hintNode != "" {
				nodeName = hintNode
			} else {
				nodeName = "cluster-node"
			}
			if iface == "" {
				iface = "br-int"
			}
		}

		// If capturing on br-int, scope the capture to the port's IP
		effectiveFilter := filter
		if iface == "br-int" && portIP != "" && !strings.Contains(filter, "host") {
			effectiveFilter = fmt.Sprintf("(%s) and host %s", filter, portIP)
		}

		pod, err := m.diagLocator.FindNodeCNIPod(ctx, nodeName)
		if err != nil {
			return diagExecResultMsg{err: fmt.Errorf("failed to locate kube-ovn-cni pod on node %s: %w", nodeName, err)}
		}

		req := diag.TcpdumpRequest{
			Interface: iface,
			Filter:    effectiveFilter,
			Count:     15,
		}
		cmd, err := diag.BuildTcpdumpCommand(req)
		if err != nil {
			return diagExecResultMsg{err: err}
		}

		stdout, stderr, err := m.diagExecutor.Exec(ctx, pod, cmd)

		rawCombined := string(stdout)
		if rawCombined == "" && len(stderr) > 0 {
			rawCombined = string(stderr)
		}

		var packetLines []string
		for _, line := range strings.Split(string(stdout), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "tcpdump:") ||
				strings.HasPrefix(trimmed, "listening on") ||
				strings.Contains(trimmed, "packets captured") ||
				strings.Contains(trimmed, "packets received") ||
				strings.Contains(trimmed, "packets dropped") {
				continue
			}
			packetLines = append(packetLines, trimmed)
		}

		var raw string
		if len(packetLines) > 0 {
			raw = fmt.Sprintf("Captured %d packet(s) on dedicated interface '%s' (port: %s on node %s)\nFilter: %s\n\n%s",
				len(packetLines), iface, portName, nodeName, effectiveFilter, strings.Join(packetLines, "\n"))
		} else if strings.Contains(rawCombined, "0 packets captured") || (err != nil && strings.Contains(err.Error(), "124")) {
			raw = fmt.Sprintf("Listening on dedicated interface '%s' (port: %s on node %s)\nFilter: %s\n\n0 packets captured within 5s (Interface is currently quiet).",
				iface, portName, nodeName, effectiveFilter)
		} else if err != nil {
			errMsg := string(stderr)
			if errMsg == "" {
				errMsg = err.Error()
			}
			return diagExecResultMsg{err: fmt.Errorf("tcpdump failed: %s", errMsg)}
		} else {
			raw = fmt.Sprintf("Listening on dedicated interface '%s' (port: %s on node %s)\nFilter: %s\n\n0 packets captured within 5s (Interface is currently quiet).",
				iface, portName, nodeName, effectiveFilter)
		}

		return diagExecResultMsg{
			action:    "tcpdump",
			title:     fmt.Sprintf("Live tcpdump on %s (%s)", iface, portName),
			rawOutput: raw,
		}
	}
}

func (m model) View() string {
	var b strings.Builder

	// Top Title and View Tabs
	header := m.styles.Title.Render(" OVN Network Topology ")

	diagBadge := ""
	if m.diagMode {
		if m.diagAvailable {
			diagBadge = m.styles.DiagBadgeActive.Render(" DIAG ACTIVE ") + " "
		} else {
			diagBadge = m.styles.DiagBadgeDisabled.Render(" DIAG DEGRADED ") + " "
		}
	}

	tab1 := m.styles.TabInactive.Render("1. L3 Tree")
	tab2 := m.styles.TabInactive.Render("2. L2 Subnets")
	tab3 := m.styles.TabInactive.Render("3. Chassis")
	tab4 := m.styles.TabInactive.Render("4. Port Matrix")

	switch m.mode {
	case viewTree:
		tab1 = m.styles.TabActive.Render("1. L3 Tree")
	case viewL2:
		tab2 = m.styles.TabActive.Render("2. L2 Subnets")
	case viewChassis:
		tab3 = m.styles.TabActive.Render("3. Chassis")
	case viewMatrix:
		tab4 = m.styles.TabActive.Render("4. Port Matrix")
	}

	tabs := fmt.Sprintf("%s%s%s%s", tab1, tab2, tab3, tab4)
	diagShortcut := ""
	if m.diagAvailable {
		diagShortcut = " | x: diag"
	}

	statusText := m.styles.MutedText.Render(
		fmt.Sprintf(" %s [%s]  [Tab/1-4: view | Space: expand | t: theme%s | j/k: nav | r: refresh | q: quit]", m.status, m.theme.Name, diagShortcut))

	b.WriteString(header + " " + diagBadge + tabs + statusText + "\n\n")

	if m.diagModal {
		b.WriteString(m.renderDiagModal())
		return b.String()
	}

	if m.err != nil {
		b.WriteString(m.styles.BadgeUnbound.Render(fmt.Sprintf("Error: %v\n", m.err)))
		b.WriteString("\nTip: Run daemon with `ovn-topologyd` or test with `--fixture`\n")
		return b.String()
	}

	if m.graphResp == nil {
		b.WriteString("Loading topology...\n")
		return b.String()
	}

	switch m.mode {
	case viewL2:
		b.WriteString(m.renderL2View())
		return b.String()
	case viewChassis:
		b.WriteString(m.renderChassisView())
		return b.String()
	case viewMatrix:
		b.WriteString(m.renderMatrixView())
		return b.String()
	default:
		b.WriteString(m.renderTreeView())
		return b.String()
	}
}

func (m model) renderDiagModal() string {
	var box strings.Builder

	box.WriteString(m.styles.Header.Render("⚡ [TIER 1 DIAGNOSTICS - ADR-0001]") + "\n")
	box.WriteString(m.styles.MutedText.Render("Executed inside privileged cluster pods using operator credentials") + "\n\n")

	// Target context
	targetDesc := "Cluster Core"
	var currentConn *RouterPortConnection
	if m.mode == viewMatrix && len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
		currentConn = &m.portList[m.selectedIdx]
		targetDesc = fmt.Sprintf("Port: %s (Router: %s, Switch: %s, IP: %s)",
			currentConn.PortName, currentConn.RouterName, currentConn.SwitchName, strings.Join(currentConn.Networks, ", "))
	} else if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
		node := m.nodeList[m.selectedIdx]
		if node.Type == api.GraphNodeTypeLSP && node.K8s != nil && node.K8s.WorkloadName != nil {
			targetDesc = fmt.Sprintf("[%s] %s (IP: %s, Node: %s)",
				*node.K8s.WorkloadKind, *node.K8s.WorkloadName, m.selectedTargetIP(), m.selectedNodeName())
		} else if node.Type == api.GraphNodeTypeLS {
			targetDesc = fmt.Sprintf("Subnet: %s (Gateway: %s, Node: %s)", node.Name, m.selectedTargetIP(), m.selectedNodeName())
		} else {
			targetDesc = fmt.Sprintf("Node: %s (Type: %s)", node.Name, node.Type)
		}
	}

	fmt.Fprintf(&box, "  Target: %s\n\n", m.styles.PortBadge.Render(targetDesc))

	if m.diagRunning {
		box.WriteString(m.styles.ActiveGateway.Render("  ⏳ Executing diagnostic command inside pod...") + "\n")
		box.WriteString(m.styles.MutedText.Render("  Please wait while stream completes...") + "\n\n")
		return m.styles.ModalBox.Render(box.String())
	}

	if m.diagError != nil {
		box.WriteString(m.styles.BadgeUnbound.Render(fmt.Sprintf("  ❌ Diagnostic Error: %v", m.diagError)) + "\n\n")
	}

	if m.diagStep == diagStepPickDest {
		box.WriteString(m.styles.Header.Render("Choose Destination Target:") + "\n\n")

		// 1. Search / Custom IP Input Bar
		searchPrompt := "  Search / Custom IP: "
		cursorStr := "█"
		inputDisplay := m.traceSearchFilter
		if inputDisplay == "" {
			inputDisplay = m.styles.MutedText.Render("(type any IP address, node name, or keyword...)") + cursorStr
		} else {
			inputDisplay = m.styles.SelectedNode.Render(inputDisplay) + cursorStr
		}
		box.WriteString(m.styles.Header.Render(searchPrompt) + inputDisplay + "\n\n")

		// 2. Category Badges Bar
		counts := make(map[string]int)
		counts["All"] = len(m.traceDestinations)
		for _, d := range m.traceDestinations {
			counts[d.Category]++
		}

		var catBadges []string
		for i, cat := range TraceCategories {
			count := counts[cat]
			label := fmt.Sprintf("[%s (%d)]", cat, count)
			if i == m.traceCategoryIdx {
				catBadges = append(catBadges, m.styles.TabActive.Render(label))
			} else {
				catBadges = append(catBadges, m.styles.TabInactive.Render(label))
			}
		}
		box.WriteString("  Category: " + strings.Join(catBadges, " ") + "\n\n")

		// 3. Filtered Destinations List
		filtered := m.filteredTraceDestinations()
		if len(filtered) == 0 {
			box.WriteString(m.styles.MutedText.Render("    (no destinations matching filter; type an IP to trace directly)") + "\n\n")
		} else {
			if m.selectedDestIdx >= len(filtered) {
				m.selectedDestIdx = len(filtered) - 1
			}
			if m.selectedDestIdx < 0 {
				m.selectedDestIdx = 0
			}

			visibleLimit := 10
			startIdx := 0
			if m.selectedDestIdx >= visibleLimit {
				startIdx = m.selectedDestIdx - visibleLimit + 1
			}
			endIdx := startIdx + visibleLimit
			if endIdx > len(filtered) {
				endIdx = len(filtered)
			}

			currentCat := ""
			for i := startIdx; i < endIdx; i++ {
				d := filtered[i]
				if d.Category != currentCat && (TraceCategories[m.traceCategoryIdx] == "All" || d.Category == "Custom") {
					currentCat = d.Category
					box.WriteString(m.styles.MutedText.Render(fmt.Sprintf("  ── %s ──", currentCat)) + "\n")
				}

				isSel := i == m.selectedDestIdx
				cursor := "  "
				if isSel {
					cursor = "► "
				}
				line := fmt.Sprintf("%s[%d] %s", cursor, i+1, d.Label)
				if isSel {
					box.WriteString("  " + m.styles.SelectedNode.Render(line) + "\n")
				} else {
					box.WriteString("  " + m.styles.Node.Render(line) + "\n")
				}
			}
			if len(filtered) > visibleLimit {
				box.WriteString(m.styles.MutedText.Render(fmt.Sprintf("    ... (showing %d-%d of %d matches)", startIdx+1, endIdx, len(filtered))) + "\n")
			}
		}

		// 4. Protocol Selector Bar
		var protoBadges []string
		for i, opt := range AvailableTraceProtocols {
			label := fmt.Sprintf("[%d] %s", i+1, opt.Label)
			if i == m.traceProtoIdx {
				protoBadges = append(protoBadges, m.styles.ActiveGateway.Render("► "+label))
			} else {
				protoBadges = append(protoBadges, m.styles.MutedText.Render(label))
			}
		}

		box.WriteString("\n  Protocol: " + strings.Join(protoBadges, " | ") + "\n\n")
		box.WriteString(m.styles.MutedText.Render("  [Type: filter / enter IP | Tab: category | Ctrl+P or F1-F5: proto | Enter: trace | Esc: back]"))

		return m.styles.ModalBox.Render(box.String())
	}

	if m.diagRawOutput != "" || m.diagTraceRes != nil {
		fmt.Fprintf(&box, "  %s\n\n", m.styles.Header.Render("Result: "+m.diagTitle))

		var contentText string
		if m.diagAction == "trace" && m.diagTraceRes != nil && !m.diagRawView {
			var traceContent strings.Builder
			tr := m.diagTraceRes
			verdictStyle := m.styles.BadgeBound
			if tr.Verdict != "delivered" {
				verdictStyle = m.styles.BadgeUnbound
			}
			fmt.Fprintf(&traceContent, "Verdict: %s\n", verdictStyle.Render(strings.ToUpper(tr.Verdict)))
			fmt.Fprintf(&traceContent, "Summary: %s\n\n", tr.Summary)

			if m.diagTraceParams != "" {
				traceContent.WriteString("Simulation Endpoints:\n")
				for _, pLine := range strings.Split(m.diagTraceParams, "\n") {
					traceContent.WriteString("  • " + pLine + "\n")
				}
				traceContent.WriteString("\n")
			}

			if tr.HopPath != "" {
				traceContent.WriteString("Hop-by-Hop Flow Path:\n")
				traceContent.WriteString("  " + m.styles.ActiveGateway.Render(tr.HopPath) + "\n\n")
			}

			traceContent.WriteString("Pipeline Stages Evaluated:\n")
			for _, st := range tr.Stages {
				fmt.Fprintf(&traceContent, "  • [%s table %d] %-24s ➔ %s\n",
					st.Pipeline, st.Table, st.Name, truncate(st.Action, 48))
			}
			contentText = traceContent.String()
		} else {
			rawText := m.diagRawOutput
			if m.diagRawView {
				rawText = diag.FormatJsonOutput(rawText)
			}
			contentText = rawText
		}

		lines := strings.Split(strings.TrimSpace(contentText), "\n")
		visibleLines := m.height - 16
		if visibleLines < 10 {
			visibleLines = 10
		}

		maxOffset := len(lines) - visibleLines
		if maxOffset < 0 {
			maxOffset = 0
		}
		if m.diagScrollOffset > maxOffset {
			m.diagScrollOffset = maxOffset
		}

		endIdx := m.diagScrollOffset + visibleLines
		if endIdx > len(lines) {
			endIdx = len(lines)
		}

		displayedLines := lines[m.diagScrollOffset:endIdx]
		box.WriteString("  " + strings.Join(displayedLines, "\n  ") + "\n\n")

		scrollHint := fmt.Sprintf("  [Lines %d-%d of %d | ↑/↓ or j/k: scroll | o: toggle raw | 1-7: run action | Esc/x: close]",
			m.diagScrollOffset+1, endIdx, len(lines))
		box.WriteString(m.styles.MutedText.Render(scrollHint))
		return m.styles.ModalBox.Render(box.String())
	}

	targetIP := m.selectedTargetIP()
	ipFilterLabel := "host " + targetIP
	if targetIP == "" || net.ParseIP(targetIP) == nil {
		ipFilterLabel = "ip"
	}

	nodeName := m.selectedNodeName()
	targetNodeLabel := "local node"
	if nodeName != "" {
		targetNodeLabel = fmt.Sprintf("node '%s'", nodeName)
	}

	box.WriteString(m.styles.Header.Render("  Choose a Diagnostic Action:") + "\n")
	box.WriteString("    [1] ovn-trace (Microflow trace from active port to peer switch/gateway)\n")
	fmt.Fprintf(&box, "    [2] Live tcpdump: ICMP traffic (Ping / Echo trace on %s)\n", targetNodeLabel)
	fmt.Fprintf(&box, "    [3] Live tcpdump: TCP traffic (All TCP connections on %s)\n", targetNodeLabel)
	fmt.Fprintf(&box, "    [4] Live tcpdump: Port IP traffic (%s on %s)\n", ipFilterLabel, targetNodeLabel)
	fmt.Fprintf(&box, "    [5] Filtered Conntrack (Active flows for port IP %s on %s)\n", targetIP, targetNodeLabel)
	fmt.Fprintf(&box, "    [6] OVS Physical Bridges & Tunnels (ovs-vsctl show on %s)\n", targetNodeLabel)
	fmt.Fprintf(&box, "    [7] Tail ovn-controller Logs (on %s)\n\n", targetNodeLabel)

	box.WriteString(m.styles.MutedText.Render("  Press 1-7 to execute | Esc or x to cancel"))

	return m.styles.ModalBox.Render(box.String())
}

func (m model) renderTreeView() string {
	var leftPanel strings.Builder

	var selectedNode *api.GraphNode
	if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
		selectedNode = &m.nodeList[m.selectedIdx]
	}

	// Render VPC compound boxes in deterministic order
	for _, vpcName := range m.vpcNames {
		nodes := m.vpcGroups[vpcName]
		var boxContent strings.Builder
		boxContent.WriteString(m.styles.Header.Render(fmt.Sprintf("VPC: %s", vpcName)) + "\n")

		for _, n := range nodes {
			isSel := selectedNode != nil && selectedNode.Id == n.Id
			line := formatNodeLine(n, m.styles)

			isExpanded := m.expandedSwitches[n.Id]
			expandHint := ""
			if n.Type == api.GraphNodeTypeLS {
				if isExpanded {
					expandHint = m.styles.MutedText.Render(" (expanded, press space to collapse)")
				} else {
					expandHint = m.styles.MutedText.Render(" (press space to expand workloads)")
				}
			}

			cursor := "  "
			if isSel {
				cursor = "► "
				boxContent.WriteString(m.styles.SelectedNode.Render(cursor+line) + expandHint + "\n")
			} else {
				boxContent.WriteString(m.styles.Node.Render(cursor+line) + expandHint + "\n")
			}

			// If switch is expanded, draw attached workload ports
			if isExpanded && n.Type == api.GraphNodeTypeLS {
				workloads := m.switchWorkloads[n.Id]
				if len(workloads) == 0 {
					boxContent.WriteString("      " + m.styles.MutedText.Render("└── (no active workloads attached or loading...)") + "\n")
				} else {
					for wIdx, w := range workloads {
						wLast := wIdx == len(workloads)-1
						wGlyph := "├──"
						if wLast {
							wGlyph = "└──"
						}
						wDesc := formatWorkloadBadge(w, m.styles)
						fmt.Fprintf(&boxContent, "      %s %s\n",
							m.styles.EdgeLine.Render(wGlyph), wDesc)
					}
				}
			}

			// If it's a Router, draw its connected LRP branch ports directly beneath it!
			if n.Type == api.GraphNodeTypeLR {
				ports := m.routerPorts[n.Id]
				for idx, conn := range ports {
					isLast := idx == len(ports)-1
					glyph := "├──"
					if isLast {
						glyph = "└──"
					}
					cidr := strings.Join(conn.Networks, ", ")
					peerInfo := fmt.Sprintf("[Switch] %s", conn.SwitchName)
					if conn.IsShared {
						peerInfo += " (interconnect)"
					}
					gwInfo := ""
					if conn.ActiveGateway != "" {
						gwInfo = m.styles.ActiveGateway.Render(fmt.Sprintf(" [GW: %s]", conn.ActiveGateway))
					}

					var ipLabel string
					if conn.ActiveGateway != "" {
						ipLabel = m.styles.ActiveGateway.Render("[EIP: " + cidr + "]")
					} else if !conn.IsShared {
						ipLabel = m.styles.CIDR.Render("[GW IP: " + cidr + "]")
					} else {
						ipLabel = m.styles.CIDR.Render("[Transit IP: " + cidr + "]")
					}

					branch := fmt.Sprintf("      %s (%s) %s ──► %s%s",
						m.styles.EdgeLine.Render(glyph),
						m.styles.PortBadge.Render(conn.PortName),
						ipLabel,
						peerInfo,
						gwInfo)
					boxContent.WriteString(branch + "\n")
				}
			}
		}
		leftPanel.WriteString(m.styles.VPCBox.Render(boxContent.String()) + "\n")
	}

	// Render Shared / Interconnect switches (e.g. join)
	if len(m.sharedNodes) > 0 {
		var sharedContent strings.Builder
		sharedContent.WriteString(m.styles.Header.Render("Interconnects (Multi-LR Subnets)") + "\n")
		for _, n := range m.sharedNodes {
			isSel := selectedNode != nil && selectedNode.Id == n.Id
			line := formatNodeLine(n, m.styles)
			cursor := "  "
			isExpanded := m.expandedSwitches[n.Id]
			expandHint := ""
			if n.Type == api.GraphNodeTypeLS {
				if isExpanded {
					expandHint = m.styles.MutedText.Render(" (expanded, press space to collapse)")
				} else {
					expandHint = m.styles.MutedText.Render(" (press space to expand workloads)")
				}
			}

			if isSel {
				cursor = "► "
				sharedContent.WriteString(m.styles.SelectedNode.Render(cursor+line) + expandHint + "\n")
			} else {
				sharedContent.WriteString(m.styles.Node.Render(cursor+line) + expandHint + "\n")
			}

			if isExpanded && n.Type == api.GraphNodeTypeLS {
				workloads := m.switchWorkloads[n.Id]
				if len(workloads) > 0 {
					for wIdx, w := range workloads {
						wLast := wIdx == len(workloads)-1
						wGlyph := "├──"
						if wLast {
							wGlyph = "└──"
						}
						wDesc := formatWorkloadBadge(w, m.styles)
						fmt.Fprintf(&sharedContent, "      %s %s\n",
							m.styles.EdgeLine.Render(wGlyph), wDesc)
					}
				}
			}

			// Show routers connecting into this switch
			conns := m.switchPorts[n.Id]
			for idx, conn := range conns {
				isLast := idx == len(conns)-1
				glyph := "├──"
				if isLast {
					glyph = "└──"
				}
				cidr := strings.Join(conn.Networks, ", ")
				branch := fmt.Sprintf("      %s ◄── (%s) %s ── [Router] %s (%s)",
					m.styles.EdgeLine.Render(glyph),
					m.styles.PortBadge.Render(conn.PortName),
					m.styles.CIDR.Render(cidr),
					conn.RouterName,
					conn.VPCName)
				sharedContent.WriteString(branch + "\n")
			}
		}
		leftPanel.WriteString(m.styles.VPCBox.Render(sharedContent.String()) + "\n")
	}

	// Render External nodes
	if len(m.externalNodes) > 0 {
		var extContent strings.Builder
		extContent.WriteString(m.styles.Header.Render("External Provider Underlays") + "\n")
		for _, n := range m.externalNodes {
			isSel := selectedNode != nil && selectedNode.Id == n.Id
			cursor := "  "
			if isSel {
				cursor = "► "
				extContent.WriteString(m.styles.SelectedNode.Render(fmt.Sprintf("%s[EXT] %s", cursor, n.Name)) + "\n")
			} else {
				extContent.WriteString(m.styles.Node.Render(fmt.Sprintf("%s[EXT] %s", cursor, n.Name)) + "\n")
			}
		}
		leftPanel.WriteString(m.styles.VPCBox.Render(extContent.String()) + "\n")
	}

	// Right panel: detailed breakdown of selected entity
	var rightPanel strings.Builder
	if selectedNode != nil {
		sel := *selectedNode
		rightPanel.WriteString(m.styles.Header.Render("Selected: "+sel.Name) + "\n")
		fmt.Fprintf(&rightPanel, "Type: %s\n", sel.Type)
		fmt.Fprintf(&rightPanel, "ID:   %s\n", sel.Id)
		if sel.Parent != nil {
			fmt.Fprintf(&rightPanel, "VPC:  %s\n", strings.TrimPrefix(*sel.Parent, graph.PrefixVPC))
		}

		switch sel.Type {
		case api.GraphNodeTypeLR:
			// Router port interconnection list
			ports := m.routerPorts[sel.Id]
			fmt.Fprintf(&rightPanel, "\n%s (%d):\n", m.styles.Header.Render("Ports & Interconnections"), len(ports))
			if len(ports) == 0 {
				rightPanel.WriteString("  (no connected ports)\n")
			}
			for _, conn := range ports {
				fmt.Fprintf(&rightPanel, "• %s:\n", m.styles.PortBadge.Render(conn.PortName))
				fmt.Fprintf(&rightPanel, "    CIDR:   %s\n", m.styles.CIDR.Render(strings.Join(conn.Networks, ", ")))
				if conn.MAC != "" {
					fmt.Fprintf(&rightPanel, "    MAC:    %s\n", conn.MAC)
				}
				peerText := fmt.Sprintf("[Switch] %s", conn.SwitchName)
				if conn.IsShared {
					peerText += " (shared interconnect)"
				}
				fmt.Fprintf(&rightPanel, "    Target: %s\n", peerText)
				if conn.ActiveGateway != "" {
					fmt.Fprintf(&rightPanel, "    Status: %s\n", m.styles.ActiveGateway.Render("Active GW on "+conn.ActiveGateway))
				}
			}

			if sel.Properties != nil {
				p := *sel.Properties
				rightPanel.WriteString("\n" + m.styles.Header.Render("L3 Statistics:") + "\n")
				fmt.Fprintf(&rightPanel, "  Static Routes: %v\n", p["routeCount"])
				fmt.Fprintf(&rightPanel, "  NAT Rules:     %v\n", p["natCount"])
				fmt.Fprintf(&rightPanel, "  Policies:      %v\n", p["policyCount"])
				fmt.Fprintf(&rightPanel, "  LoadBalancers: %v\n", p["lbCount"])
			}
		case api.GraphNodeTypeLS:
			// Switch attachments list
			routers := m.switchPorts[sel.Id]
			fmt.Fprintf(&rightPanel, "\n%s (%d):\n", m.styles.Header.Render("Connected Routers"), len(routers))
			if len(routers) == 0 {
				rightPanel.WriteString("  (standalone switch)\n")
			}
			for _, conn := range routers {
				fmt.Fprintf(&rightPanel, "• %s (%s):\n", conn.RouterName, conn.VPCName)
				fmt.Fprintf(&rightPanel, "    Router Port: %s\n", m.styles.PortBadge.Render(conn.PortName))
				fmt.Fprintf(&rightPanel, "    Subnet/CIDR: %s\n", m.styles.CIDR.Render(strings.Join(conn.Networks, ", ")))
			}

			if sel.Properties != nil {
				p := *sel.Properties
				rightPanel.WriteString("\n" + m.styles.Header.Render("L2 Port Summary:") + "\n")
				fmt.Fprintf(&rightPanel, "  Total Ports:   %v\n", p["portCount"])
				fmt.Fprintf(&rightPanel, "  Bound Ports:   %s\n", m.styles.BadgeBound.Render(fmt.Sprintf("%v", p["boundPortCount"])))
				fmt.Fprintf(&rightPanel, "  Unbound Ports: %s\n", m.styles.BadgeUnbound.Render(fmt.Sprintf("%v", p["unboundPortCount"])))
			}
		}

		if sel.K8s != nil {
			rightPanel.WriteString("\n" + m.styles.Header.Render("Kubernetes Metadata:") + "\n")
			if sel.K8s.Namespace != nil {
				fmt.Fprintf(&rightPanel, "  Namespace: %s\n", *sel.K8s.Namespace)
			}
			if sel.K8s.Subnet != nil {
				fmt.Fprintf(&rightPanel, "  Subnet:    %s\n", *sel.K8s.Subnet)
			}
			if sel.K8s.Vpc != nil {
				fmt.Fprintf(&rightPanel, "  VPC:       %s\n", *sel.K8s.Vpc)
			}
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel.String(), m.styles.DetailBox.Render(rightPanel.String()))
}

func (m model) renderL2View() string {
	var leftPanel strings.Builder
	var rightPanel strings.Builder

	leftPanel.WriteString(m.styles.Header.Render("Subnets (Broadcast Domains)") + "\n")

	var switches []api.GraphNode
	for _, n := range m.nodeList {
		if n.Type == api.GraphNodeTypeLS {
			alreadyIn := false
			for _, s := range switches {
				if s.Id == n.Id {
					alreadyIn = true
					break
				}
			}
			if !alreadyIn {
				switches = append(switches, n)
			}
		}
	}

	selectedSwitchIdx := 0
	for i, s := range switches {
		if s.Id == m.focusedLS || s.Name == m.focusedLSName {
			selectedSwitchIdx = i
			break
		}
	}

	for i, s := range switches {
		isSel := i == selectedSwitchIdx
		cursor := "  "
		if isSel {
			cursor = "► "
		}

		pCount := ""
		if s.Properties != nil {
			p := *s.Properties
			pCount = fmt.Sprintf(" [%v bound]", p["boundPortCount"])
		}

		line := fmt.Sprintf("%s%s%s", cursor, s.Name, pCount)
		if isSel {
			leftPanel.WriteString(m.styles.SelectedNode.Render(line) + "\n")
		} else {
			leftPanel.WriteString(m.styles.Node.Render(line) + "\n")
		}
	}

	leftBox := m.styles.VPCBox.Width(34).Render(leftPanel.String())

	// Right Pane: Workload inventory for focused switch
	swName := m.focusedLSName
	if swName == "" {
		swName = m.focusedLS
	}

	rightPanel.WriteString(m.styles.Header.Render(fmt.Sprintf("Workloads in %s (%d ports)", swName, len(m.l2Nodes))) + "\n\n")

	if len(m.l2Nodes) == 0 {
		rightPanel.WriteString(m.styles.MutedText.Render("  No active workloads or ports in this subnet.\n"))
		rightPanel.WriteString(m.styles.MutedText.Render("  Tip: Press 's' to cycle focused subnet or 'r' to refresh.\n"))
	} else {
		for _, n := range m.l2Nodes {
			kind := "Pod"
			name := n.Name
			ns := "default"
			owner := ""
			vmi := ""
			if n.K8s != nil {
				if n.K8s.WorkloadKind != nil {
					kind = *n.K8s.WorkloadKind
				}
				if n.K8s.WorkloadName != nil {
					name = *n.K8s.WorkloadName
				}
				if n.K8s.Namespace != nil {
					ns = *n.K8s.Namespace
				}
				if n.K8s.Owner != nil {
					owner = *n.K8s.Owner
				}
				if n.K8s.Vmi != nil {
					vmi = *n.K8s.Vmi
				}
			}

			props := make(map[string]interface{})
			if n.Properties != nil {
				props = *n.Properties
			}
			addrStr := "(none)"
			if addrs, ok := props["addresses"].([]interface{}); ok && len(addrs) > 0 {
				var strList []string
				for _, a := range addrs {
					if s, ok := a.(string); ok {
						strList = append(strList, s)
					}
				}
				addrStr = strings.Join(strList, ", ")
			}

			chassis := "(unbound)"
			if bound, ok := props["boundChassis"].(string); ok && bound != "" {
				chassis = bound
				if n.K8s != nil && n.K8s.Node != nil {
					chassis = *n.K8s.Node
				}
			}

			var card strings.Builder
			cardTitle := fmt.Sprintf("[%s] %s/%s", kind, ns, name)
			card.WriteString(m.styles.PortBadge.Render(cardTitle) + "\n")
			fmt.Fprintf(&card, "  • Port Name:   %s\n", n.Name)
			fmt.Fprintf(&card, "  • IP / MAC:    %s\n", m.styles.CIDR.Render(addrStr))
			fmt.Fprintf(&card, "  • Placement:   Node %s\n", m.styles.BadgeBound.Render(chassis))
			if owner != "" {
				fmt.Fprintf(&card, "  • Owner:       %s\n", owner)
			}
			if vmi != "" && vmi != name {
				fmt.Fprintf(&card, "  • KubeVirt VMI: %s\n", vmi)
			}
			rightPanel.WriteString(m.styles.DetailBox.Width(54).Render(card.String()) + "\n")
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, rightPanel.String())
}

func (m model) renderChassisView() string {
	var leftPanel strings.Builder
	var rightPanel strings.Builder

	leftPanel.WriteString(m.styles.Header.Render("Cluster Nodes") + "\n")

	for i, ch := range m.chassisNodes {
		isSel := i == m.selectedIdx
		cursor := "  "
		if isSel {
			cursor = "► "
		}

		props := make(map[string]interface{})
		if ch.Properties != nil {
			props = *ch.Properties
		}
		hostname, _ := props["hostname"].(string)
		if hostname == "" {
			hostname = ch.Name
		}
		nodeName, _ := props["nodeName"].(string)
		if nodeName == "" {
			nodeName = hostname
		}
		bCount := props["bindingCount"]

		line := fmt.Sprintf("%s%s (%v ports)", cursor, nodeName, bCount)
		if isSel {
			leftPanel.WriteString(m.styles.SelectedNode.Render(line) + "\n")
		} else {
			leftPanel.WriteString(m.styles.Node.Render(line) + "\n")
		}
	}

	leftBox := m.styles.VPCBox.Width(32).Render(leftPanel.String())

	// Right Pane: Details for highlighted chassis
	if len(m.chassisNodes) > 0 && m.selectedIdx < len(m.chassisNodes) {
		ch := m.chassisNodes[m.selectedIdx]
		props := make(map[string]interface{})
		if ch.Properties != nil {
			props = *ch.Properties
		}
		hostname, _ := props["hostname"].(string)
		if hostname == "" {
			hostname = ch.Name
		}
		nodeName, _ := props["nodeName"].(string)
		if nodeName == "" {
			nodeName = hostname
		}
		ip, _ := props["ip"].(string)
		encap, _ := props["encapType"].(string)

		rightPanel.WriteString(m.styles.Header.Render(fmt.Sprintf("Node Details: %s", nodeName)) + "\n")
		fmt.Fprintf(&rightPanel, "  • Hostname:     %s\n", hostname)
		fmt.Fprintf(&rightPanel, "  • Chassis UUID: %s\n", ch.Name)
		fmt.Fprintf(&rightPanel, "  • Encap IP:     %s (type: %s)\n", m.styles.CIDR.Render(ip), encap)
		fmt.Fprintf(&rightPanel, "  • Bound Ports:  %v\n\n", props["bindingCount"])

		// Active Gateways hosted on this node
		var gateways []string
		for _, conn := range m.portList {
			if conn.ActiveGateway == hostname || conn.ActiveGateway == nodeName || conn.ActiveGateway == ch.Name {
				gateways = append(gateways, fmt.Sprintf("%s (VPC: %s, EIP: %s)", conn.PortName, conn.VPCName, strings.Join(conn.Networks, ", ")))
			}
		}

		rightPanel.WriteString(m.styles.Header.Render("Active HA Gateways:") + "\n")
		if len(gateways) == 0 {
			rightPanel.WriteString(m.styles.MutedText.Render("  (no active gateway roles on this node)\n\n"))
		} else {
			for _, gw := range gateways {
				rightPanel.WriteString("  • " + m.styles.ActiveGateway.Render(gw) + "\n")
			}
			rightPanel.WriteString("\n")
		}

		// Hosted Workloads
		rightPanel.WriteString(m.styles.Header.Render("Workloads Running on this Node:") + "\n")
		var hostedCount int
		for _, lsps := range m.switchWorkloads {
			for _, lsp := range lsps {
				if lsp.K8s != nil && lsp.K8s.Node != nil && (*lsp.K8s.Node == nodeName || *lsp.K8s.Node == hostname) {
					hostedCount++
					wBadge := formatWorkloadBadge(lsp, m.styles)
					rightPanel.WriteString("  • " + wBadge + "\n")
				}
			}
		}
		if hostedCount == 0 {
			rightPanel.WriteString(m.styles.MutedText.Render("  (no attached workloads discovered on this node)\n"))
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, m.styles.DetailBox.Width(58).Render(rightPanel.String()))
}

func (m model) renderMatrixView() string {
	var b strings.Builder

	header := fmt.Sprintf("  %-16s %-30s %-24s %-20s %-19s %s",
		"VPC/ROUTER", "PORT (LRP)", "INTERFACE IP (CIDR)", "TARGET SWITCH", "MAC", "GATEWAY STATUS")
	b.WriteString(m.styles.TableHeader.Render(header) + "\n")

	if len(m.portList) == 0 {
		b.WriteString("\n  No router ports found.\n")
		return b.String()
	}

	for idx, conn := range m.portList {
		routerLabel := conn.RouterName
		cidr := strings.Join(conn.Networks, ", ")
		switchLabel := conn.SwitchName
		if conn.IsShared {
			switchLabel += " *"
		}
		gw := "Distributed"
		if conn.ActiveGateway != "" {
			gw = fmt.Sprintf("Active: %s", conn.ActiveGateway)
		}

		cursor := "  "
		if idx == m.selectedIdx {
			cursor = "► "
		}

		row := fmt.Sprintf("%s%-16s %-30s %-24s %-20s %-19s %s",
			cursor,
			truncate(routerLabel, 16),
			truncate(conn.PortName, 30),
			truncate(cidr, 24),
			truncate(switchLabel, 20),
			truncate(conn.MAC, 19),
			gw)

		if idx == m.selectedIdx {
			b.WriteString(m.styles.TableRowSelected.Render(row) + "\n")
		} else {
			b.WriteString(m.styles.TableRow.Render(row) + "\n")
		}
	}

	b.WriteString("\n" + m.styles.MutedText.Render(
		"  * Marked switches are shared interconnects (connected to multiple Logical Routers)."))

	// Render Port Inspector Box below the matrix for the currently selected LRP
	if len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
		selectedConn := m.portList[m.selectedIdx]
		b.WriteString("\n" + m.renderPortInspector(selectedConn))
	}

	return b.String()
}

func (m model) renderPortInspector(conn RouterPortConnection) string {
	var box strings.Builder

	box.WriteString(m.styles.Header.Render("Port Interconnection Inspector: "+conn.PortName) + "\n")

	sharedBadge := ""
	if conn.IsShared {
		sharedBadge = " (Shared Multi-VPC Interconnect)"
	}
	peerLSP := conn.PeerPortName
	if peerLSP == "" {
		peerLSP = "(none / internal)"
	}

	rawCidr := strings.Join(conn.Networks, ", ")
	ipAddr := rawCidr
	prefix := ""
	if strings.Contains(rawCidr, "/") {
		parts := strings.SplitN(rawCidr, "/", 2)
		ipAddr = parts[0]
		prefix = "/" + parts[1]
	}

	fmt.Fprintf(&box, "  Router:       %-34s Target Switch: %s%s\n",
		fmt.Sprintf("%s (VPC: %s)", conn.RouterName, conn.VPCName),
		conn.SwitchName,
		sharedBadge)
	fmt.Fprintf(&box, "  LRP Port:     %-34s Peer LSP:      %s\n",
		m.styles.PortBadge.Render(conn.PortName),
		peerLSP)
	fmt.Fprintf(&box, "  MAC Address:  %-34s Subnet Prefix: %s\n",
		conn.MAC,
		prefix)

	if conn.ActiveGateway != "" {
		fmt.Fprintf(&box, "  Interface IP: %-34s Gateway Role:  %s\n",
			m.styles.CIDR.Render(rawCidr),
			m.styles.ActiveGateway.Render("External EIP Gateway"))
	} else if conn.IsShared {
		fmt.Fprintf(&box, "  Interface IP: %-34s Gateway Role:  %s\n",
			m.styles.CIDR.Render(rawCidr),
			"Multi-VPC Transit IP")
	} else {
		fmt.Fprintf(&box, "  Interface IP: %-34s Gateway Role:  %s\n",
			m.styles.CIDR.Render(rawCidr),
			m.styles.BadgeBound.Render(fmt.Sprintf("Default Gateway for %s", conn.SwitchName)))
	}

	// If this port attaches to a shared interconnect switch, show all other interconnected routers!
	if conn.IsShared {
		var otherRouters []RouterPortConnection
		for _, peerConn := range m.switchPorts[conn.SwitchID] {
			if peerConn.RouterID != conn.RouterID {
				otherRouters = append(otherRouters, peerConn)
			}
		}

		if len(otherRouters) > 0 {
			fmt.Fprintf(&box, "\n  %s\n", m.styles.Header.Render("Interconnected VPC Routers on '"+conn.SwitchName+"':"))
			for _, peer := range otherRouters {
				fmt.Fprintf(&box, "    • %-16s ── %-18s (via %s)\n",
					fmt.Sprintf("%s (%s)", peer.RouterName, peer.VPCName),
					m.styles.CIDR.Render(strings.Join(peer.Networks, ", ")),
					m.styles.PortBadge.Render(peer.PortName))
			}
		}
	}

	// Gateway & High Availability details
	fmt.Fprintf(&box, "\n  %s\n", m.styles.Header.Render("Gateway & Routing Details:"))
	if conn.ActiveGateway != "" {
		fmt.Fprintf(&box, "    • Routing Mode:   Centralized HA Gateway\n")
		fmt.Fprintf(&box, "    • Active Gateway: %s\n", m.styles.ActiveGateway.Render(conn.ActiveGateway+" (Active node)"))
		fmt.Fprintf(&box, "    • Redirect Port:  cr-%s\n", conn.PortName)
		fmt.Fprintf(&box, "    • Egress Subnet:  %s\n", conn.SwitchName)
	} else if conn.IsShared {
		fmt.Fprintf(&box, "    • Routing Mode:   Distributed Transit Interconnect\n")
		fmt.Fprintf(&box, "    • Subnet Switch:  %s (shared by multiple VPC routers)\n", conn.SwitchName)
	} else {
		fmt.Fprintf(&box, "    • Routing Mode:   Distributed Subnet Gateway (Local Chassis Egress)\n")
		fmt.Fprintf(&box, "    • Gateway Target: Workloads inside '%s' use %s as their default gateway\n",
			conn.SwitchName, ipAddr)
	}

	// Architectural topology role explanation
	fmt.Fprintf(&box, "\n  %s\n", m.styles.Header.Render("Topology Role:"))
	switch {
	case conn.ActiveGateway != "":
		fmt.Fprintf(&box, "    North-South Gateway: EIP interface providing centralized egress to external network '%s'\n",
			conn.SwitchName)
	case conn.IsShared:
		fmt.Fprintf(&box, "    East-West Interconnect: links router '%s' to '%s' for cross-VPC communication\n",
			conn.RouterName, conn.SwitchName)
	default:
		fmt.Fprintf(&box, "    Internal Subnet Gateway: default gateway interface (%s) for pods and VMs in '%s'\n",
			ipAddr, conn.SwitchName)
	}

	return m.styles.InspectorBox.Render(box.String())
}

func formatNodeLine(n api.GraphNode, styles ThemeStyles) string {
	switch n.Type {
	case api.GraphNodeTypeLR:
		counts := ""
		if n.Properties != nil {
			p := *n.Properties
			counts = fmt.Sprintf(" (routes:%v, nat:%v, policies:%v, lb:%v)", p["routeCount"], p["natCount"], p["policyCount"], p["lbCount"])
		}
		return fmt.Sprintf("▼ [Router] %s%s", n.Name, counts)
	case api.GraphNodeTypeLS:
		portInfo := ""
		if n.Properties != nil {
			p := *n.Properties
			portInfo = fmt.Sprintf(" [%s bound, %s unbound]",
				styles.BadgeBound.Render(fmt.Sprintf("%v", p["boundPortCount"])),
				styles.BadgeUnbound.Render(fmt.Sprintf("%v", p["unboundPortCount"])))
		}
		return fmt.Sprintf("[Switch] %s%s", n.Name, portInfo)
	default:
		return fmt.Sprintf("[%s] %s", n.Type, n.Name)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		if maxLen > 3 {
			return s[:maxLen-3] + "..."
		}
		return s[:maxLen]
	}
	return s
}

func formatWorkloadBadge(n api.GraphNode, styles ThemeStyles) string {
	kind := "Pod"
	name := n.Name
	ns := ""
	if n.K8s != nil {
		if n.K8s.WorkloadKind != nil && *n.K8s.WorkloadKind != "" {
			kind = *n.K8s.WorkloadKind
		}
		if n.K8s.WorkloadName != nil && *n.K8s.WorkloadName != "" {
			name = *n.K8s.WorkloadName
		}
		if n.K8s.Namespace != nil && *n.K8s.Namespace != "" {
			ns = *n.K8s.Namespace + "/"
		}
	}

	badge := fmt.Sprintf("[%s] %s%s", kind, ns, name)
	switch kind {
	case "VirtualMachine":
		badge = styles.ActiveGateway.Render(badge)
	case "Node":
		badge = styles.PortBadge.Render(badge)
	default:
		badge = styles.BadgeBound.Render(badge)
	}

	ipStr := ""
	if n.Properties != nil {
		p := *n.Properties
		if addrs, ok := p["addresses"].([]interface{}); ok && len(addrs) > 0 {
			if s, ok := addrs[0].(string); ok {
				ipStr = " (IP: " + s + ")"
			}
		}
		if boundChassis, ok := p["boundChassis"].(string); ok && boundChassis != "" {
			nodeName := boundChassis
			if n.K8s != nil && n.K8s.Node != nil {
				nodeName = *n.K8s.Node
			}
			ipStr += " on " + nodeName
		}
	}

	return badge + styles.MutedText.Render(ipStr)
}

func runMachineOutput(daemonURL, fixturePath, viewName, focusID, format string) error {
	var resp *api.GraphResponse
	var err error

	if fixturePath != "" {
		snap := graph.LoadTestSnapshot(nil)
		resp, err = snap.BuildGraph(viewName, focusID, 1, 1, 100)
	} else {
		url := fmt.Sprintf("%s/graph?view=%s", strings.TrimRight(daemonURL, "/"), viewName)
		if focusID != "" {
			url += fmt.Sprintf("&focus=%s", focusID)
		}
		client := &http.Client{Timeout: 5 * time.Second}
		r, reqErr := client.Get(url)
		if reqErr != nil {
			return fmt.Errorf("failed to reach daemon: %w", reqErr)
		}
		defer func() { _ = r.Body.Close() }()
		if r.StatusCode != http.StatusOK {
			return fmt.Errorf("daemon returned HTTP %d", r.StatusCode)
		}
		var decoded api.GraphResponse
		if decodeErr := json.NewDecoder(r.Body).Decode(&decoded); decodeErr != nil {
			return decodeErr
		}
		resp = &decoded
	}

	if err != nil {
		return err
	}

	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	case "yaml", "yml":
		yBytes, err := yaml.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(yBytes)
		return err
	default:
		return fmt.Errorf("unsupported output format: %s (supported: json, yaml)", format)
	}
}

func main() {
	var daemonURL string
	var useFixture bool
	var themeName string
	var diagMode bool
	var kubeconfig string
	var k8sNamespace string
	var outputFormat string
	var viewName string
	var focusID string

	flag.StringVar(&daemonURL, "daemon-url", "http://127.0.0.1:8080/api/v1", "URL of the ovn-topologyd API")
	flag.BoolVar(&useFixture, "fixture", false, "Use bundled dev fixture dataset offline")
	flag.StringVar(&themeName, "theme", getEnvDefault("THEME", "default"), "Color theme: default, suse, dracula, nord, tokyonight, catppuccin")
	flag.BoolVar(&diagMode, "diag", false, "Enable Tier 1 exec-based diagnostics (ADR-0001)")
	flag.StringVar(&kubeconfig, "kubeconfig", getEnvDefault("KUBECONFIG", ""), "Path to kubeconfig (defaults to standard kubeconfig paths)")
	flag.StringVar(&k8sNamespace, "k8s-namespace", "kube-system", "Kubernetes namespace containing Kube-OVN/OVS daemon pods")
	flag.StringVar(&outputFormat, "o", "", "Output format: json or yaml (scriptable machine output mode)")
	flag.StringVar(&viewName, "view", "l3", "View for machine output: l3, l2, chassis")
	flag.StringVar(&focusID, "focus", "", "Focus target for view (e.g. ls:<name|uuid>)")

	// Allow "ovn-topo graph ..." style invocations
	if len(os.Args) > 1 && os.Args[1] == "graph" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	flag.Parse()

	fixturePath := ""
	if useFixture {
		fixturePath = "dev-fixture"
	}

	if outputFormat != "" {
		if err := runMachineOutput(daemonURL, fixturePath, viewName, focusID, outputFormat); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	p := tea.NewProgram(initialModel(daemonURL, fixturePath, themeName, diagMode, kubeconfig, k8sNamespace), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func getEnvDefault(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}
