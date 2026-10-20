package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
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
)

type viewMode int

const (
	viewTree viewMode = iota
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

	// Tier 1 Diagnostics (ADR-0001)
	diagMode       bool
	diagAvailable  bool
	diagReason     string
	k8sNamespace   string
	k8sClient      kubernetes.Interface
	diagExecutor   diag.Executor
	diagLocator    *diag.PodLocator
	diagCentralPod diag.PodRef
	diagModal      bool
	diagRunning    bool
	diagAction     string
	diagTitle      string
	diagTraceRes   *diag.TraceResult
	diagRawOutput  string
	diagRawView    bool
	diagError      error
}

type graphLoadedMsg struct {
	resp *api.GraphResponse
	err  error
}

type diagProbeMsg struct {
	available  bool
	reason     string
	centralPod diag.PodRef
}

type diagExecResultMsg struct {
	action    string
	title     string
	traceRes  *diag.TraceResult
	rawOutput string
	err       error
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
		k8sNamespace: k8sNamespace,
		nodeMap:      make(map[string]api.GraphNode),
		routerPorts:  make(map[string][]RouterPortConnection),
		switchPorts:  make(map[string][]RouterPortConnection),
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
		m.diagRawOutput = msg.rawOutput
		m.diagError = msg.err
		return m, nil

	case tea.KeyMsg:
		if m.diagModal {
			switch msg.String() {
			case "esc", "x", "ctrl+c":
				m.diagModal = false
				m.diagRawOutput = ""
				m.diagTraceRes = nil
				m.diagError = nil
				m.diagRunning = false
				return m, nil
			case "o":
				m.diagRawView = !m.diagRawView
				return m, nil
			case "1":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagError = nil
				var conn *RouterPortConnection
				if m.mode == viewMatrix && len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
					conn = &m.portList[m.selectedIdx]
				} else if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
					node := m.nodeList[m.selectedIdx]
					if ports, ok := m.routerPorts[node.Id]; ok && len(ports) > 0 {
						conn = &ports[0]
					} else if ports, ok := m.switchPorts[node.Id]; ok && len(ports) > 0 {
						conn = &ports[0]
					}
				}
				return m, m.executeTrace(conn)
			case "2":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagError = nil
				nodeName := m.selectedNodeName()
				return m, m.executeNodeDiag("conntrack", nodeName)
			case "3":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagError = nil
				nodeName := m.selectedNodeName()
				return m, m.executeNodeDiag("ovs-iface", nodeName)
			case "4":
				if m.diagRunning {
					return m, nil
				}
				m.diagRunning = true
				m.diagError = nil
				nodeName := m.selectedNodeName()
				return m, m.executeNodeDiag("logs", nodeName)
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
			m.diagRawOutput = ""
			m.diagTraceRes = nil
			m.diagError = nil
			m.diagRunning = false
			m.diagRawView = false
			return m, nil
		case "t":
			m.themeIndex = (m.themeIndex + 1) % len(AvailableThemes)
			m.theme = AvailableThemes[m.themeIndex]
			m.styles = CompileStyles(m.theme)
			m.status = fmt.Sprintf("Theme: %s", m.theme.Name)
			return m, nil
		case "tab":
			if m.mode == viewTree {
				m.mode = viewMatrix
				m.selectedIdx = 0
			} else {
				m.mode = viewTree
				m.selectedIdx = 0
			}
			return m, nil
		case "r":
			m.status = "Refreshing..."
			return m, m.fetchGraphCmd()
		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}
		case "down", "j":
			maxIdx := len(m.nodeList) - 1
			if m.mode == viewMatrix {
				maxIdx = len(m.portList) - 1
			}
			if m.selectedIdx < maxIdx {
				m.selectedIdx++
			}
		}
	}

	return m, nil
}

