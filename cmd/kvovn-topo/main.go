package main

// kvovn-topo: k9s-style TUI for the OVN topology served by kvovn-topod.
// Navigation is xray trees (vpc / chassis / workload) plus a :ports table; the
// Tier 1 diagnostics (ADR-0001) act on whatever is selected.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coulof/kvovn-topo/pkg/api"
	"github.com/coulof/kvovn-topo/pkg/diag"
	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/kubeproxy"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

type model struct {
	daemonURL string
	// httpClient carries the kubeconfig credentials on the service-proxy path;
	// nil means http.DefaultClient (direct --daemon-url). via labels the path.
	httpClient  *http.Client
	via         string
	fixturePath string
	theme       Theme
	themeIndex  int
	styles      ThemeStyles
	width       int
	height      int
	err         error

	// Graph data and indexes (data.go), shared by the views and the diagnostics.
	graphResp       *api.GraphResponse
	baseRevision    int
	nodeMap         map[string]api.GraphNode
	nodeList        []api.GraphNode
	vpcNames        []string
	vpcGroups       map[string][]api.GraphNode
	sharedNodes     []api.GraphNode
	externalNodes   []api.GraphNode
	portList        []RouterPortConnection
	gatewayPorts    []*RouterPortConnection // gateway LRPs with no router-peer edge (from LR properties)
	routerPorts     map[string][]RouterPortConnection
	switchPorts     map[string][]RouterPortConnection
	switchWorkloads map[string][]api.GraphNode
	chassisResp     *api.GraphResponse
	chassisNodes    []api.GraphNode

	// Xray / table UI
	tree       *xtree
	mode       mode
	scr        screen
	rows       []row
	cursor     int
	offset     int
	expanded   map[string]bool
	touched    map[string]bool
	history    []histEntry
	filter     string
	filterEdit bool
	cmdEdit    bool
	cmd        string
	desc       *descState
	pinspect   bool
	zone       string // ovn-kubernetes zone (node) on screen, from --zone or a zone-<node> capture

	// `!` runs an Inspect command (inspect.go)
	changes   []change // Changes view log (changes.go)
	changeSeq int

	// Topology history from the daemon (history.go, M8)
	histAvail   bool     // the daemon records history (probed at start)
	histChanges []change // view 5 from the daemon's feed, newest first
	histObject  string   // H: the graph ID view 5 is narrowed to
	histName    string   // H: its name (the filter when it has no graph ID)
	at, atLabel string   // historical view: the point every fetch asks for ("" = live)
	diff        *diffState
	helpTab     int // help screen tab and scroll (help.go)
	helpOff     int
	inspectPick bool
	inspectSel  int
	kube        kubeTarget

	// Cached node details for the details pane (lazy fetch, dropped when the
	// graph revision moves past detailsRev). Not baseRevision: that one marks
	// the first load, and moving it would stop changed rows from flashing.
	nodeDetails map[string]*api.NodeDetail
	nodeErrs    map[string]error
	nodeLoading map[string]bool
	detailsRev  int

	// Feedback and live updates
	flash         string
	flashAt       time.Time
	flashErr      bool
	changed       map[string]time.Time
	flashFor      time.Duration // --flash: how long a changed row stays marked
	needFlashTick bool
	live          liveState
	livePaused    bool
	pendingIDs    []string // changed IDs from SSE since the last graph load

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
	tcpdumpFilterIdx  int
	tcpdumpCustom     string
	tcpdumpPackets    []string
	diagTgt           diagTarget // captured when the modal opens (diagtarget.go)
	nicIdx            int
	chassisIdx        int
	pendingAction     diagAction
	diagStreaming     bool
	diagFollow        bool // live capture view sticks to the newest packets
	diagCancelStream  context.CancelFunc
	diagRawOutput     string
	diagRawView       bool
	diagScrollOffset  int
	diagError         error
}

