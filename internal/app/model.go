package app

import (
	"os"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

var claudeSpinner = spinner.Spinner{
	Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	FPS:    80 * time.Millisecond,
}

type AppState int

const (
	StateNormal AppState = iota
	StateFiltering
	StateKillConfirm
	StatePromptRelay
	StateQueueRelay
	StateDeferPrompt
	StatePalette
)

// originalPane stores the tmux pane that was active when the TUI launched,
// so we can restore it on ESC/quit.
type originalPane struct {
	Session  string
	Window   int
	Pane     int
	PaneID   string
	Captured bool // true once we've successfully captured the state
}

type Model struct {
	client         *daemon.Client
	list           ui.ListModel
	preview        ui.PreviewModel
	filter         ui.FilterModel
	relay          ui.RelayModel
	queueRelay     ui.RelayModel
	deferPrompt    ui.DeferPromptModel
	minimap        ui.MinimapModel
	usageBar       ui.UsageBarModel
	sessions       []claude.ClaudeSession
	state          AppState
	showHooks         bool
	hideTranscript    bool
	showMinimap       bool
	inFullscreenPopup bool   // true when launched via CLAUDE_TUI_FULLSCREEN=1
	binaryPath        string // cached os.Executable() result
	minimapSession    string // tmux session currently shown in minimap
	origPane       originalPane // tmux state to restore on ESC
	spinner        spinner.Model
	width          int
	height         int
	listWidthPct   int // percentage of total width for the session list
	ready          bool
	err            error
	flashMsg       string    // transient message overlay
	flashIsError   bool      // true = error style, false = info style
	flashExpiry    time.Time // when to auto-dismiss the flash
	renaming       bool   // true while Haiku is generating a window name
	pendingChord         string // accumulated chord prefix (e.g. "y" waiting for next key)
	initialSelectionDone bool   // true after first smart cursor placement
	killTargetPaneID     string // pane being confirmed for kill
	killTargetPID        int    // PID of the claude process to kill
	killTargetTitle      string // display title for kill confirmation
	killTargetBookmarkID string // bookmark ID to remove when killing a Later session
	selectActive         bool   // true when launched with CMC_SELECT_ACTIVE=1 (ctrl-space)
	debugMode            bool   // toggle debug overlay (D key)
	showHelp             bool   // toggle help overlay (? key)
	palette              ui.PaletteModel
	commands             []Command
}

func NewModel(client *daemon.Client) Model {
	list := ui.NewListModel()
	list.SetGroupByProject(loadPrefBool("groupByProject"))
	s := spinner.New()
	s.Spinner = claudeSpinner
	bin, _ := os.Executable()
	return Model{
		client:            client,
		list:              list,
		preview:           ui.NewPreviewModel(),
		filter:            ui.NewFilterModel(),
		relay:             ui.NewRelayModel(),
		queueRelay:        ui.NewQueueRelayModel(),
		deferPrompt:       ui.NewDeferPromptModel(),
		palette:           ui.NewPaletteModel(),
		commands:          buildCommands(),
		minimap:           ui.NewMinimapModel(),
		showMinimap:       loadPrefBool("minimap"),
		listWidthPct:      loadPrefInt("listWidthPct", 30),
		spinner:           s,
		inFullscreenPopup: os.Getenv("CLAUDE_TUI_FULLSCREEN") == "1",
		selectActive:      os.Getenv("CMC_SELECT_ACTIVE") == "1",
		binaryPath:        bin,
	}
}

// applyLayout recomputes and applies component sizes from m.width, m.height, m.listWidthPct.
func (m *Model) applyLayout() {
	innerWidth := m.width
	contentHeight := m.height - 2 // label + footer
	if !m.inFullscreenPopup {
		innerWidth -= 2 // left/right border chars
		contentHeight -= 2 // top border + bottom border
	}
	listWidth := max(innerWidth*m.listWidthPct/100, 20)
	m.list.SetSize(listWidth-1, contentHeight)
	m.preview.SetSize(innerWidth-listWidth, contentHeight)
	minimapH := min(contentHeight/2, 14)
	m.minimap.SetSize(0, minimapH)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.subscribeToDaemon(),
		m.spinner.Tick,
		captureOriginalPane(),
	)
}

// captureOriginalPane snapshots the tmux client's active pane at startup.
func captureOriginalPane() tea.Cmd {
	return func() tea.Msg {
		session, window, pane, paneID, err := tmux.GetClientSession()
		return OriginalPaneCapturedMsg{
			Session: session, Window: window, Pane: pane, PaneID: paneID, Err: err,
		}
	}
}

