package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
	"github.com/huylenq/claude-mission-control/internal/ui/destroyer"
)

// MessageLogEntry is a recorded flash message for the message log.
type MessageLogEntry struct {
	Text    string
	IsError bool
	Time    time.Time
}

const maxMessageLog = 50
const messageToastTTL = 8 * time.Second

var claudeSpinner = spinner.Spinner{
	Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	FPS:    80 * time.Millisecond,
}

type AppState int

const (
	StateNormal AppState = iota
	StateSearching
	StateKillConfirm
	StatePromptRelay
	StateQueueRelay
	StatePalette
	StateNewSessionPrompt
	StateNewSessionPathInput  // path text input before the new-session prompt
	StateMinimapSettings
	StatePrefsEditor
	StateBacklogPrompt        // creating or editing a backlog item
	StateBacklogDeleteConfirm // confirming deletion of a backlog item
	StateMacro                // macro palette shown, waiting for key
	StateMacroEdit            // inline macro editor open
	StateTagRelay             // tag input relay open
	StateNoteEdit             // session note editor open
	StateCopilot              // copilot chat panel active
	StateCopilotConfirm       // copilot tool confirmation pending
	StateAdjustCopilot        // copilot overlay resize/reposition mode
	StateLaterWait            // waiting for optional duration input before marking as later
	StateDestroyer            // session destroyer easter egg
)

const defaultMinimapMaxH = 14

// Copilot display modes (persisted as pref).
const (
	CopilotModeFloat  = "float"
	CopilotModeDocked = "docked"
)

const (
	defaultCopilotDockedW = 70
	minCopilotDockedW     = 40
	doubleTabThreshold    = 300 * time.Millisecond
)

// Sidebar width percentage bounds (clamped during keyboard/mouse resize).
const (
	minSidebarWidthPct = 10
	maxSidebarWidthPct = 60
)

// Minimap display modes (cycled with M key).
const (
	MinimapAuto   = "auto"   // docked in fullscreen, overlay in normal
	MinimapDocked = "docked" // always docked at bottom
	MinimapFloat  = "float"  // always overlay
	MinimapSmart  = "smart"  // docked when minimap wider than sidebar panel
)

var minimapModes = []string{MinimapAuto, MinimapDocked, MinimapFloat, MinimapSmart}

// Outline display modes (cycled with T key).
const (
	ChatOutlineOverlay    = "overlay"     // floats on top of viewport
	ChatOutlineDocked     = "docked"      // side-by-side with viewport (right)
	ChatOutlineDockedLeft = "docked-left" // side-by-side with viewport (left)
	ChatOutlineHidden     = "hidden"      // not shown
)

var chatOutlineModes = []string{ChatOutlineOverlay, ChatOutlineDocked, ChatOutlineDockedLeft, ChatOutlineHidden}

func nextChatOutlineMode(mode string) string {
	for i, m := range chatOutlineModes {
		if m == mode {
			return chatOutlineModes[(i+1)%len(chatOutlineModes)]
		}
	}
	return ChatOutlineOverlay
}

func chatOutlineModeFlash(active string) string {
	var parts []string
	for _, mode := range chatOutlineModes {
		if mode == active {
			parts = append(parts, ui.FooterKeyStyle.Render(mode))
		} else {
			parts = append(parts, ui.FooterDimStyle.Render(mode))
		}
	}
	return "chat outline: " + strings.Join(parts, ui.FooterDimStyle.Render(" · "))
}

// minimapModeFlash returns a styled string showing all modes with the active one highlighted,
// plus a scale indicator showing the current max height.
func minimapModeFlash(active string, maxH int, collapse bool) string {
	var parts []string
	for _, mode := range minimapModes {
		if mode == active {
			parts = append(parts, ui.FooterKeyStyle.Render(mode))
		} else {
			parts = append(parts, ui.FooterDimStyle.Render(mode))
		}
	}
	scale := "  " + ui.FooterKeyStyle.Render("+/-") + " " + ui.FooterKeyStyle.Render(fmt.Sprintf("%d", maxH))
	collapseLabel := ui.FooterDimStyle.Render("collapse")
	if collapse {
		collapseLabel = ui.FooterKeyStyle.Render("collapse")
	}
	return "minimap: " + strings.Join(parts, ui.FooterDimStyle.Render(" · ")) + scale + "  " + ui.FooterKeyStyle.Render("c") + " " + collapseLabel
}