func (m model) selectedNodeName() string {
	if m.mode == viewMatrix && len(m.portList) > 0 && m.selectedIdx < len(m.portList) {
		conn := m.portList[m.selectedIdx]
		if conn.ActiveGateway != "" {
			return conn.ActiveGateway
		}
	}
	for _, conn := range m.portList {
		if conn.ActiveGateway != "" {
			return conn.ActiveGateway
		}
	}
	return ""
}

func (m model) executeTrace(conn *RouterPortConnection) tea.Cmd {
	return func() tea.Msg {
		if m.diagExecutor == nil || m.diagLocator == nil {
			return diagExecResultMsg{err: fmt.Errorf("diagnostics executor not initialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		centralPod := m.diagCentralPod
		if centralPod.Name == "" {
			pod, err := m.diagLocator.FindOvnCentralPod(ctx)
			if err != nil {
				return diagExecResultMsg{err: err}
			}
			centralPod = pod
		}

		datapath := "default-subnet"
		inport := "test"
		srcIP := "10.244.0.1"
		dstIP := "10.244.0.2"
		srcMAC := ""

		if conn != nil {
			datapath = conn.SwitchName
			inport = conn.PortName
			srcMAC = conn.MAC
			if len(conn.Networks) > 0 {
				srcIP = strings.Split(conn.Networks[0], "/")[0]
			}
			for _, peer := range m.switchPorts[conn.SwitchID] {
				if peer.PortName != conn.PortName && len(peer.Networks) > 0 {
					dstIP = strings.Split(peer.Networks[0], "/")[0]
					break
				}
			}
		}

		req := diag.TraceRequest{
			Datapath: datapath,
			Inport:   inport,
			SrcMAC:   srcMAC,
			SrcIP:    srcIP,
			DstIP:    dstIP,
			Protocol: "icmp",
		}

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
		return diagExecResultMsg{
			action:    "trace",
			title:     fmt.Sprintf("ovn-trace (%s -> %s via %s)", srcIP, dstIP, inport),
			traceRes:  &traceRes,
			rawOutput: string(stdout),
		}
	}
}

func (m model) executeNodeDiag(action string, nodeName string) tea.Cmd {
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
			title = fmt.Sprintf("Conntrack Table on node %s", nodeName)
		case "ovs-iface":
			cmd = diag.BuildOvsVsctlListInterfaceCommand()
			title = fmt.Sprintf("OVS Interfaces on node %s", nodeName)
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

		return diagExecResultMsg{
			action:    action,
			title:     title,
			rawOutput: string(stdout),
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

	treeTab := m.styles.TabInactive.Render("1. Topology Tree")
	matrixTab := m.styles.TabInactive.Render("2. Port Interconnection Matrix")
	if m.mode == viewTree {
		treeTab = m.styles.TabActive.Render("1. Topology Tree")
	} else {
		matrixTab = m.styles.TabActive.Render("2. Port Interconnection Matrix")
	}

	tabs := fmt.Sprintf("%s%s", treeTab, matrixTab)
	diagShortcut := ""
	if m.diagAvailable {
		diagShortcut = " | x: diag"
	}

	statusText := m.styles.MutedText.Render(
		fmt.Sprintf(" %s [%s]  [Tab: view | t: theme%s | j/k: nav | r: refresh | q: quit]", m.status, m.theme.Name, diagShortcut))

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

	if m.mode == viewMatrix {
		b.WriteString(m.renderMatrixView())
		return b.String()
	}

	// Tree View
	b.WriteString(m.renderTreeView())
	return b.String()
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
		targetDesc = fmt.Sprintf("Node: %s (Type: %s)", node.Name, node.Type)
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

	if m.diagRawOutput != "" || m.diagTraceRes != nil {
		box.WriteString(m.styles.Header.Render(fmt.Sprintf("  Result: %s", m.diagTitle)) + "\n\n")

		if m.diagAction == "trace" && m.diagTraceRes != nil && !m.diagRawView {
			tr := m.diagTraceRes
			verdictStyle := m.styles.BadgeBound
			if tr.Verdict != "delivered" {
				verdictStyle = m.styles.BadgeUnbound
			}
			fmt.Fprintf(&box, "  Verdict: %s\n", verdictStyle.Render(strings.ToUpper(tr.Verdict)))
			fmt.Fprintf(&box, "  Summary: %s\n\n", tr.Summary)

			box.WriteString(m.styles.Header.Render("  Pipeline Stages Evaluated:") + "\n")
			for i, st := range tr.Stages {
				if i >= 12 {
					fmt.Fprintf(&box, "    ... (%d more stages, press 'o' for raw)\n", len(tr.Stages)-12)
					break
				}
				fmt.Fprintf(&box, "    • [%s table %d] %-22s ➔ %s\n",
					st.Pipeline, st.Table, st.Name, truncate(st.Action, 45))
			}
		} else {
			lines := strings.Split(strings.TrimSpace(m.diagRawOutput), "\n")
			maxLines := 15
			if len(lines) > maxLines {
				lines = lines[:maxLines]
				lines = append(lines, fmt.Sprintf("... (%d lines truncated, press 'o' to toggle)", len(strings.Split(m.diagRawOutput, "\n"))-maxLines))
			}
			box.WriteString("  " + strings.Join(lines, "\n  ") + "\n")
		}

		box.WriteString("\n" + m.styles.MutedText.Render("  [o: toggle raw output | 1-4: run another test | Esc/x: return to topology]"))
		return m.styles.ModalBox.Render(box.String())
	}

	box.WriteString(m.styles.Header.Render("  Choose a Diagnostic Action:") + "\n")
	box.WriteString("    [1] ovn-trace (Microflow trace from this port to peer switch/gateway)\n")
	box.WriteString("    [2] Dump Conntrack Entries (ovs-appctl dpctl/dump-conntrack)\n")
	box.WriteString("    [3] Physical OVS Interfaces (ovs-vsctl list Interface on node)\n")
	box.WriteString("    [4] Tail Controller Logs (ovn-controller log tail on node)\n\n")

	box.WriteString(m.styles.MutedText.Render("  Press 1, 2, 3, 4 to execute | Esc or x to cancel"))

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

			cursor := "  "
			if isSel {
				cursor = "► "
				boxContent.WriteString(m.styles.SelectedNode.Render(cursor+line) + "\n")
			} else {
				boxContent.WriteString(m.styles.Node.Render(cursor+line) + "\n")
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
			if isSel {
				cursor = "► "
				sharedContent.WriteString(m.styles.SelectedNode.Render(cursor+line) + "\n")
			} else {
				sharedContent.WriteString(m.styles.Node.Render(cursor+line) + "\n")
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

func main() {
	var daemonURL string
	var useFixture bool
	var themeName string
	var diagMode bool
	var kubeconfig string
	var k8sNamespace string

	flag.StringVar(&daemonURL, "daemon-url", "http://127.0.0.1:8080/api/v1", "URL of the ovn-topologyd API")
	flag.BoolVar(&useFixture, "fixture", false, "Use bundled dev fixture dataset offline")
	flag.StringVar(&themeName, "theme", getEnvDefault("THEME", "default"), "Color theme: default, suse, dracula, nord, tokyonight, catppuccin")
	flag.BoolVar(&diagMode, "diag", false, "Enable Tier 1 exec-based diagnostics (ADR-0001)")
	flag.StringVar(&kubeconfig, "kubeconfig", getEnvDefault("KUBECONFIG", ""), "Path to kubeconfig (defaults to standard kubeconfig paths)")
	flag.StringVar(&k8sNamespace, "k8s-namespace", "kube-system", "Kubernetes namespace containing Kube-OVN/OVS daemon pods")
	flag.Parse()

	fixturePath := ""
	if useFixture {
		fixturePath = "dev-fixture"
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