// switchPaneQuiet switches the tmux client to a pane without flashing (fire-and-forget).
func switchPaneQuiet(sessionName string, windowIndex, paneIndex int) tea.Cmd {
	return func() tea.Msg {
		tmux.SwitchToPaneQuiet(sessionName, windowIndex, paneIndex)
		return nil
	}
}

// subscribeToDaemon sends the subscribe request and returns the initial sessions.
func (m Model) subscribeToDaemon() tea.Cmd {
	return func() tea.Msg {
		sessions, usage, err := m.client.Subscribe()
		if err != nil {
			return DaemonDisconnectedMsg{Err: err}
		}
		return SessionsRefreshedMsg{Sessions: sessions, Usage: usage}
	}
}

// waitForDaemonUpdate blocks until the daemon pushes the next session snapshot.
func (m Model) waitForDaemonUpdate() tea.Cmd {
	return func() tea.Msg {
		sessions, usage, err := m.client.ReadNext()
		if err != nil {
			return DaemonDisconnectedMsg{Err: err}
		}
		return SessionsRefreshedMsg{Sessions: sessions, Usage: usage}
	}
}

func capturePreview(paneID string) tea.Cmd {
	return func() tea.Msg {
		content, err := tmux.CapturePaneContent(paneID)
		return PreviewReadyMsg{PaneID: paneID, Content: content, Err: err}
	}
}

func (m Model) fetchTranscript(paneID, sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		msgs, _ := m.client.Transcript(sessionID)
		return TranscriptReadyMsg{PaneID: paneID, Messages: msgs}
	}
}

func (m Model) fetchHooks(paneID string) tea.Cmd {
	return func() tea.Msg {
		events, _ := m.client.HookEvents(paneID)
		return HooksReadyMsg{PaneID: paneID, Events: events}
	}
}

func (m Model) fetchDiffStats(paneID, sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		stats, _ := m.client.DiffStats(sessionID)
		return DiffStatsReadyMsg{PaneID: paneID, SessionID: sessionID, Stats: stats}
	}
}

func (m Model) fetchAllDiffStats(sessions []claude.ClaudeSession) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range sessions {
		if s.SessionID != "" {
			cmds = append(cmds, m.fetchDiffStats(s.PaneID, s.SessionID))
		}
	}
	return tea.Batch(cmds...)
}

func (m Model) fetchCachedSummary(paneID, sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		summary, _ := m.client.Summary(sessionID)
		if summary == nil {
			return nil
		}
		return SummaryReadyMsg{PaneID: paneID, Summary: summary, FromCache: true}
	}
}

func (m Model) fetchSynthesize(paneID, sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		summary, fromCache, err := m.client.Synthesize(paneID, sessionID)
		return SummaryReadyMsg{PaneID: paneID, Summary: summary, Err: err, FromCache: fromCache, UserRequested: true}
	}
}

func (m Model) fetchSynthesizeAll(skipPaneID string) tea.Cmd {
	return func() tea.Msg {
		results, err := m.client.SynthesizeAll(skipPaneID)
		if err != nil {
			return SynthesizeAllReadyMsg{Err: err}
		}
		appResults := make([]SynthesizeAllResult, len(results))
		for i, r := range results {
			appResults[i] = SynthesizeAllResult{PaneID: r.PaneID, Summary: r.Summary, FromCache: r.FromCache}
		}
		return SynthesizeAllReadyMsg{Results: appResults}
	}
}

func (m Model) fetchMinimapData(sessionName string) tea.Cmd {
	return func() tea.Msg {
		panes, err := m.client.PaneGeometry(sessionName)
		if err != nil {
			return MinimapReadyMsg{SessionName: sessionName}
		}
		return MinimapReadyMsg{SessionName: sessionName, Panes: panes}
	}
}

func sendPromptRelay(paneID, text string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.SendKeysLiteral(paneID, text); err != nil {
			return flashErrorMsg("send failed: " + err.Error())
		}
		return flashInfoMsg("sent")
	}
}

func (m Model) fetchRenameWindow(sessionName string, windowIndex int) tea.Cmd {
	return func() tea.Msg {
		name, err := m.client.RenameWindow(sessionName, windowIndex)
		return WindowRenameMsg{Name: name, Err: err}
	}
}