func nextMinimapMode(mode string) string {
	switch mode {
	case MinimapAuto:
		return MinimapDocked
	case MinimapDocked:
		return MinimapFloat
	case MinimapFloat:
		return MinimapSmart
	case MinimapSmart:
		return MinimapAuto
	default:
		return MinimapAuto
	}
}

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
	client               *daemon.Client
	sidebar              ui.SidebarModel
	detail               ui.DetailModel
	search               ui.SearchModel
	relay                ui.RelayModel
	queueRelay           ui.RelayModel
	tagRelay             ui.RelayModel
	laterRelay           ui.RelayModel
	laterKillMode        bool // true when StateLaterWait was triggered by W (later+kill)
	minimap              ui.MinimapModel
	usageBar             ui.UsageBarModel
	sessions             []claude.ClaudeSession
	state                AppState
	showHooks            bool
	showRawTranscript    bool
	showDiffs            bool
	chatOutlineMode          string // ChatOutlineOverlay, ChatOutlineDocked, ChatOutlineHidden
	showMinimap          bool
	minimapMode          string       // MinimapAuto, MinimapDocked, MinimapFloat, MinimapSmart
	minimapMaxH          int          // max minimap height (persisted pref, default 14)
	minimapCollapse      bool         // collapse single-pane windows in minimap
	inFullscreenPopup    bool         // true when launched via CLAUDE_TUI_FULLSCREEN=1
	binaryPath           string       // cached os.Executable() result
	minimapSession       string       // tmux session currently shown in minimap
	origPane             originalPane // tmux state to restore on ESC
	spinner              spinner.Model
	width                int
	height               int
	sidebarWidthPct      int // percentage of total width for the sidebar
	ready                bool
	err                  error
	autoJumpTextUntil    time.Time         // show "ON"/"OFF" text next to autojump glyph until this time
	flashMsg             string            // transient message overlay
	flashIsError         bool              // true = error style, false = info style
	flashExpiry          time.Time         // when to auto-dismiss the flash
	messageLog           []MessageLogEntry // ring buffer of past flash messages (permanent history)
	toastQueue           []MessageLogEntry // entries actively displayed in the toast overlay
	showMessageLog       bool              // toggle full message log overlay
	renaming             bool              // true while Haiku is generating a window name
	pendingChord         string            // accumulated chord prefix (e.g. "y" waiting for next key)
	initialSelectionDone bool              // true after first smart cursor placement
	killTargetPaneID     string            // pane being confirmed for kill
	killTargetSessionID  string            // session ID of the pane being killed
	killTargetPID        int               // PID of the claude process to kill
	killTargetTitle      string            // display title for kill confirmation
	killTargetAnimalIdx  int               // avatar animal index for kill confirmation
	killTargetColorIdx   int               // avatar color index for kill confirmation
	killTargetBookmarkID string            // bookmark ID to remove when killing a Later session
	selectActive         bool              // true when launched with CMC_SELECT_ACTIVE=1 (ctrl-space)
	rotateNext           bool              // true when launched with CMC_ROTATE_NEXT=1 (ctrl-tab)
	pendingSelectPaneID  string            // pane to auto-select once it appears in the sidebar
	promptEditor         ui.PromptEditorModel
	pathInput            ui.RelayModel             // single-line path input for "A" new-session-at-path
	newSessionProject    string                    // project name for the new session being created
	newSessionCWD        string                    // working directory for the new session
	newSessionTmuxSess   string                    // tmux session for the new window
	newSessionPrevPaneID string                    // session to restore if prompt is cancelled from session level
	newSessionWasSession bool                      // true if `a` was pressed from session level
	queueCursor          int                       // -1 = text input focused, >= 0 = highlighted item index
	debugMode            bool                      // toggle debug overlay (D key)
	globalEffects        []claude.GlobalHookEffect // latest handled effects across all sessions
	showHelp             bool                      // toggle help overlay (? key)
	showSpiritAnimal     bool                      // toggle spirit animal overlay (gs chord)
	lastClickPaneID      string                    // pane clicked last (for double-click detection)
	lastClickTime        time.Time                 // when the last minimap click happened
	outlineDragging      bool                      // true while drag-resizing the chat outline panel
	outlineDragStartX    int                       // terminal x at drag start
	outlineDragStartW    int                       // panel width at drag start
	sidebarDragging      bool                      // true while drag-resizing the sidebar panel
	sidebarDragStartX    int                       // terminal x at drag start
	sidebarDragStartPct  int                       // sidebarWidthPct at drag start
	jumpTrail            []string                  // pane IDs for jump history (like Vim's jumplist)
	jumpCursor           int                       // position in jumpTrail; len(jumpTrail) = at head
	nonClaudePane        *ui.MinimapPaneInfo       // focused non-Claude pane (minimap nav)
	palette              ui.PaletteModel
	settingsCursor       int // cursor position in settings overlay
	commands             []Command
	activeBacklogID      string         // backlog item being edited or submitted (empty = new item)
	activeBacklogCWD     string         // CWD for the active backlog operation
	backlogOverlay       bool           // true = show backlog prompt as overlay; false = right-pane editor
	backlogScroll        int            // scroll offset (in lines) for the backlog preview panel
	deleteTargetBacklog  claude.Backlog // backlog item pending delete confirmation
	macros               []claude.Macro // loaded macros (built-in + user)
	macroEditor          ui.MacroEditorModel
	copilot              ui.CopilotModel
	copilotInput         ui.RelayModel
	copilotVisible       bool      // overlay rendered but may not be focused (StateNormal + visible = read-only)
	copilotMode          string    // CopilotModeFloat or CopilotModeDocked (persisted)
	copilotDockedW       int       // docked panel width in columns (persisted)
	lastTabTime          time.Time // for double-tab detection
	copilotOffX          int       // horizontal offset from default position (negative = left) [float only]
	copilotOffY          int       // vertical offset from default position (negative = up) [float only]
	copilotDW            int       // delta width from default (positive = wider) [float only]
	copilotDH            int       // delta max-height from default (positive = taller) [float only]
	destroyer *destroyer.Model // session destroyer easter egg (nil = inactive)
}

