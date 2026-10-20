package main

import (
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
	"github.com/coulof/ovn-topology/pkg/graph"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#0070F3")).
			Padding(0, 1)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1).
			MarginRight(1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888888")).
				Background(lipgloss.Color("#222222")).
				Padding(0, 1).
				MarginRight(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00A86B")).
			MarginBottom(1)

	vpcBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).
			Padding(0, 1).
			Margin(0, 1, 1, 0)

	nodeStyle = lipgloss.NewStyle().
			Padding(0, 1)

	selectedNodeStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#7D56F4")).
				Padding(0, 1)

	edgeLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	cidrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00C7BE")).
			Bold(true)

	portBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF9500"))

	detailBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#555555")).
			Padding(0, 1).
			Width(52)

	badgeBound = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00A86B"))

	badgeUnbound = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF453A"))

	activeGatewayStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fe7c3f")).
				Bold(true)

	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#333333")).
				Padding(0, 1)

	tableRowStyle = lipgloss.NewStyle().
			Padding(0, 1)

	tableRowSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#7D56F4")).
				Padding(0, 1)

	inspectorBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#7D56F4")).
				Padding(0, 1).
				MarginTop(1)
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
}

type graphLoadedMsg struct {
	resp *api.GraphResponse
	err  error
}

func initialModel(daemonURL, fixturePath string) model {
	return model{
		daemonURL:   daemonURL,
		fixturePath: fixturePath,
		status:      "Connecting...",
		mode:        viewTree,
		nodeMap:     make(map[string]api.GraphNode),
		routerPorts: make(map[string][]RouterPortConnection),
		switchPorts: make(map[string][]RouterPortConnection),
	}
}

