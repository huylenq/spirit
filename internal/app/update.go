package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/tmux"
	"github.com/huylenq/spirit/internal/ui"
)

// executeChord dispatches a completed chord sequence to its action.
func (m Model) executeChord(chord Chord) (tea.Model, tea.Cmd) {
	return chord.Execute(&m)
}

// openTranscriptInEditor opens the transcript JSONL in $EDITOR in a new tmux window.
func openTranscriptInEditor(tmuxSession, sessionID string) tea.Cmd {
	return func() tea.Msg {
		path, err := claude.TranscriptPath(sessionID)
		if err != nil {
			return flashErrorMsg("transcript not found")
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vim"
		}
		cmd := fmt.Sprintf("%s %s", editor, path)
		if err := exec.Command("tmux", "new-window", "-t", tmuxSession, cmd).Run(); err != nil {
			return flashErrorMsg("open editor: " + err.Error())
		}
		return tea.QuitMsg{}
	}
}

// copyToClipboard copies text to the system clipboard via pbcopy and shows a flash.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return flashErrorMsg("copy failed: " + err.Error())
		}
		// Show truncated preview for short strings, generic message for long ones
		if len(text) < 100 {
			return flashInfoMsg("copied " + text)
		}
		return flashInfoMsg(fmt.Sprintf("captured %d chars", len(text)))
	}
}

// sessionDisplayTitle returns the effective display title for a session,
// matching the sidebar panel's priority: custom title → synthesized title → first message → "New session".
func sessionDisplayTitle(s claude.ClaudeSession) string {
	title := s.DisplayName()
	if title == "" {
		title = "New session"
	} else {
		title = strings.ReplaceAll(title, "\n", " ")
	}
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:79]) + "…"
	}
	return title
}

// killPaneCmd sends SIGTERM to the claude process, kills the tmux pane, and cleans up status files.
func killPaneCmd(paneID, sessionID string, pid int, laterID string) tea.Cmd {
	return func() tea.Msg {
		if pid > 0 {
			syscall.Kill(pid, syscall.SIGTERM) //nolint:errcheck
		}
		tmux.KillPane(paneID) //nolint:errcheck
		if sessionID != "" {
			claude.RemoveSessionFiles(sessionID)
		}
		claude.RemovePaneMapping(paneID)
		if laterID != "" {
			claude.RemoveLaterRecord(laterID)
		}
		return PaneKilledMsg{}
	}
}

type flashInfoMsg string
type flashErrorMsg string

// reopenPopup schedules a new tmux popup to open after the current one closes.
// It persists the new fullscreen state to prefs so `spirit popup` picks it up,
// then uses run-shell with a short sleep so the new popup opens after the old one exits.
func reopenPopup(bin string, currentlyFullscreen bool) tea.Cmd {
	// Persist the toggled state so future `spirit popup` invocations use it
	savePrefBool("fullscreen", !currentlyFullscreen)
	return func() tea.Msg {
		if bin == "" || os.Getenv("TMUX") == "" {
			return tea.QuitMsg{}
		}
		// Escape any single quotes in the path (POSIX: replace ' with '\'')
		escaped := strings.ReplaceAll(bin, "'", `'\''`)
		var shellCmd string
		if currentlyFullscreen {
			shellCmd = fmt.Sprintf("sleep 0.2 && tmux display-popup -B -E -w 80%% -h 70%% '%s'", escaped)
		} else {
			shellCmd = fmt.Sprintf("sleep 0.2 && tmux display-popup -B -E -w 100%% -h 100%% -e CLAUDE_TUI_FULLSCREEN=1 '%s'", escaped)
		}
		exec.Command("tmux", "run-shell", shellCmd).Start() //nolint:errcheck
		return tea.QuitMsg{}
	}
}

// autoJump selects the user-turn session with the oldest LastChanged
// (waiting longest), skipping Later and skipPaneID.
// Returns nil if autoJump is disabled. Returns cmds from fetchForSelection otherwise.
func (m *Model) autoJump(skipPaneID string) []tea.Cmd {
	if !m.autoJumpOn {
		return nil
	}
	targetID := m.sidebar.AutoJumpTarget(skipPaneID)
	if targetID == "" {
		return nil
	}
	m.recordJump()
	if !m.selectByPaneID(targetID) {
		return nil
	}
	m.recordJump() // register destination so ] can reach it
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return nil
	}
	m.sidebar.SetTrail(skipPaneID)
	m.sidebar.SetLand(targetID, ui.JumpAnimFrames)
	return m.fetchForSelection(s, true)
}