func NewModel(client *daemon.Client) Model {
	sidebar := ui.NewSidebarModel()
	sidebar.SetGroupByProject(loadPrefBool("groupByProject"))
	migratePref("showIdeas", "showBacklog")
	migratePref("showBacklog", "backlogExpanded")
	sidebar.SetBacklogExpanded(loadPrefBool("backlogExpanded"))
	sidebar.SetLaterExpanded(!loadPrefBool("laterCollapsed"))
	sidebar.SetClaudingExpanded(!loadPrefBool("claudingCollapsed"))
	sidebar.ShowAutoJump = Flag("autoJump")
	s := spinner.New()
	s.Spinner = claudeSpinner
	bin, _ := os.Executable()
	m := Model{
		client:            client,
		sidebar:           sidebar,
		detail:            ui.NewDetailModel(),
		search:            ui.NewSearchModel(),
		relay:             ui.NewRelayModel(),
		queueRelay:        ui.NewQueueRelayModel(),
		tagRelay:          ui.NewTagRelayModel(),
		laterRelay:        ui.NewLaterRelayModel(),
		palette:           ui.NewPaletteModel(),
		commands:          buildCommands(),
		minimap:           ui.NewMinimapModel(),
		promptEditor:      ui.NewPromptEditorModel(),
		pathInput:         ui.NewPathRelayModel(),
		macroEditor:       ui.NewMacroEditorModel(),
		copilot:           ui.NewCopilotModel(),
		copilotInput:      ui.NewCopilotRelayModel(),
		copilotVisible:    loadPrefBool("copilotVisible"),
		copilotMode:       loadPrefString("copilotMode", CopilotModeFloat),
		copilotDockedW:    loadPrefInt("copilotDockedW", defaultCopilotDockedW),
		copilotOffX:       loadPrefInt("copilotOffX", 0),
		copilotOffY:       loadPrefInt("copilotOffY", 0),
		copilotDW:         loadPrefInt("copilotDW", 0),
		copilotDH:         loadPrefInt("copilotDH", 0),
		macros:            claude.LoadMacros(nil),
		chatOutlineMode:       loadPrefString("chatOutlineMode", ChatOutlineOverlay),
		showMinimap:       loadPrefBool("minimap"),
		minimapMode:       loadPrefString("minimapMode", MinimapAuto),
		minimapMaxH:       loadPrefInt("minimapMaxH", defaultMinimapMaxH),
		minimapCollapse:   loadPrefBool("minimapCollapse"),
		sidebarWidthPct:   loadPrefInt("sidebarWidthPct", 30),
		spinner:           s,
		inFullscreenPopup: os.Getenv("CLAUDE_TUI_FULLSCREEN") == "1",
		selectActive:      os.Getenv("CMC_SELECT_ACTIVE") == "1",
		rotateNext:        os.Getenv("CMC_ROTATE_NEXT") == "1",
		binaryPath:        bin,
		messageLog:        loadMessageLog(),
	}
	ensureSettingDefaults()
	m.detail.SetChatOutlineMode(m.chatOutlineMode)
	if w := loadPrefInt("chatOutlineWidth", 0); w > 0 {
		m.detail.SetChatOutlineWidth(w)
	}
	// Restore copilot input to visible-but-unfocused on TUI reopen.
	// Normally Activate() fires inside execOpenCopilot/execToggleCopilot, but
	// those never run on restart — the pref already has copilotVisible=true.
	// We can't call unfocusCopilot() here because it also sets m.state=StateNormal,
	// which is wrong during init (state hasn't been set yet).
	if m.copilotVisible {
		m.copilotInput.Activate()
		m.copilotInput.TextInput().Blur()
		m.copilotInput.SetPromptStyle(ui.CopilotPromptDimStyle)
	}
	return m
}