func (m model) Init() tea.Cmd {
	return m.fetchGraphCmd()
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

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
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

func (m model) View() string {
	var b strings.Builder

	// Top Title and View Tabs
	header := titleStyle.Render(" OVN Network Topology ")

	treeTab := tabInactiveStyle.Render("1. Topology Tree")
	matrixTab := tabInactiveStyle.Render("2. Port Interconnection Matrix")
	if m.mode == viewTree {
		treeTab = tabActiveStyle.Render("1. Topology Tree")
	} else {
		matrixTab = tabActiveStyle.Render("2. Port Interconnection Matrix")
	}

	tabs := fmt.Sprintf("%s%s", treeTab, matrixTab)
	statusText := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(
		fmt.Sprintf(" %s  [Tab: switch view | j/k: navigate | r: refresh | q: quit]", m.status))

	b.WriteString(header + " " + tabs + statusText + "\n\n")

	if m.err != nil {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FF453A")).Render(fmt.Sprintf("Error: %v\n", m.err)))
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
		boxContent.WriteString(headerStyle.Render(fmt.Sprintf("VPC: %s", vpcName)) + "\n")

		for _, n := range nodes {
			isSel := selectedNode != nil && selectedNode.Id == n.Id
			line := formatNodeLine(n)

			cursor := "  "
			if isSel {
				cursor = "► "
				boxContent.WriteString(selectedNodeStyle.Render(cursor+line) + "\n")
			} else {
				boxContent.WriteString(nodeStyle.Render(cursor+line) + "\n")
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
						gwInfo = activeGatewayStyle.Render(fmt.Sprintf(" [GW: %s]", conn.ActiveGateway))
					}

					var ipLabel string
					if conn.ActiveGateway != "" {
						ipLabel = activeGatewayStyle.Render("[EIP: " + cidr + "]")
					} else if !conn.IsShared {
						ipLabel = cidrStyle.Render("[GW IP: " + cidr + "]")
					} else {
						ipLabel = cidrStyle.Render("[Transit IP: " + cidr + "]")
					}

					branch := fmt.Sprintf("      %s (%s) %s ──► %s%s",
						edgeLineStyle.Render(glyph),
						portBadgeStyle.Render(conn.PortName),
						ipLabel,
						peerInfo,
						gwInfo)
					boxContent.WriteString(branch + "\n")
				}
			}
		}
		leftPanel.WriteString(vpcBoxStyle.Render(boxContent.String()) + "\n")
	}

	// Render Shared / Interconnect switches (e.g. join)
	if len(m.sharedNodes) > 0 {
		var sharedContent strings.Builder
		sharedContent.WriteString(headerStyle.Render("Interconnects (Multi-LR Subnets)") + "\n")
		for _, n := range m.sharedNodes {
			isSel := selectedNode != nil && selectedNode.Id == n.Id
			line := formatNodeLine(n)
			cursor := "  "
			if isSel {
				cursor = "► "
				sharedContent.WriteString(selectedNodeStyle.Render(cursor+line) + "\n")
			} else {
				sharedContent.WriteString(nodeStyle.Render(cursor+line) + "\n")
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
					edgeLineStyle.Render(glyph),
					portBadgeStyle.Render(conn.PortName),
					cidrStyle.Render(cidr),
					conn.RouterName,
					conn.VPCName)
				sharedContent.WriteString(branch + "\n")
			}
		}
		leftPanel.WriteString(vpcBoxStyle.Render(sharedContent.String()) + "\n")
	}

	// Render External nodes
	if len(m.externalNodes) > 0 {
		var extContent strings.Builder
		extContent.WriteString(headerStyle.Render("External Provider Underlays") + "\n")
		for _, n := range m.externalNodes {
			isSel := selectedNode != nil && selectedNode.Id == n.Id
			cursor := "  "
			if isSel {
				cursor = "► "
				extContent.WriteString(selectedNodeStyle.Render(fmt.Sprintf("%s[EXT] %s", cursor, n.Name)) + "\n")
			} else {
				extContent.WriteString(nodeStyle.Render(fmt.Sprintf("%s[EXT] %s", cursor, n.Name)) + "\n")
			}
		}
		leftPanel.WriteString(vpcBoxStyle.Render(extContent.String()) + "\n")
	}

	// Right panel: detailed breakdown of selected entity
	var rightPanel strings.Builder
	if selectedNode != nil {
		sel := *selectedNode
		rightPanel.WriteString(headerStyle.Render("Selected: "+sel.Name) + "\n")
		fmt.Fprintf(&rightPanel, "Type: %s\n", sel.Type)
		fmt.Fprintf(&rightPanel, "ID:   %s\n", sel.Id)
		if sel.Parent != nil {
			fmt.Fprintf(&rightPanel, "VPC:  %s\n", strings.TrimPrefix(*sel.Parent, graph.PrefixVPC))
		}

		switch sel.Type {
		case api.GraphNodeTypeLR:
			// Router port interconnection list
			ports := m.routerPorts[sel.Id]
			fmt.Fprintf(&rightPanel, "\n%s (%d):\n", headerStyle.Render("Ports & Interconnections"), len(ports))
			if len(ports) == 0 {
				rightPanel.WriteString("  (no connected ports)\n")
			}
			for _, conn := range ports {
				fmt.Fprintf(&rightPanel, "• %s:\n", portBadgeStyle.Render(conn.PortName))
				fmt.Fprintf(&rightPanel, "    CIDR:   %s\n", cidrStyle.Render(strings.Join(conn.Networks, ", ")))
				if conn.MAC != "" {
					fmt.Fprintf(&rightPanel, "    MAC:    %s\n", conn.MAC)
				}
				peerText := fmt.Sprintf("[Switch] %s", conn.SwitchName)
				if conn.IsShared {
					peerText += " (shared interconnect)"
				}
				fmt.Fprintf(&rightPanel, "    Target: %s\n", peerText)
				if conn.ActiveGateway != "" {
					fmt.Fprintf(&rightPanel, "    Status: %s\n", activeGatewayStyle.Render("Active GW on "+conn.ActiveGateway))
				}
			}

			if sel.Properties != nil {
				p := *sel.Properties
				rightPanel.WriteString("\n" + headerStyle.Render("L3 Statistics:") + "\n")
				fmt.Fprintf(&rightPanel, "  Static Routes: %v\n", p["routeCount"])
				fmt.Fprintf(&rightPanel, "  NAT Rules:     %v\n", p["natCount"])
				fmt.Fprintf(&rightPanel, "  Policies:      %v\n", p["policyCount"])
				fmt.Fprintf(&rightPanel, "  LoadBalancers: %v\n", p["lbCount"])
			}
		case api.GraphNodeTypeLS:
			// Switch attachments list
			routers := m.switchPorts[sel.Id]
			fmt.Fprintf(&rightPanel, "\n%s (%d):\n", headerStyle.Render("Connected Routers"), len(routers))
			if len(routers) == 0 {
				rightPanel.WriteString("  (standalone switch)\n")
			}
			for _, conn := range routers {
				fmt.Fprintf(&rightPanel, "• %s (%s):\n", conn.RouterName, conn.VPCName)
				fmt.Fprintf(&rightPanel, "    Router Port: %s\n", portBadgeStyle.Render(conn.PortName))
				fmt.Fprintf(&rightPanel, "    Subnet/CIDR: %s\n", cidrStyle.Render(strings.Join(conn.Networks, ", ")))
			}

			if sel.Properties != nil {
				p := *sel.Properties
				rightPanel.WriteString("\n" + headerStyle.Render("L2 Port Summary:") + "\n")
				fmt.Fprintf(&rightPanel, "  Total Ports:   %v\n", p["portCount"])
				fmt.Fprintf(&rightPanel, "  Bound Ports:   %s\n", badgeBound.Render(fmt.Sprintf("%v", p["boundPortCount"])))
				fmt.Fprintf(&rightPanel, "  Unbound Ports: %s\n", badgeUnbound.Render(fmt.Sprintf("%v", p["unboundPortCount"])))
			}
		}

		if sel.K8s != nil {
			rightPanel.WriteString("\n" + headerStyle.Render("Kubernetes Metadata:") + "\n")
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

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel.String(), detailBoxStyle.Render(rightPanel.String()))
}