type streamLineMsg struct {
	line    string
	scanner *bufio.Scanner
}

type streamDoneMsg struct {
	err error
}

type graphLoadedMsg struct {
	resp *api.GraphResponse
	err  error
}

type graphUnchangedMsg struct{}

type l2LoadedMsg struct {
	lsID string
	lsps []api.GraphNode
	err  error
}

type chassisLoadedMsg struct {
	resp *api.GraphResponse
	err  error
}

type nodeDetailMsg struct {
	id     string
	detail *api.NodeDetail
	err    error
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

// initialModel builds the TUI state. kube is the kubeconfig the diagnostics
// exec through; main passes the same one the service-proxy connection uses, so
// graph and diagnostics always target the same cluster (nil: default rules).
func initialModel(daemonURL, fixturePath, themeName string, diagMode bool, kube clientcmd.ClientConfig, k8sNamespace string) model {
	theme, idx := GetThemeByName(themeName)

	m := model{
		daemonURL:        daemonURL,
		fixturePath:      fixturePath,
		mode:             modeVPC,
		themeIndex:       idx,
		theme:            theme,
		styles:           CompileStyles(theme),
		diagMode:         diagMode,
		k8sNamespace:     k8sNamespace,
		nodeMap:          make(map[string]api.GraphNode),
		routerPorts:      make(map[string][]RouterPortConnection),
		switchPorts:      make(map[string][]RouterPortConnection),
		switchWorkloads:  make(map[string][]api.GraphNode),
		expanded:         make(map[string]bool),
		touched:          make(map[string]bool),
		changed:          make(map[string]time.Time),
		flashFor:         defaultFlashFor,
		pinspect:         true,
		nodeDetails:      make(map[string]*api.NodeDetail),
		nodeErrs:         make(map[string]error),
		nodeLoading:      make(map[string]bool),
		traceProtocol:    AvailableTraceProtocols[0].Protocol,
		tracePort:        AvailableTraceProtocols[0].Port,
		traceProtoIdx:    0,
		traceCategoryIdx: 0,
	}

	if diagMode {
		if kube == nil {
			kube = kubeproxy.Loader("", "")
		}
		k8sConfig, err := kube.ClientConfig()

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
	if m.fixturePath == "" {
		cmds = append(cmds, m.connectLiveCmd(), m.probeHistoryCmd())
	}
	if m.diagMode && m.diagExecutor != nil && m.diagLocator != nil {
		cmds = append(cmds, m.probeDiagCmd())
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	res, cmd := m.update(msg)
	mm := res.(model)
	if mm.needFlashTick {
		mm.needFlashTick = false
		cmd = tea.Batch(cmd, flashTick(mm.flashFor))
	}
	// If details pane is visible and selected object is an LR whose detail isn't loaded, fetch it
	if detailCmd := mm.lazyFetchSelectedDetailCmd(); detailCmd != nil {
		cmd = tea.Batch(cmd, detailCmd)
	}
	if mm.mode == modeProblems { // a new revision drops the details the return-path check needs
		if c := mm.problemsFetchCmd(); c != nil {
			cmd = tea.Batch(cmd, c)
		}
	}
	return mm, cmd
}

// detailRouterID is the router whose /node detail the pane needs: the selected
// LR, or the router of a selected LRP (for the routes that leave through it).
func detailRouterID(n *xnode) string {
	switch {
	case n == nil:
		return ""
	case n.kind == kLR && n.gn != nil:
		return n.gn.Id
	case n.kind == kLRP && n.conn != nil:
		return n.conn.RouterID
	}
	return ""
}

// lazyFetchSelectedDetailCmd triggers a lazy fetch of /node/{id} when the details pane shows a router or router port.
func (m *model) lazyFetchSelectedDetailCmd() tea.Cmd {
	if !m.showPane() || m.scr != scrMain {
		return nil
	}
	id := detailRouterID(m.selectedXNode())
	if id == "" {
		return nil
	}
	if m.nodeDetails[id] != nil || m.nodeLoading[id] || m.nodeErrs[id] != nil {
		return nil
	}
	m.nodeLoading[id] = true
	return m.fetchNodeDetailCmd(id)
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.offset = window(m.cursor, m.offset, len(m.rows), m.bodyH())
		return m, nil

	case graphLoadedMsg:
		if msg.err != nil {
			if m.graphResp == nil {
				m.err = msg.err
			} else {
				m.setFlash("refresh failed: "+msg.err.Error(), true)
			}
			return m, nil
		}
		if m.tree == nil {
			m.baseRevision = msg.resp.Revision
		}
		if msg.resp.Revision != m.detailsRev {
			m.detailsRev = msg.resp.Revision
			m.nodeDetails = make(map[string]*api.NodeDetail)
			m.nodeErrs = make(map[string]error)
			m.nodeLoading = make(map[string]bool)
		}
		switches, chassis := m.refetchPlan(m.pendingIDs)
		m.pendingIDs = nil
		m.applyGraph(msg.resp)
		cmds := []tea.Cmd{m.fetchSwitchesCmd(switches)}
		if chassis || m.chassisResp == nil {
			cmds = append(cmds, m.fetchChassisCmd())
		}
		if m.histAvail && m.mode == modeChanges {
			cmds = append(cmds, m.fetchHistEventsCmd())
		}
		return m, tea.Batch(cmds...)

	case histCoverageMsg:
		m.histAvail = msg.err == nil && msg.cov != nil
		if m.histAvail && m.mode == modeChanges {
			return m, m.fetchHistEventsCmd()
		}
		return m, nil

	case histEventsMsg:
		if msg.object != m.histObject || msg.name != m.histName {
			return m, nil // H moved on while this was loading
		}
		if msg.err != nil {
			m.setFlash("history: "+msg.err.Error(), true)
			return m, nil
		}
		m.histChanges = histChangesFrom(msg.events)
		if m.mode == modeChanges {
			m.rebuildRows()
		}
		return m, nil

	case diffLoadedMsg:
		if m.diff != nil && m.diff.title == msg.title {
			m.diff.loading, m.diff.diffs, m.diff.err = false, msg.diffs, msg.err
		}
		return m, nil

	case graphUnchangedMsg:
		return m, nil

	case l2LoadedMsg:
		if msg.err != nil {
			m.setFlash("ports of "+msg.lsID+": "+msg.err.Error(), true)
			return m, nil
		}
		m.switchWorkloads[msg.lsID] = msg.lsps
		m.rebuildTree()
		return m, nil

	case chassisLoadedMsg:
		if msg.err != nil {
			m.setFlash("chassis: "+msg.err.Error(), true)
			return m, nil
		}
		m.chassisResp = msg.resp
		m.chassisNodes = msg.resp.Nodes
		m.rebuildTree()
		return m, nil

	case inspectDoneMsg:
		switch {
		case msg.err != nil:
			m.setFlash("run failed: "+msg.err.Error(), true)
		default:
			m.setFlash(fmt.Sprintf("ran: %s (exit %d)", msg.text, msg.code), msg.code != 0)
		}
		return m, nil

	case nodeDetailMsg:
		// Cache only answers to a fetch made in this revision; one still in
		// flight when the revision moved would otherwise be kept as current.
		if m.nodeLoading[msg.id] {
			m.nodeLoading[msg.id] = false
			if msg.err != nil {
				m.nodeErrs[msg.id] = msg.err
			} else {
				m.nodeDetails[msg.id] = msg.detail
				if m.tree != nil && m.tree.byID[msg.id] != nil {
					m.tree.byID[msg.id].detailRefs = m.detailRefsFor(msg.detail)
					m.rebuildRows()
				}
			}
		}
		if m.desc != nil && m.desc.node != nil && m.desc.node.gn != nil && m.desc.node.gn.Id == msg.id {
			m.desc.loading = false
			m.desc.detail, m.desc.err = msg.detail, msg.err
		}
		return m, nil

	case liveEventMsg, liveErrMsg, pollTickMsg:
		return m.updateLive(msg)

	case flashTickMsg:
		for id, t := range m.changed {
			if time.Since(t) >= m.flashFor {
				delete(m.changed, id)
			}
		}
		return m, nil

	case diagProbeMsg:
		m.diagAvailable = msg.available
		m.diagReason = msg.reason
		m.diagCentralPod = msg.centralPod
		if msg.available {
			m.setFlash("diagnostics ready: "+msg.reason, false)
		} else {
			m.setFlash("diagnostics degraded: "+msg.reason, true)
		}
		return m, nil

	case streamLineMsg:
		if !m.diagStreaming {
			return m, nil
		}
		m.tcpdumpPackets = append(m.tcpdumpPackets, msg.line)
		m.diagRawOutput = strings.Join(m.tcpdumpPackets, "\n")
		return m, readNextStreamLineCmd(msg.scanner)

	case streamDoneMsg:
		m.diagStreaming = false
		m.freezeFollow()
		m.diagRunning = false
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) && !errors.Is(msg.err, io.EOF) {
			m.diagError = msg.err
		}
		return m, nil

	case tcpdumpStartedMsg:
		if msg.err != nil {
			m.diagStreaming, m.diagRunning = false, false
			m.diagError = fmt.Errorf("tcpdump not started: %w", msg.err)
			return m, nil
		}
		if !m.diagStreaming { // stopped while the interface was being resolved
			return m, nil
		}
		m.diagRunning = false // capture is live: show packets as they arrive
		m.diagTitle = msg.title
		m.tcpdumpPackets = msg.header
		m.diagRawOutput = strings.Join(m.tcpdumpPackets, "\n")
		return m, readNextStreamLineCmd(msg.scanner)

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
			return m.updateDiagModal(msg)
		}
		if m.inspectPick {
			return m.updateInspectPick(msg)
		}
		return m.handleKey(msg)
	}

	return m, nil
}