// syncAllQuietAnim starts or stops the mobile animation based on sidebar state.
func (m *Model) syncAllQuietAnim() tea.Cmd {
	if m.sidebar.IsAllQuiet() && !m.detail.AllQuietAnimActive() {
		animCmd := m.detail.StartAllQuietAnim()
		// Schedule auto-destroyer after pendulums swing a while
		return tea.Batch(animCmd, scheduleDestroyerAutoStart())
	}
	if !m.sidebar.IsAllQuiet() && m.detail.AllQuietAnimActive() {
		m.detail.StopAllQuietAnim()
		// Cancel destroyer if sessions became active again
		if m.state == StateDestroyer {
			m.destroyer = nil
			m.state = StateNormal
		}
	}
	return nil
}

// toast enqueues a message for display in the toast overlay and schedules its removal.
func (m *Model) toast(text string, isError bool) tea.Cmd {
	m.toastQueue = append(m.toastQueue, MessageLogEntry{
		Text:    text,
		IsError: isError,
		Time:    time.Now(),
	})
	return tea.Tick(messageToastTTL, func(time.Time) tea.Msg { return ClearToastMsg{} })
}

// appendMessageLog appends an entry to the message log, trims to maxMessageLog, and persists.
func (m *Model) appendMessageLog(text string, isError bool) {
	m.messageLog = append(m.messageLog, MessageLogEntry{
		Text:    text,
		IsError: isError,
		Time:    time.Now(),
	})
	if len(m.messageLog) > maxMessageLog {
		m.messageLog = m.messageLog[len(m.messageLog)-maxMessageLog:]
	}
	saveMessageLog(m.messageLog)
}

// setFlash records a flash message and writes it to the footer flash bar.
// When the flash bar is occupied by a transient state (StateMinimapSettings),
// it routes to the toast overlay instead.
func (m *Model) setFlash(text string, isError bool, ttl time.Duration) tea.Cmd {
	m.appendMessageLog(text, isError)

	if m.state == StateMinimapSettings {
		return m.toast(text, isError)
	}
	m.flashMsg = text
	m.flashIsError = isError
	m.flashExpiry = time.Now().Add(ttl)
	return tea.Tick(ttl, func(time.Time) tea.Msg { return ClearFlashMsg{} })
}

// innerWidth returns the usable content width (total width minus side borders when not fullscreen).
func (m Model) innerWidth() int {
	w := m.width
	if !m.inFullscreenPopup {
		w -= 2
	}
	return w
}

// sidebarPanelWidth returns the computed sidebar panel width from the current layout.
func (m Model) sidebarPanelWidth() int {
	return max(m.innerWidth()*m.sidebarWidthPct/100, 20)
}