// doJump moves selection to target with trail/landing animation.
// Returns (model, nil) if target is empty or not found.
func (m Model) doJump(target string) (tea.Model, tea.Cmd) {
	if target == "" {
		return m, nil
	}
	var prevPaneID string
	if cur, ok := m.sidebar.SelectedItem(); ok {
		prevPaneID = cur.PaneID
	}
	if !m.selectByPaneID(target) {
		return m, nil
	}
	m.sidebar.SetTrail(prevPaneID)
	m.sidebar.SetLand(target, ui.JumpAnimFrames)
	if s, ok := m.sidebar.SelectedItem(); ok {
		return m, tea.Batch(m.fetchForSelection(s, true)...)
	}
	return m, nil
}

// tryInitialSelection auto-selects a pane on launch.
//
// Two modes controlled by env vars:
//   - selectActive (ctrl-space): select the originating pane's session
//   - rotateNext   (ctrl-tab):   skip originating pane, rotate to next user-turn
//
// Fallback chain (both modes):
//  1. Mode-specific selection (see above)
//  2. Same tmux session: first Claude session in the same tmux session (in sort order)
//  3. Default: cursor stays at 0 (first in sort order across all sessions)
//
// Only runs once, when both sessions and origPane are available.
// Returns true if the cursor was moved (caller should fetch preview).
func (m *Model) tryInitialSelection() bool {
	if !m.selectActive && !m.rotateNext {
		return false
	}
	if m.initialSelectionDone || len(m.sessions) == 0 {
		return false
	}
	if !m.origPane.Captured {
		return false
	}
	m.initialSelectionDone = true
	m.recordJump() // record pre-selection position (cursor 0)

	var moved bool
	var targetPaneID string
	if m.rotateNext {
		// Skip originating pane so ctrl+tab always rotates to a different session
		if tid := m.sidebar.AutoJumpTarget(m.origPane.PaneID); tid != "" {
			if m.selectByPaneID(tid) {
				moved = true
				targetPaneID = tid
			}
		}
	} else {
		// ctrl-space: exact match on originating pane (any status)
		for _, s := range m.sessions {
			if s.PaneID == m.origPane.PaneID {
				if m.selectByPaneID(m.origPane.PaneID) {
					moved = true
					targetPaneID = m.origPane.PaneID
				}
				break
			}
		}
		// Fallback: first non-Later session in same tmux session (already sorted)
		if !moved {
			items := m.sidebar.Items()
			for _, s := range items {
				if s.TmuxSession == m.origPane.Session && s.LaterID == "" {
					if m.selectByPaneID(s.PaneID) {
						moved = true
						targetPaneID = s.PaneID
					}
					break
				}
			}
		}
	}
	if moved {
		m.recordJump() // register destination so ] can reach it
		// Activation flash animations
		m.sidebar.SetLand(targetPaneID, ui.ActivateAnimFrames)
		if m.rotateNext && m.origPane.PaneID != targetPaneID {
			// Ctrl+Tab: fading ghost trail on the origin pane
			m.sidebar.SetTrail(m.origPane.PaneID)
		}
	}
	return moved
}