func runMachineOutput(daemonURL string, client *http.Client, fixturePath, viewName, focusID, at, format string) error {
	var resp *api.GraphResponse
	var err error

	if viewName == "all" {
		var snap *graph.Snapshot
		if fixturePath != "" {
			if snap, err = fixtureSnapshot(fixturePath); err != nil {
				return err
			}
		}
		d, err := buildDump(snap, daemonURL, client, at)
		if err != nil {
			return err
		}
		return writeMachine(d, format)
	}

	if fixturePath != "" {
		var snap *graph.Snapshot
		if snap, err = fixtureSnapshot(fixturePath); err == nil {
			resp, err = snap.BuildGraph(viewName, focusID, 1, 1, 100)
		}
	} else {
		q := url.Values{"view": {viewName}}
		if focusID != "" {
			q.Set("focus", focusID)
		}
		withAt(q, at)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var decoded api.GraphResponse
		if err := httpGetJSON(ctx, client, apiURL(daemonURL, "/graph", q), "", &decoded); err != nil {
			return err
		}
		resp = &decoded
	}

	if err != nil {
		return err
	}
	return writeMachine(resp, format)
}

// writeMachine prints v to stdout as json or yaml.
func writeMachine(v any, format string) error {
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case "yaml", "yml":
		yBytes, err := yaml.Marshal(v)
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
	var fixtureDir string
	var themeName string
	var diagMode bool
	var kubeconfig string
	var k8sNamespace string
	var outputFormat string
	var viewName string
	var focusID string
	var zone string
	var flashFor time.Duration
	var configPath string
	var atPoint, diffFrom, diffTo string

	var kubeContext, namespace, service, port string
	flag.StringVar(&daemonURL, "daemon-url", "", "kvovn-topod API URL, e.g. http://127.0.0.1:8080/api/v1 (default: the in-cluster daemon through the Kubernetes service proxy)")
	flag.StringVar(&kubeContext, "context", "", "kubeconfig context (default: current context)")
	flag.StringVar(&namespace, "namespace", kubeproxy.Default.Namespace, "namespace of the kvovn-topod Service; setting it (or --service) skips discovery")
	flag.StringVar(&service, "service", kubeproxy.Default.Name, "name of the kvovn-topod Service; setting it (or --namespace) skips discovery")
	flag.StringVar(&port, "port", "", "kvovn-topod Service port name or number (default: the port named http)")
	flag.BoolVar(&useFixture, "fixture", false, "Use bundled dev fixture dataset offline")
	flag.StringVar(&fixtureDir, "fixture-dir", "", "Explore a captured scenario offline (testdata/scenarios/<name>, or one zone-<node> of an ovn-kubernetes capture)")
	flag.StringVar(&themeName, "theme", getEnvDefault("THEME", "default"), "Color theme: default, suse, dracula, nord, tokyonight, catppuccin")
	flag.BoolVar(&diagMode, "diag", false, "Enable Tier 1 exec-based diagnostics")
	flag.StringVar(&kubeconfig, "kubeconfig", getEnvDefault("KUBECONFIG", ""), "Path to kubeconfig (defaults to standard kubeconfig paths)")
	flag.StringVar(&k8sNamespace, "k8s-namespace", "", "Kubernetes namespace of the OVN pods diagnostics exec into (default: detect kube-system for Kube-OVN, else openshift-ovn-kubernetes or ovn-kubernetes)")
	flag.StringVar(&outputFormat, "o", "", "Output format: json or yaml (scriptable machine output mode)")
	flag.StringVar(&viewName, "view", "l3", "View for machine output: l3, l2, chassis, or all (every view and every switch in one document, for offline debugging)")
	flag.StringVar(&focusID, "focus", "", "Focus target for view (e.g. ls:<name|uuid>)")
	flashDefault := defaultFlashFor
	if d, err := time.ParseDuration(os.Getenv("KVOVN_TOPO_FLASH")); err == nil {
		flashDefault = d
	}
	flag.DurationVar(&flashFor, "flash", flashDefault, "How long a changed row stays highlighted: solid for 3s, then a ▌ mark; 0 turns it off (env KVOVN_TOPO_FLASH)")
	flag.StringVar(&configPath, "config", "", "Config file: UI preferences and per-context daemon settings (default: $XDG_CONFIG_HOME/kvovn-topo/config.yaml or ~/.config/kvovn-topo/config.yaml)")
	flag.StringVar(&atPoint, "at", "", "Browse the topology as it was (daemon history, M8): 15:04, \"2026-11-06 15:04\", RFC3339, -1h or a revision; also applies to -o")
	flag.StringVar(&diffFrom, "from", "", "kvovn-topo diff: the older point (same forms as --at)")
	flag.StringVar(&diffTo, "to", "", "kvovn-topo diff: the newer point (default now)")
	flag.StringVar(&zone, "zone", "", "With --diag: live view of one ovn-kubernetes interconnect zone (node) through an exec relay, e.g. OpenShift; \"list\" prints the zones")

	// "kvovn-topo mcp": the MCP server has its own flags and never starts the TUI.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(runMCP(os.Args[2:], os.Stderr))
	}

	// "kvovn-topo skill": print the agent skill and exit.
	if len(os.Args) > 1 && os.Args[1] == "skill" {
		os.Exit(runSkill(os.Stdout))
	}

	// Allow "kvovn-topo graph ..." style invocations; "kvovn-topo diff" prints a diff.
	diffMode := false
	if len(os.Args) > 1 && (os.Args[1] == "graph" || os.Args[1] == "diff") {
		diffMode = os.Args[1] == "diff"
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	flag.Parse()

	atLabel := ""
	if atPoint != "" {
		var err error
		if atPoint, atLabel, err = normalizePoint(atPoint, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "Error: --at: %v\n", err)
			os.Exit(2)
		}
	}

	fixturePath := ""
	if useFixture {
		fixturePath = devFixture
	}
	if fixtureDir != "" {
		// Load once up front so a bad directory fails here, not inside the TUI.
		if _, err := fixtureSnapshot(fixtureDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fixturePath = fixtureDir
	}

	// Without --daemon-url, reach the in-cluster daemon the supported way: the
	// API server's service proxy with the kubeconfig's credentials and RBAC.
	loader := kubeproxy.Loader(kubeconfig, kubeContext)
	currentContext := ""
	if raw, err := loader.RawConfig(); err == nil {
		currentContext = raw.CurrentContext
	}

	// The config file fills what no flag or environment variable set.
	setFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	cfg, err := loadConfig(firstNonEmpty(configPath, defaultConfigPath()), setFlags["config"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	opts := settings{theme: themeName, flash: flashFor, pane: true, namespace: namespace, service: service, port: port,
		k8sNamespace: k8sNamespace, pinned: setFlags["namespace"] || setFlags["service"]}
	cfg.apply(&opts, firstNonEmpty(kubeContext, currentContext), func(name string) bool { return setFlags[name] }, os.Getenv)
	themeName, flashFor, namespace, service, port, k8sNamespace = opts.theme, opts.flash, opts.namespace, opts.service, opts.port, opts.k8sNamespace

	var client *http.Client
	via := "direct " + daemonURL

	// --zone: the zone's NB/SB through exec relays, an embedded graph on loopback.
	if zone != "" {
		if !diagMode {
			fmt.Fprintln(os.Stderr, "Error: live ovn-kubernetes zones are Tier 1 (pods/exec relay); add --diag")
			os.Exit(2)
		}
		if zone == "list" {
			if err := listZones(loader, k8sNamespace, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		url, err := startZone(ctx, loader, k8sNamespace, zone, os.Stderr)
		if err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "Error: %v\nRun with --zone list to see the zones.\n", err)
			os.Exit(1)
		}
		daemonURL, via = url, "zone "+zone+" (exec relay)"
	}

	if fixturePath == "" && daemonURL == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		conn, err := kubeproxy.Connect(ctx, loader,
			kubeproxy.ServiceRef{Namespace: namespace, Name: service, Port: port}, opts.pinned)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\nUse --daemon-url for a daemon you can reach directly, or --fixture / --fixture-dir to explore offline.\n", err)
			os.Exit(1)
		}
		daemonURL, client, via = conn.BaseURL, conn.Client, "service proxy "+conn.Service.String()
	}

	if diffMode {
		err := runDiffCLI(daemonURL, client, fixturePath, diffFrom, diffTo, viewName, focusID, outputFormat, func(s string) { fmt.Println(s) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if outputFormat != "" {
		if atPoint != "" && fixturePath != "" {
			fmt.Fprintln(os.Stderr, "Error: --at needs the daemon's history; an offline fixture has none")
			os.Exit(2)
		}
		if err := runMachineOutput(daemonURL, client, fixturePath, viewName, focusID, atPoint, outputFormat); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	m := initialModel(daemonURL, fixturePath, themeName, diagMode, loader, k8sNamespace)
	m.httpClient, m.via = client, via
	m.flashFor, m.pinspect = flashFor, opts.pane
	m.at, m.atLabel = atPoint, atLabel
	m.zone = firstNonEmpty(zone, zoneOf(fixtureDir))
	m.kube = kubeTarget{kubeconfig: kubeconfig, context: kubeContext, currentContext: currentContext}
	if zone != "" && m.diagLocator != nil {
		m.diagLocator.SetHomeZone(zone)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
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