func (m model) renderMatrixView() string {
	var b strings.Builder

	header := fmt.Sprintf("  %-16s %-30s %-24s %-20s %-19s %s",
		"VPC/ROUTER", "PORT (LRP)", "INTERFACE IP (CIDR)", "TARGET SWITCH", "MAC", "GATEWAY STATUS")
	b.WriteString(tableHeaderStyle.Render(header) + "\n")

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
			b.WriteString(tableRowSelectedStyle.Render(row) + "\n")
		} else {
			b.WriteString(tableRowStyle.Render(row) + "\n")
		}
	}

	b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(
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

	box.WriteString(headerStyle.Render("Port Interconnection Inspector: "+conn.PortName) + "\n")

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
		portBadgeStyle.Render(conn.PortName),
		peerLSP)
	fmt.Fprintf(&box, "  MAC Address:  %-34s Subnet Prefix: %s\n",
		conn.MAC,
		prefix)

	if conn.ActiveGateway != "" {
		fmt.Fprintf(&box, "  Interface IP: %-34s Gateway Role:  %s\n",
			cidrStyle.Render(rawCidr),
			activeGatewayStyle.Render("External EIP Gateway"))
	} else if conn.IsShared {
		fmt.Fprintf(&box, "  Interface IP: %-34s Gateway Role:  %s\n",
			cidrStyle.Render(rawCidr),
			"Multi-VPC Transit IP")
	} else {
		fmt.Fprintf(&box, "  Interface IP: %-34s Gateway Role:  %s\n",
			cidrStyle.Render(rawCidr),
			badgeBound.Render(fmt.Sprintf("Default Gateway for %s", conn.SwitchName)))
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
			fmt.Fprintf(&box, "\n  %s\n", headerStyle.Render("Interconnected VPC Routers on '"+conn.SwitchName+"':"))
			for _, peer := range otherRouters {
				fmt.Fprintf(&box, "    • %-16s ── %-18s (via %s)\n",
					fmt.Sprintf("%s (%s)", peer.RouterName, peer.VPCName),
					cidrStyle.Render(strings.Join(peer.Networks, ", ")),
					portBadgeStyle.Render(peer.PortName))
			}
		}
	}

	// Gateway & High Availability details
	fmt.Fprintf(&box, "\n  %s\n", headerStyle.Render("Gateway & Routing Details:"))
	if conn.ActiveGateway != "" {
		fmt.Fprintf(&box, "    • Routing Mode:   Centralized HA Gateway\n")
		fmt.Fprintf(&box, "    • Active Gateway: %s\n", activeGatewayStyle.Render(conn.ActiveGateway+" (Active node)"))
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
	fmt.Fprintf(&box, "\n  %s\n", headerStyle.Render("Topology Role:"))
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

	return inspectorBoxStyle.Render(box.String())
}

func formatNodeLine(n api.GraphNode) string {
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
				badgeBound.Render(fmt.Sprintf("%v", p["boundPortCount"])),
				badgeUnbound.Render(fmt.Sprintf("%v", p["unboundPortCount"])))
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

	flag.StringVar(&daemonURL, "daemon-url", "http://127.0.0.1:8080/api/v1", "URL of the ovn-topologyd API")
	flag.BoolVar(&useFixture, "fixture", false, "Use bundled dev fixture dataset offline")
	flag.Parse()

	fixturePath := ""
	if useFixture {
		fixturePath = "dev-fixture"
	}

	p := tea.NewProgram(initialModel(daemonURL, fixturePath), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