// claudeStatusToPane converts claude.Status to ui.PaneStatus* constant.
func claudeStatusToPane(s claude.Status) int {
	switch s {
	case claude.StatusAgentTurn:
		return ui.PaneStatusAgentTurn
	case claude.StatusUserTurn:
		return ui.PaneStatusUserTurn
	default:
		return ui.PaneStatusNone
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.applyLayout()
		return m, nil

	case DaemonDisconnectedMsg:
		m.err = msg.Err
		if m.client != nil {
			m.client.Close()
		}
		flashCmd := m.setFlash("daemon disconnected, reconnecting...", true, 30*time.Second)
		return m, tea.Batch(flashCmd, reconnectToDaemon())

	case DaemonReconnectedMsg:
		m.err = nil
		m.client = msg.Client
		flashCmd := m.setFlash("reconnected", false, 2*time.Second)
		return m, tea.Batch(m.subscribeToDaemon(), flashCmd)

	case MacroEditorExitedMsg:
		m.macros = claude.LoadMacros(nil)
		return m, nil

	case LuaEvalDoneMsg:
		var cmds []tea.Cmd
		for _, f := range msg.Msgs.Flashes {
			cmds = append(cmds, m.setFlash(f, false, 8*time.Second))
		}
		for _, t := range msg.Msgs.Toasts {
			cmds = append(cmds, m.toast(t, false))
		}
		if msg.Err != nil {
			cmds = append(cmds, m.setFlash("lua: "+msg.Err.Error(), true, 10*time.Second))
		} else {
			result := msg.Result
			if result == "" {
				result = "ok"
			}
			cmds = append(cmds, m.setFlash("lua: "+result, false, 10*time.Second))
		}
		return m, tea.Batch(cmds...)

	case CopilotHistoryReadyMsg:
		uiMsgs := make([]ui.CopilotMessage, len(msg.Messages))
		for i, h := range msg.Messages {
			uiMsgs[i] = ui.CopilotMessage{Role: h.Role, Content: h.Content, Time: h.Time}
		}
		m.copilot.LoadHistory(uiMsgs)
		return m, nil

	case CopilotStreamChunkMsg:
		m.copilot.HandleStreamMsg(msg.Msg)
		// Auto-pop copilot on stream completion if hidden
		if (msg.Msg.Type == "done" || msg.Msg.Type == "error") && !m.copilotVisible {
			m.setCopilotVisible(true) // handles applyLayout for docked mode
		}
		// Tool confirmation handling
		if msg.Msg.Type == "confirm" {
			if m.state == StateCopilot {
				// Already focused: transition to confirm state
				m.state = StateCopilotConfirm
			}
			// Unfocused (float or docked): don't steal focus, pending tool renders inline.
			// User must tab to focus, which will detect the pending tool.
		}
		// Re-invoke subscribe loop to receive the next event
		return m, m.waitForDaemonUpdate()

	case ui.UsageBarTickMsg:
		cmd := m.usageBar.Tick()
		return m, cmd

	case ui.AllQuietTickMsg:
		cmd := m.detail.TickAllQuiet()
		return m, cmd

	case DestroyerAutoStartMsg:
		// Auto-start destroyer if still in AllQuiet state (pendulums visible)
		if m.sidebar.IsAllQuiet() && m.state == StateNormal && m.destroyer == nil {
			nm, cmd := m.execDestroyer()
			return nm, cmd
		}
		return m, nil

	case DestroyerTickMsg:
		if m.state == StateDestroyer && m.destroyer != nil {
			m.destroyer.Tick()
			if m.destroyer.IsRebuilt() {
				m.destroyer = nil
				m.state = StateNormal
				return m, nil
			}
			return m, tickDestroyer()
		}
		return m, nil

	case SessionsRefreshedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		// Detect auto-synthesis completions: SynthesizePending was true, now false
		prevSynth := make(map[string]bool, len(m.sessions))
		for _, s := range m.sessions {
			if s.SynthesizePending {
				prevSynth[s.PaneID] = true
			}
		}
		var autoSynthCmds []tea.Cmd
		for _, s := range msg.Sessions {
			if prevSynth[s.PaneID] && !s.SynthesizePending && s.SynthesizedTitle != "" {
				title := s.SynthesizedTitle
				if runes := []rune(title); len(runes) > 60 {
					title = string(runes[:59]) + "…"
				}
				text := "synthesized: " + title
				m.appendMessageLog(text, false)
				autoSynthCmds = append(autoSynthCmds, m.toast(text, false))
			}
		}
		m.sessions = msg.Sessions
		m.refreshSessions()
		m.tryInitialSelection()
		if !m.initialSelectionDone && !m.selectActive && !m.rotateNext && len(m.sessions) > 0 {
			m.initialSelectionDone = true
		}
		// Auto-select newly created session once it appears
		if m.pendingSelectPaneID != "" {
			m.sidebar.EnterSessionLevel() // switch from project → session level
			if m.selectByPaneID(m.pendingSelectPaneID) {
				m.pendingSelectPaneID = ""
			}
		}
		var cmds []tea.Cmd
		// Update usage bar
		if msg.Usage != nil {
			if cmd := m.usageBar.SetUsage(msg.Usage); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// Update minimap status flags
		if m.showMinimap {
			paneStatuses := make(map[string]int)
			for _, s := range msg.Sessions {
				if s.LaterID != "" {
					paneStatuses[s.PaneID] = ui.PaneStatusLater
				} else {
					paneStatuses[s.PaneID] = claudeStatusToPane(s.Status)
				}
			}
			m.minimap.UpdateStatus(paneStatuses)
		}
		// Capture preview + transcript + diff stats for selected item; diff stats for all sessions
		cmds = append(cmds, m.fetchAllDiffStats(msg.Sessions))
		if s, ok := m.sidebar.SelectedItem(); ok {
			m.detail.SetNote(s.Note)
			cmds = append(cmds, capturePreview(s.PaneID), m.fetchChatOutline(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
			if m.showMinimap && m.minimapSession == "" {
				cmds = append(cmds, m.fetchMinimapData(s.TmuxSession))
			}
		} else if m.nonClaudePane != nil {
			cmds = append(cmds, capturePreview(m.nonClaudePane.PaneID))
		}
		// Always discover backlog items so the collapsed badge count stays accurate
		cmds = append(cmds, m.discoverBacklogs(msg.Sessions))
		// Start/stop all-quiet animation based on sidebar state
		if cmd := m.syncAllQuietAnim(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Auto-synthesis completion toasts
		cmds = append(cmds, autoSynthCmds...)
		// Wait for next daemon push
		cmds = append(cmds, m.waitForDaemonUpdate())
		return m, tea.Batch(cmds...)

	case backlogWrittenMsg:
		cmds := []tea.Cmd{m.discoverBacklogs(m.sessions)}
		if msg.flash != "" {
			cmds = append(cmds, m.setFlash(msg.flash, false, 2*time.Second))
		}
		return m, tea.Batch(cmds...)

	case BacklogsRefreshedMsg:
		m.sidebar.SetBacklog(msg.Backlogs)
		return m, m.syncAllQuietAnim()

	case PreviewReadyMsg:
		if msg.Err != nil {
			return m, nil
		}
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.detail.SetSession(&s, msg.Content)
		} else if m.nonClaudePane != nil && m.nonClaudePane.PaneID == msg.PaneID {
			m.detail.SetNonClaudePane(msg.PaneID, m.nonClaudePane.PaneTitle, msg.Content)
		}
		return m, nil

	case ChatOutlineReadyMsg:
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.detail.SetUserMessages(msg.Messages)
		}
		return m, nil

	case DiffStatsReadyMsg:
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.detail.SetDiffStats(msg.Stats)
		}
		m.sidebar.SetDiffStats(msg.SessionID, msg.Stats)
		return m, nil

	case HooksReadyMsg:
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.detail.SetHookEvents(msg.Events)
		}
		return m, nil

	case GlobalEffectsReadyMsg:
		m.globalEffects = msg.Effects
		return m, nil

	case DiffHunksReadyMsg:
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.detail.SetDiffHunks(msg.Hunks, msg.CWD)
		}
		return m, nil

	case RawTranscriptReadyMsg:
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.detail.SetTranscriptEntries(msg.Entries)
		}
		return m, nil

	case SummaryReadyMsg:
		if msg.Err != nil {
			return m, m.setFlash("Synthesize failed: "+msg.Err.Error(), true, 5*time.Second)
		}
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.detail.SetSummary(msg.Summary)
		}
		// Update in-memory synthesized titles + problem type immediately so the list reflects it
		if msg.Summary != nil {
			for i := range m.sessions {
				if m.sessions[i].PaneID == msg.PaneID {
					m.sessions[i].SynthesizedTitle = msg.Summary.SynthesizedTitle
					if msg.Summary.ProblemType != "" {
						m.sessions[i].ProblemType = msg.Summary.ProblemType
					}
					break
				}
			}
			m.refreshSessions()
		}
		if msg.FromCache && msg.UserRequested {
			return m, m.setFlash("summary unchanged (cached)", false, 2*time.Second)
		}
		return m, nil

	case SynthesizeAllReadyMsg:
		if msg.Err != nil {
			return m, m.setFlash("Synthesize all failed: "+msg.Err.Error(), true, 5*time.Second)
		}
		updated := false
		for _, r := range msg.Results {
			if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == r.PaneID {
				m.detail.SetSummary(r.Summary)
			}
			if r.Summary != nil {
				for i := range m.sessions {
					if m.sessions[i].PaneID == r.PaneID {
						m.sessions[i].SynthesizedTitle = r.Summary.SynthesizedTitle
						if r.Summary.ProblemType != "" {
							m.sessions[i].ProblemType = r.Summary.ProblemType
						}
						updated = true
						break
					}
				}
			}
		}
		if updated {
			m.refreshSessions()
		}
		return m, nil

	case ApplyTitleReadyMsg:
		if msg.Err != nil {
			return m, m.setFlash("Apply title failed: "+msg.Err.Error(), true, 5*time.Second)
		}
		return m, nil

	case flashInfoMsg:
		return m, m.setFlash(string(msg), false, 2*time.Second)

	case flashErrorMsg:
		return m, m.setFlash(string(msg), true, 5*time.Second)

	case ClearToastMsg:
		if len(m.toastQueue) > 0 {
			m.toastQueue = m.toastQueue[1:]
		}
		return m, nil

	case ClearFlashMsg:
		if !m.flashExpiry.IsZero() && !time.Now().Before(m.flashExpiry) {
			// Don't auto-dismiss minimap settings state — it requires explicit
			// exit (Esc or unrecognized key) to prevent accidental commands when
			// the transient state escapes without the user noticing.
			if m.state == StateMinimapSettings {
				return m, nil
			}
			m.flashMsg = ""
			m.flashIsError = false
			m.flashExpiry = time.Time{}
		}
		return m, nil

	case WindowRenameMsg:
		m.renaming = false
		if msg.Err != nil {
			return m, m.setFlash("Rename failed: "+msg.Err.Error(), true, 5*time.Second)
		}
		return m, m.setFlash("renamed → "+msg.Name, false, 3*time.Second)

	case pathValidatedMsg:
		m.newSessionCWD = msg.cwd
		m.newSessionProject = msg.project
		m.state = StateNewSessionPrompt
		m.promptEditor.Activate()
		return m, nil

	case NewSessionCreatedMsg:
		if m.newSessionWasSession {
			// Invoked from session level — stay on the original session
			m.sidebar.EnterSessionLevel()
			m.selectByPaneID(m.newSessionPrevPaneID)
		} else {
			// Invoked from project level — auto-jump to the new session
			m.pendingSelectPaneID = msg.PaneID
		}
		m.newSessionWasSession = false
		return m, m.setFlash("new session created", false, 2*time.Second)

	case PaneKilledMsg:
		title := m.killTargetTitle
		m.state = StateNormal
		m.killTargetPaneID = ""
		m.killTargetSessionID = ""
		m.killTargetPID = 0
		m.killTargetTitle = ""
		m.killTargetAnimalIdx = 0
		m.killTargetColorIdx = 0
		m.killTargetLaterID = ""
		if msg.Err != nil {
			return m, m.setFlash("kill failed: "+msg.Err.Error(), true, 5*time.Second)
		}
		return m, m.setFlash("killed "+title, false, 2*time.Second)

	case OriginalPaneCapturedMsg:
		if msg.Err == nil {
			m.origPane = originalPane{
				Session: msg.Session, Window: msg.Window, Pane: msg.Pane,
				PaneID: msg.PaneID, Captured: true,
			}
			if m.tryInitialSelection() {
				if s, ok := m.sidebar.SelectedItem(); ok {
					return m, tea.Batch(
						capturePreview(s.PaneID),
						m.fetchChatOutline(s.PaneID, s.SessionID),
						m.fetchCachedSummary(s.PaneID, s.SessionID),
					)
				}
			}
		}
		return m, nil

	case MinimapReadyMsg:
		paneStatuses := make(map[string]int)
		paneAvatars := make(map[string]ui.PaneAvatarInfo)
		for _, s := range m.sessions {
			if s.LaterID != "" {
				paneStatuses[s.PaneID] = ui.PaneStatusLater
			} else {
				paneStatuses[s.PaneID] = claudeStatusToPane(s.Status)
			}
			paneAvatars[s.PaneID] = ui.PaneAvatarInfo{
				ColorIdx:  s.AvatarColorIdx,
				AnimalIdx: s.AvatarAnimalIdx,
			}
		}
		selectedPaneID := ""
		if s, ok := m.sidebar.SelectedItem(); ok {
			selectedPaneID = s.PaneID
		}
		m.minimap.SetData(msg.Panes, paneStatuses, paneAvatars, selectedPaneID, msg.SessionName)
		m.minimapSession = msg.SessionName
		m.applyLayout()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		frame := m.spinner.View()
		m.sidebar.SetSpinnerView(frame)
		m.minimap.SetSpinnerView(frame)
		m.copilot.TickSpinner()
		m.detail.TickPulse()
		return m, cmd

	case tea.MouseMsg:
		// Destroyer intercepts all mouse events
		if m.state == StateDestroyer && m.destroyer != nil {
			return m.handleMouseDestroyer(msg)
		}
		if m.showHelp || m.showSpiritAnimal {
			return m, nil
		}
		// Handle panel drags in progress (motion/release) before anything else.
		if m.outlineDragging {
			switch msg.Action {
			case tea.MouseActionMotion:
				return m.handleOutlineDragMotion(msg)
			case tea.MouseActionRelease:
				return m.handleOutlineDragRelease()
			}
			return m, nil
		}
		if m.sidebarDragging {
			switch msg.Action {
			case tea.MouseActionMotion:
				return m.handleSidebarDragMotion(msg)
			case tea.MouseActionRelease:
				return m.handleSidebarDragRelease()
			}
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			return m.handleMouseWheel(msg, -1)
		case tea.MouseButtonWheelDown:
			return m.handleMouseWheel(msg, 1)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress && m.state == StateNormal {
				return m.handleMouseClick(msg)
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// alt+' toggles copilot visibility from any state.
	if msg.String() == "alt+'" {
		return execToggleCopilot(&m)
	}

	// Shift+tab: switch copilot mode (float ↔ docked) from relevant states.
	if msg.String() == "shift+tab" {
		switch m.state {
		case StateNormal, StateCopilot, StateCopilotConfirm, StateAdjustCopilot:
			return execSwitchCopilotMode(&m)
		}
	}

	// Double-tab detection: hide copilot if two tabs within threshold.
	// Only track lastTabTime within copilot-relevant states to avoid false
	// double-taps when tab is pressed in unrelated states (search, palette, etc.).
	if msg.String() == "tab" {
		switch m.state {
		case StateNormal, StateCopilot, StateCopilotConfirm, StateAdjustCopilot:
			now := time.Now()
			if now.Sub(m.lastTabTime) < doubleTabThreshold {
				m.lastTabTime = time.Time{} // reset to avoid triple-tap
				if m.copilotVisible {
					return execHideCopilot(&m)
				}
				return execOpenCopilot(&m)
			}
			m.lastTabTime = now
		}
	}

	switch m.state {
	case StateSearching:
		return m.handleKeySearching(msg)
	case StateKillConfirm:
		return m.handleKeyKillConfirm(msg)
	case StateMinimapSettings:
		return m.handleKeyMinimapSettings(msg)
	case StatePrefsEditor:
		return m.handleKeySettings(msg)
	case StatePromptRelay:
		return m.handleKeyPromptRelay(msg)
	case StateQueueRelay:
		return m.handleKeyQueueRelay(msg)
	case StateTagRelay:
		return m.handleKeyTagRelay(msg)
	case StateLaterWait:
		return m.handleKeyLaterWait(msg)
	case StateNewSessionPathInput:
		return m.handleKeyNewSessionPath(msg)
	case StateNewSessionPrompt:
		return m.handleKeyNewSession(msg)
	case StateBacklogPrompt:
		return m.handleKeyBacklogPrompt(msg)
	case StateBacklogDeleteConfirm:
		return m.handleKeyBacklogDeleteConfirm(msg)
	case StatePalette:
		return m.handleKeyPalette(msg)
	case StateMacro:
		return m.handleKeyMacro(msg)
	case StateMacroEdit:
		return m.handleKeyMacroEdit(msg)
	case StateNoteEdit:
		return m.handleKeyNoteEdit(msg)
	case StateCopilot:
		return m.handleKeyCopilot(msg)
	case StateCopilotConfirm:
		return m.handleKeyCopilotConfirm(msg)
	case StateAdjustCopilot:
		return m.handleKeyAdjustCopilot(msg)
	case StateDestroyer:
		return m.handleKeyDestroyer(msg)
	default:
		return m.handleKeyNormal(msg)
	}
}