// shouldDockMinimap returns true when the minimap should be docked at the bottom
// (reducing content height) rather than overlaid on top of the content.
func (m Model) shouldDockMinimap() bool {
	if !m.showMinimap {
		return false
	}
	switch m.minimapMode {
	case MinimapDocked:
		return true
	case MinimapFloat:
		return false
	case MinimapSmart:
		mmW, _ := m.minimap.ViewSize()
		return mmW > 0 && mmW > m.sidebarPanelWidth()
	default: // MinimapAuto
		return m.inFullscreenPopup
	}
}

// Copilot float overlay geometry constants — shared between view.go and update_mouse.go.
const (
	copilotFloatMaxW = 70 // max overlay width in columns
	copilotFloatMinH = 5  // min overlay height (title + 1 msg + input + border)
	copilotFloatMargR = 2  // right margin from content edge
	copilotFloatMargT = 1  // top margin from content edge
)

// copilotFloatGeometry computes the float overlay's position and size from raw layout dimensions.
// Returns (row, col, overlayW, maxOverlayH) all in content-area-local coordinates.
// Note: row uses maxOverlayH for clamping; view.go refines with actual rendered height.
func (m Model) copilotFloatGeometry(innerWidth, contentHeight int) (row, col, overlayW, maxOverlayH int) {
	overlayW = min(copilotFloatMaxW+m.copilotDW, innerWidth-4)
	overlayW = max(overlayW, 20)
	maxOverlayH = min(max(contentHeight-2*copilotFloatMargT+m.copilotDH, copilotFloatMinH), contentHeight-2)

	row = copilotFloatMargT + m.copilotOffY
	row = max(row, 0)
	row = min(row, contentHeight-maxOverlayH)
	col = innerWidth - overlayW - copilotFloatMargR + m.copilotOffX
	col = max(col, 0)
	col = min(col, innerWidth-overlayW)
	return
}

// copilotDockedWidth returns the copilot panel width when docked and visible, or 0.
func (m Model) copilotDockedWidth() int {
	if !m.copilotVisible || m.copilotMode != CopilotModeDocked {
		return 0
	}
	return min(m.copilotDockedW, m.innerWidth()/2)
}

// applyLayout recomputes and applies component sizes from m.width, m.height, m.sidebarWidthPct.
func (m *Model) applyLayout() {
	innerW := m.innerWidth()
	contentHeight := m.contentHeight()
	minimapH := min(contentHeight/2, m.minimapMaxH)
	m.minimap.SetSize(0, minimapH)
	// Scale window cols proportionally to height, preserving the default 40:14 aspect ratio
	m.minimap.SetWindowCols(m.minimapMaxH * ui.DefaultMinimapWindowCols / defaultMinimapMaxH)
	m.minimap.SetCollapse(m.minimapCollapse)

	// When docked, subtract minimap height so panels shrink to make room
	contentHeight = m.panelContentHeight(contentHeight)

	sidebarWidth := m.sidebarPanelWidth()
	copilotW := m.copilotDockedWidth()
	m.sidebar.SetSize(sidebarWidth-1, contentHeight)
	m.detail.SetSize(innerW-sidebarWidth-copilotW, contentHeight)
}

// applyLayoutFast recomputes component sizes like applyLayout but skips
// expensive viewport content reflow. Use during drag; call applyLayout on release.
func (m *Model) applyLayoutFast() {
	innerW := m.innerWidth()
	contentHeight := m.panelContentHeight(m.contentHeight())
	sidebarWidth := m.sidebarPanelWidth()
	copilotW := m.copilotDockedWidth()
	m.sidebar.SetSize(sidebarWidth-1, contentHeight)
	m.detail.SetSizeFast(innerW-sidebarWidth-copilotW, contentHeight)
}

// contentHeight returns the raw content height before minimap/divider adjustments.
func (m Model) contentHeight() int {
	h := m.height - 3 // top border + label + footer
	if !m.inFullscreenPopup {
		h-- // bottom border
	}
	return h
}

// panelContentHeight adjusts the raw content height for minimap docking and divider.
func (m Model) panelContentHeight(raw int) int {
	minimapDocked := false
	if m.shouldDockMinimap() {
		if _, mmViewH := m.minimap.ViewSize(); mmViewH > 0 {
			raw -= mmViewH
			minimapDocked = true
		}
	}
	// Divider line between content and footer (shown when minimap isn't docked above footer)
	if !minimapDocked {
		raw--
	}
	return raw
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.subscribeToDaemon(),
		m.spinner.Tick,
		captureOriginalPane(),
		m.fetchCopilotHistory(),
	)
}

