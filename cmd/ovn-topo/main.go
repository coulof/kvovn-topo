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

	detailBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#666666")).
			Padding(1).
			Width(45)

	badgeBound = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00A86B"))

	badgeUnbound = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF453A"))
)

type model struct {
	daemonURL    string
	fixturePath  string
	graphResp    *api.GraphResponse
	selectedIdx  int
	nodeList     []api.GraphNode
	err          error
	status       string
	width        int
	height       int
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
		m.err = nil
		m.graphResp = msg.resp
		m.status = fmt.Sprintf("Revision %d | %d nodes, %d edges", msg.resp.Revision, len(msg.resp.Nodes), len(msg.resp.Edges))

		// Build flat navigable list of non-container nodes
		m.nodeList = make([]api.GraphNode, 0)
		for _, n := range msg.resp.Nodes {
			if n.Type != api.GraphNodeTypeVPC {
				m.nodeList = append(m.nodeList, n)
			}
		}
		sort.Slice(m.nodeList, func(i, j int) bool {
			return m.nodeList[i].Name < m.nodeList[j].Name
		})

		if m.selectedIdx >= len(m.nodeList) {
			m.selectedIdx = 0
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.status = "Refreshing..."
			return m, m.fetchGraphCmd()
		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}
		case "down", "j":
			if m.selectedIdx < len(m.nodeList)-1 {
				m.selectedIdx++
			}
		}
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	// Top banner
	header := titleStyle.Render(" OVN Network Topology (L3) ")
	statusText := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(fmt.Sprintf(" %s  [q: quit | r: refresh | j/k: navigate]", m.status))
	b.WriteString(header + statusText + "\n\n")

	if m.err != nil {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FF453A")).Render(fmt.Sprintf("Error: %v\n", m.err)))
		b.WriteString("\nTip: Run daemon with `ovn-topologyd` or test with `--fixture`\n")
		return b.String()
	}

	if m.graphResp == nil {
		b.WriteString("Loading topology...\n")
		return b.String()
	}

	// Group nodes by VPC parent
	vpcGroups := make(map[string][]api.GraphNode)
	externalNodes := make([]api.GraphNode, 0)
	sharedNodes := make([]api.GraphNode, 0)

	for _, n := range m.graphResp.Nodes {
		if n.Type == api.GraphNodeTypeVPC {
			continue
		}
		if n.Type == api.GraphNodeTypeExternal {
			externalNodes = append(externalNodes, n)
		} else if n.Parent != nil && *n.Parent != "" {
			vpcName := strings.TrimPrefix(*n.Parent, graph.PrefixVPC)
			vpcGroups[vpcName] = append(vpcGroups[vpcName], n)
		} else {
			sharedNodes = append(sharedNodes, n)
		}
	}

	var leftPanel strings.Builder

	// Render VPC compound boxes
	for vpcName, nodes := range vpcGroups {
		var boxContent strings.Builder
		boxContent.WriteString(headerStyle.Render(fmt.Sprintf("VPC: %s", vpcName)) + "\n")

		for _, n := range nodes {
			line := formatNodeLine(n)
			if isSelected(n, m.nodeList, m.selectedIdx) {
				boxContent.WriteString(selectedNodeStyle.Render(line) + "\n")
			} else {
				boxContent.WriteString(nodeStyle.Render(line) + "\n")
			}
		}
		leftPanel.WriteString(vpcBoxStyle.Render(boxContent.String()) + "\n")
	}

	// Render Shared/Interconnect nodes (e.g. join subnets)
	if len(sharedNodes) > 0 {
		var sharedContent strings.Builder
		sharedContent.WriteString(headerStyle.Render("Interconnect (Multi-LR)") + "\n")
		for _, n := range sharedNodes {
			line := formatNodeLine(n)
			if isSelected(n, m.nodeList, m.selectedIdx) {
				sharedContent.WriteString(selectedNodeStyle.Render(line) + "\n")
			} else {
				sharedContent.WriteString(nodeStyle.Render(line) + "\n")
			}
		}
		leftPanel.WriteString(vpcBoxStyle.Render(sharedContent.String()) + "\n")
	}

	// Render External nodes
	if len(externalNodes) > 0 {
		var extContent strings.Builder
		extContent.WriteString(headerStyle.Render("External Underlays") + "\n")
		for _, n := range externalNodes {
			line := fmt.Sprintf("[EXT] %s", n.Name)
			if isSelected(n, m.nodeList, m.selectedIdx) {
				extContent.WriteString(selectedNodeStyle.Render(line) + "\n")
			} else {
				extContent.WriteString(nodeStyle.Render(line) + "\n")
			}
		}
		leftPanel.WriteString(vpcBoxStyle.Render(extContent.String()) + "\n")
	}

	// Detail side panel for selected node
	var rightPanel strings.Builder
	if len(m.nodeList) > 0 && m.selectedIdx < len(m.nodeList) {
		selected := m.nodeList[m.selectedIdx]
		rightPanel.WriteString(headerStyle.Render("Node Details") + "\n")
		fmt.Fprintf(&rightPanel, "ID:   %s\n", selected.Id)
		fmt.Fprintf(&rightPanel, "Name: %s\n", selected.Name)
		fmt.Fprintf(&rightPanel, "Type: %s\n", selected.Type)

		if selected.Properties != nil {
			rightPanel.WriteString("\nProperties:\n")
			for k, v := range *selected.Properties {
				fmt.Fprintf(&rightPanel, "  • %s: %v\n", k, v)
			}
		}

		if selected.K8s != nil {
			rightPanel.WriteString("\nKubernetes:\n")
			if selected.K8s.Namespace != nil {
				fmt.Fprintf(&rightPanel, "  Namespace: %s\n", *selected.K8s.Namespace)
			}
			if selected.K8s.Subnet != nil {
				fmt.Fprintf(&rightPanel, "  Subnet:    %s\n", *selected.K8s.Subnet)
			}
			if selected.K8s.Vpc != nil {
				fmt.Fprintf(&rightPanel, "  VPC:       %s\n", *selected.K8s.Vpc)
			}
		}
	}

	// Join left panel and side detail panel horizontally
	layout := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel.String(), detailBoxStyle.Render(rightPanel.String()))
	b.WriteString(layout)
	return b.String()
}

func isSelected(n api.GraphNode, list []api.GraphNode, selectedIdx int) bool {
	if selectedIdx < 0 || selectedIdx >= len(list) {
		return false
	}
	return list[selectedIdx].Id == n.Id
}

func formatNodeLine(n api.GraphNode) string {
	switch n.Type {
	case api.GraphNodeTypeLR:
		counts := ""
		if n.Properties != nil {
			p := *n.Properties
			counts = fmt.Sprintf(" (routes:%v, nat:%v, policies:%v, lb:%v)", p["routeCount"], p["natCount"], p["policyCount"], p["lbCount"])
		}
		return fmt.Sprintf("[Router] %s%s", n.Name, counts)
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