func (m Model) fetchCopilotHistory() tea.Cmd {
	return func() tea.Msg {
		msgs, _ := m.client.CopilotHistory()
		return CopilotHistoryReadyMsg{Messages: msgs}
	}
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

// waitForDaemonUpdate blocks until the daemon pushes the next message on the
// subscribe connection. Dispatches to the appropriate Msg type based on response type
// (session snapshots vs copilot stream events).
func (m Model) waitForDaemonUpdate() tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.ReadNextResponse()
		if err != nil {
			return DaemonDisconnectedMsg{Err: err}
		}
		switch resp.Type {
		case daemon.RespCopilotStream:
			var data daemon.CopilotStreamData
			json.Unmarshal(resp.Data, &data)
			return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
				Type:    data.Type,
				Content: data.Content,
				ToolID:  data.ToolID,
				Status:  data.Status,
				Kind:    data.Kind,
			}}
		default:
			// RespSessions or unknown — treat as session update
			var data daemon.SessionsData
			json.Unmarshal(resp.Data, &data)
			return SessionsRefreshedMsg{Sessions: data.Sessions, Usage: data.Usage}
		}
	}
}

// reconnectToDaemon attempts to reconnect to the daemon with exponential backoff.
func reconnectToDaemon() tea.Cmd {
	return func() tea.Msg {
		for attempt := range 10 {
			time.Sleep(time.Duration(500*(1<<min(attempt, 4))) * time.Millisecond) // 500ms..8s
			client, err := daemon.Connect()
			if err == nil {
				return DaemonReconnectedMsg{Client: client}
			}
		}
		return DaemonDisconnectedMsg{Err: fmt.Errorf("reconnect failed after 10 attempts")}
	}
}

func capturePreview(paneID string) tea.Cmd {
	return func() tea.Msg {
		content, err := tmux.CapturePaneContent(paneID)
		return PreviewReadyMsg{PaneID: paneID, Content: content, Err: err}
	}
}

func (m Model) fetchChatOutline(paneID, sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		msgs, _ := m.client.Transcript(sessionID)
		return ChatOutlineReadyMsg{PaneID: paneID, Messages: msgs}
	}
}

func (m Model) fetchRawTranscript(paneID, sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		entries, _ := m.client.TranscriptEntries(sessionID)
		return RawTranscriptReadyMsg{PaneID: paneID, Entries: entries}
	}
}

func (m Model) fetchGlobalEffects() tea.Cmd {
	return func() tea.Msg {
		effects, _ := m.client.AllHookEffects()
		return GlobalEffectsReadyMsg{Effects: effects}
	}
}

func (m Model) fetchHooks(paneID, sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		events, _ := m.client.HookEvents(sessionID)
		return HooksReadyMsg{PaneID: paneID, Events: events}
	}
}

// fetchVisibleOverlays returns commands to refresh any active overlay (hooks, raw transcript).
func (m Model) fetchVisibleOverlays(paneID, sessionID, cwd string) []tea.Cmd {
	var cmds []tea.Cmd
	if m.showHooks {
		cmds = append(cmds, m.fetchHooks(paneID, sessionID))
	}
	if m.showRawTranscript {
		cmds = append(cmds, m.fetchRawTranscript(paneID, sessionID))
	}
	if m.showDiffs {
		cmds = append(cmds, m.fetchDiffHunks(paneID, sessionID, cwd))
	}
	if m.debugMode {
		cmds = append(cmds, m.fetchGlobalEffects())
	}
	return cmds
}

// fetchForSelection builds the standard cmd batch when the selected session changes:
// preview capture, transcript, diff stats, summary, tmux pane switch, and active overlays.
// If syncMinimap is true, also refreshes the minimap to track the new selection.
func (m *Model) fetchForSelection(s claude.ClaudeSession, syncMinimap bool) []tea.Cmd {
	m.nonClaudePane = nil // clear non-Claude focus when selecting a Claude session
	m.detail.SetNote(s.Note)
	cmds := []tea.Cmd{
		capturePreview(s.PaneID),
		m.fetchChatOutline(s.PaneID, s.SessionID),
		m.fetchDiffStats(s.PaneID, s.SessionID),
		m.fetchCachedSummary(s.PaneID, s.SessionID),
		switchPaneQuiet(s.TmuxSession, s.TmuxWindow, s.TmuxPane),
	}
	cmds = append(cmds, m.fetchVisibleOverlays(s.PaneID, s.SessionID, s.CWD)...)
	if syncMinimap && m.showMinimap {
		if s.TmuxSession != m.minimapSession {
			cmds = append(cmds, m.fetchMinimapData(s.TmuxSession))
		} else {
			m.minimap.UpdateSelected(s.PaneID)
		}
	}
	return cmds
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

func (m Model) fetchDiffHunks(paneID, sessionID, cwd string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		hunks, _ := m.client.DiffHunks(sessionID)
		return DiffHunksReadyMsg{PaneID: paneID, CWD: cwd, Hunks: hunks}
	}
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

func (m Model) fetchApplyTitle(paneID, sessionID string) tea.Cmd {
	return func() tea.Msg {
		err := m.client.ApplyTitle(paneID, sessionID)
		return ApplyTitleReadyMsg{Err: err}
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

// sendBangKey sends "!" as an interactive keystroke (no -l, no Enter)
// to trigger Claude's bash mode switch.
func sendBangKey(paneID string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.SendKeys(paneID, "!"); err != nil {
			return flashErrorMsg("send failed: " + err.Error())
		}
		return nil
	}
}

const maxJumpTrail = 100

// recordJump saves the currently selected paneID to the jump trail.
// Call before any programmatic jump (gg, G, spatial nav, clicks, autoJump, etc.).
func (m *Model) recordJump() {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return
	}
	paneID := s.PaneID

	// If navigating history, truncate forward entries
	if m.jumpCursor < len(m.jumpTrail) {
		m.jumpTrail = m.jumpTrail[:m.jumpCursor]
	}

	// Deduplicate consecutive entries
	if len(m.jumpTrail) > 0 && m.jumpTrail[len(m.jumpTrail)-1] == paneID {
		return
	}

	m.jumpTrail = append(m.jumpTrail, paneID)
	if len(m.jumpTrail) > maxJumpTrail {
		m.jumpTrail = m.jumpTrail[len(m.jumpTrail)-maxJumpTrail:]
	}
	m.jumpCursor = len(m.jumpTrail)
}

// jumpBack navigates to the previous entry in the jump trail ([).
// Returns the target paneID, or "" if there's nowhere to go.
func (m *Model) jumpBack() string {
	if len(m.jumpTrail) == 0 {
		return ""
	}
	// First time going back: save current position at the end
	if m.jumpCursor >= len(m.jumpTrail) {
		if s, ok := m.sidebar.SelectedItem(); ok {
			current := s.PaneID
			if len(m.jumpTrail) == 0 || m.jumpTrail[len(m.jumpTrail)-1] != current {
				m.jumpTrail = append(m.jumpTrail, current)
			}
			m.jumpCursor = len(m.jumpTrail) - 1
		}
	}
	if m.jumpCursor <= 0 {
		return ""
	}
	m.jumpCursor--
	return m.jumpTrail[m.jumpCursor]
}

// jumpForward navigates to the next entry in the jump trail (]).
// Returns the target paneID, or "" if already at head.
func (m *Model) jumpForward() string {
	if m.jumpCursor >= len(m.jumpTrail)-1 {
		m.jumpCursor = len(m.jumpTrail)
		return ""
	}
	m.jumpCursor++
	return m.jumpTrail[m.jumpCursor]
}

// discoverBacklogs scans unique CWDs from sessions for .cmc/backlog/ directories.
func (m Model) discoverBacklogs(sessions []claude.ClaudeSession) tea.Cmd {
	if len(sessions) == 0 {
		return nil
	}
	return func() tea.Msg {
		return BacklogsRefreshedMsg{Backlogs: claude.DiscoverBacklogs(sessions)}
	}
}

func (m Model) fetchRenameWindow(sessionName string, windowIndex int) tea.Cmd {
	return func() tea.Msg {
		name, err := m.client.RenameWindow(sessionName, windowIndex)
		return WindowRenameMsg{Name: name, Err: err}
	}
}
