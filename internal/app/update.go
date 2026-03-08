package app

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

// executeChord dispatches a completed chord sequence to its action.
func (m Model) executeChord(chord Chord) (tea.Model, tea.Cmd) {
	switch chord.Keys {
	case "ys":
		if s, ok := m.list.SelectedItem(); ok && s.SessionID != "" {
			return m, copyToClipboard(s.SessionID)
		}
	}
	return m, nil
}

// copyToClipboard copies text to the system clipboard via pbcopy and shows a flash.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return flashErrorMsg("copy failed: " + err.Error())
		}
		return flashInfoMsg("copied " + text)
	}
}

// sessionDisplayTitle returns the effective display title for a session,
// matching the list panel's priority: custom title → headline → first message → "New session".
func sessionDisplayTitle(s claude.ClaudeSession) string {
	var title string
	switch {
	case s.CustomTitle != "":
		title = s.CustomTitle
	case s.Headline != "":
		title = s.Headline
	case s.FirstMessage != "":
		title = strings.ReplaceAll(s.FirstMessage, "\n", " ")
	default:
		title = "New session"
	}
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:79]) + "…"
	}
	return title
}

// killPaneCmd sends SIGTERM to the claude process, kills the tmux pane, and cleans up status files.
func killPaneCmd(paneID string, pid int) tea.Cmd {
	return func() tea.Msg {
		if pid > 0 {
			syscall.Kill(pid, syscall.SIGTERM) //nolint:errcheck
		}
		tmux.KillPane(paneID) //nolint:errcheck
		claude.RemoveStatus(paneID)
		return PaneKilledMsg{}
	}
}

type flashInfoMsg string
type flashErrorMsg string

// reopenPopup schedules a new tmux popup to open after the current one closes.
// It uses run-shell with a short sleep so the new popup opens after the old one exits.
// bin must be the cached os.Executable() path from Model.binaryPath.
func reopenPopup(bin string, currentlyFullscreen bool) tea.Cmd {
	return func() tea.Msg {
		if bin == "" || os.Getenv("TMUX") == "" {
			return tea.QuitMsg{}
		}
		// Escape any single quotes in the path (POSIX: replace ' with '\'')
		escaped := strings.ReplaceAll(bin, "'", `'\''`)
		var shellCmd string
		if currentlyFullscreen {
			shellCmd = fmt.Sprintf("sleep 0.2 && tmux display-popup -E -w 80%% -h 70%% '%s'", escaped)
		} else {
			shellCmd = fmt.Sprintf("sleep 0.2 && tmux display-popup -E -w 100%% -h 100%% -e CLAUDE_TUI_FULLSCREEN=1 '%s'", escaped)
		}
		exec.Command("tmux", "run-shell", shellCmd).Start() //nolint:errcheck
		return tea.QuitMsg{}
	}
}

// tryInitialSelection sets the cursor to the user's originating pane if it's a
// Done Claude session, otherwise leaves cursor at 0 (oldest Done session after sort).
// Only runs once, when both sessions and origPane are available.
// Returns true if the cursor was moved to origPane (caller should fetch preview).
func (m *Model) tryInitialSelection() bool {
	if m.initialSelectionDone || len(m.sessions) == 0 {
		return false
	}
	if m.origPane.Captured {
		for _, s := range m.sessions {
			if s.PaneID == m.origPane.PaneID && s.Status == claude.StatusDone {
				m.initialSelectionDone = true
				return m.list.SelectByPaneID(m.origPane.PaneID)
			}
		}
		// origPane is not a Done Claude session — cursor stays at 0 (oldest Done)
		m.initialSelectionDone = true
	}
	return false
}

// claudeStatusToPane converts claude.Status to ui.PaneStatus* constant.
func claudeStatusToPane(s claude.Status) int {
	switch s {
	case claude.StatusWorking:
		return ui.PaneStatusWorking
	case claude.StatusDone:
		return ui.PaneStatusDone
	case claude.StatusDeferred:
		return ui.PaneStatusDeferred
	default:
		return ui.PaneStatusNone
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		m.ready = true
		listWidth := max(m.width*m.listWidthPct/100, 20)
		previewWidth := m.width - listWidth
		contentHeight := m.height - 2 // 1 header + 1 footer
		m.list.SetSize(listWidth-1, contentHeight) // -1 for ListPanelStyle right border
		m.preview.SetSize(previewWidth, contentHeight)
		minimapH := contentHeight / 2
		if minimapH > 14 {
			minimapH = 14
		}
		m.minimap.SetSize(0, minimapH)
		return m, nil

	case DaemonDisconnectedMsg:
		m.err = msg.Err
		return m, nil

	case SessionsRefreshedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.sessions = msg.Sessions
		m.list.SetItems(m.sessions)
		m.tryInitialSelection()
		var cmds []tea.Cmd
		// Update minimap status flags
		if m.showMinimap {
			paneStatuses := make(map[string]int)
			for _, s := range msg.Sessions {
				paneStatuses[s.PaneID] = claudeStatusToPane(s.Status)
			}
			m.minimap.UpdateStatus(paneStatuses)
		}
		// Capture preview + transcript + diff stats for selected item; diff stats for all sessions
		cmds = append(cmds, m.fetchAllDiffStats(msg.Sessions))
		if s, ok := m.list.SelectedItem(); ok {
			cmds = append(cmds, capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
			if m.showMinimap && m.minimapSession == "" {
				cmds = append(cmds, m.fetchMinimapData(s.TmuxSession))
			}
		}
		// Wait for next daemon push
		cmds = append(cmds, m.waitForDaemonUpdate())
		return m, tea.Batch(cmds...)

	case PreviewReadyMsg:
		if msg.Err != nil {
			return m, nil
		}
		if s, ok := m.list.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.preview.SetSession(&s, msg.Content)
		}
		return m, nil

	case TranscriptReadyMsg:
		if s, ok := m.list.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.preview.SetUserMessages(msg.Messages)
		}
		return m, nil

	case DiffStatsReadyMsg:
		if s, ok := m.list.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.preview.SetDiffStats(msg.Stats)
		}
		m.list.SetDiffStats(msg.SessionID, msg.Stats)
		return m, nil

	case HooksReadyMsg:
		if s, ok := m.list.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.preview.SetHookEvents(msg.Events)
		}
		return m, nil

	case SummaryReadyMsg:
		m.list.SetSummaryLoading(msg.PaneID, false)
		if msg.Err != nil {
			m.flashMsg = "Summarize failed: " + msg.Err.Error()
			m.flashIsError = true
			m.flashExpiry = time.Now().Add(5 * time.Second)
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
		}
		if s, ok := m.list.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.preview.SetSummary(msg.Summary)
		}
		// Update in-memory headline immediately so the list reflects it
		if msg.Summary != nil && msg.Summary.Headline != "" {
			for i := range m.sessions {
				if m.sessions[i].PaneID == msg.PaneID {
					m.sessions[i].Headline = msg.Summary.Headline
					break
				}
			}
			m.list.SetItems(m.sessions)
		}
		if msg.FromCache && msg.UserRequested {
			m.flashMsg = "summary unchanged (cached)"
			m.flashIsError = false
			m.flashExpiry = time.Now().Add(2 * time.Second)
			return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
		}
		return m, nil

	case SummarizeAllReadyMsg:
		if msg.Err != nil {
			m.flashMsg = "Summarize all failed: " + msg.Err.Error()
			m.flashIsError = true
			m.flashExpiry = time.Now().Add(5 * time.Second)
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
		}
		updated := false
		for _, r := range msg.Results {
			m.list.SetSummaryLoading(r.PaneID, false)
			if s, ok := m.list.SelectedItem(); ok && s.PaneID == r.PaneID {
				m.preview.SetSummary(r.Summary)
			}
			if r.Summary != nil && r.Summary.Headline != "" {
				for i := range m.sessions {
					if m.sessions[i].PaneID == r.PaneID {
						m.sessions[i].Headline = r.Summary.Headline
						updated = true
						break
					}
				}
			}
		}
		if updated {
			m.list.SetItems(m.sessions)
		}
		return m, nil

	case flashInfoMsg:
		m.flashMsg = string(msg)
		m.flashIsError = false
		m.flashExpiry = time.Now().Add(2 * time.Second)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })

	case flashErrorMsg:
		m.flashMsg = string(msg)
		m.flashIsError = true
		m.flashExpiry = time.Now().Add(5 * time.Second)
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })

	case ClearFlashMsg:
		if !m.flashExpiry.IsZero() && !time.Now().Before(m.flashExpiry) {
			m.flashMsg = ""
			m.flashIsError = false
			m.flashExpiry = time.Time{}
		}
		return m, nil

	case WindowRenameMsg:
		m.renaming = false
		if msg.Err != nil {
			m.flashMsg = "Rename failed: " + msg.Err.Error()
			m.flashIsError = true
			m.flashExpiry = time.Now().Add(5 * time.Second)
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
		}
		m.flashMsg = "renamed → " + msg.Name
		m.flashIsError = false
		m.flashExpiry = time.Now().Add(3 * time.Second)
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })

	case PaneKilledMsg:
		title := m.killTargetTitle
		m.state = StateNormal
		m.killTargetPaneID = ""
		m.killTargetPID = 0
		m.killTargetTitle = ""
		if msg.Err != nil {
			m.flashMsg = "kill failed: " + msg.Err.Error()
			m.flashIsError = true
			m.flashExpiry = time.Now().Add(5 * time.Second)
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
		}
		m.flashMsg = "killed " + title
		m.flashIsError = false
		m.flashExpiry = time.Now().Add(2 * time.Second)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })

	case OriginalPaneCapturedMsg:
		if msg.Err == nil {
			m.origPane = originalPane{
				Session: msg.Session, Window: msg.Window, Pane: msg.Pane,
				PaneID: msg.PaneID, Captured: true,
			}
			if m.tryInitialSelection() {
				if s, ok := m.list.SelectedItem(); ok {
					return m, tea.Batch(
						capturePreview(s.PaneID),
						m.fetchTranscript(s.PaneID, s.SessionID),
						m.fetchCachedSummary(s.PaneID, s.SessionID),
					)
				}
			}
		}
		return m, nil

	case MinimapReadyMsg:
		paneStatuses := make(map[string]int)
		for _, s := range m.sessions {
			paneStatuses[s.PaneID] = claudeStatusToPane(s.Status)
		}
		selectedPaneID := ""
		if s, ok := m.list.SelectedItem(); ok {
			selectedPaneID = s.PaneID
		}
		m.minimap.SetData(msg.Panes, paneStatuses, selectedPaneID, msg.SessionName)
		m.minimapSession = msg.SessionName
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		frame := m.spinner.View()
		m.list.SetSpinnerView(frame)
		m.minimap.SetSpinnerView(frame)
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

	case StateFiltering:
		switch {
		case key.Matches(msg, Keys.Escape):
			m.state = StateNormal
			m.filter.Deactivate()
			m.list.ClearFilter()
			return m, nil
		case key.Matches(msg, Keys.Enter):
			val := m.filter.Confirm()
			m.state = StateNormal
			if val == "" {
				m.list.ClearFilter()
			}
			return m, nil
		default:
			// Forward to textinput
			ti := m.filter.TextInput()
			newTI, cmd := ti.Update(msg)
			*ti = newTI
			m.list.SetFilter(m.filter.Value())
			// Update preview for new selection
			if s, ok := m.list.SelectedItem(); ok {
				return m, tea.Batch(cmd, capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
			}
			return m, cmd
		}

	case StateKillConfirm:
		switch msg.String() {
		case "y":
			return m, killPaneCmd(m.killTargetPaneID, m.killTargetPID)
		case "n", "esc":
			m.state = StateNormal
			m.killTargetPaneID = ""
			m.killTargetPID = 0
			m.killTargetTitle = ""
			return m, nil
		default:
			return m, nil
		}

	case StateDeferPrompt:
		switch {
		case key.Matches(msg, Keys.Escape):
			m.state = StateNormal
			m.deferPrompt.Deactivate()
			return m, nil
		case key.Matches(msg, Keys.Enter):
			val := m.deferPrompt.Confirm()
			m.state = StateNormal
			minutes, err := strconv.Atoi(val)
			if err != nil || minutes <= 0 {
				return m, nil
			}
			if s, ok := m.list.SelectedItem(); ok {
				return m, func() tea.Msg {
					if err := m.client.Defer(s.PaneID, minutes); err != nil {
						return nil
					}
					return nil
				}
			}
			return m, nil
		default:
			ti := m.deferPrompt.TextInput()
			newTI, cmd := ti.Update(msg)
			*ti = newTI
			return m, cmd
		}

	case StatePromptRelay:
		switch {
		case key.Matches(msg, Keys.Escape):
			m.state = StateNormal
			m.relay.Deactivate()
			return m, nil
		case key.Matches(msg, Keys.MsgNext):
			m.preview.NavigateMsg(1)
			return m, nil
		case key.Matches(msg, Keys.MsgPrev):
			m.preview.NavigateMsg(-1)
			return m, nil
		case key.Matches(msg, Keys.Enter):
			val := m.relay.Confirm()
			m.state = StateNormal
			if val == "" {
				return m, nil
			}
			if s, ok := m.list.SelectedItem(); ok {
				return m, sendPromptRelay(s.PaneID, val)
			}
			return m, nil
		default:
			ti := m.relay.TextInput()
			newTI, cmd := ti.Update(msg)
			*ti = newTI
			return m, cmd
		}

	default: // StateNormal
		// Handle multi-key chord sequences
		if m.pendingChord != "" {
			seq := m.pendingChord + msg.String()
			if chord, ok := ChordExact(seq); ok {
				m.pendingChord = ""
				return m.executeChord(chord)
			}
			if len(ChordsWithPrefix(seq)) > 0 {
				m.pendingChord = seq
				return m, nil
			}
			// Not a valid chord continuation — cancel and fall through
			m.pendingChord = ""
		}

		// Check if this key starts any chord
		if len(ChordsWithPrefix(msg.String())) > 0 {
			m.pendingChord = msg.String()
			return m, nil
		}

		switch {
		case key.Matches(msg, Keys.Quit), key.Matches(msg, Keys.Escape):
			if m.origPane.Captured {
				tmux.SwitchToPaneQuiet(m.origPane.Session, m.origPane.Window, m.origPane.Pane)
			}
			return m, tea.Quit

		case m.showMinimap && key.Matches(msg, Keys.SpatialUp, Keys.SpatialDown, Keys.SpatialLeft, Keys.SpatialRight):
			var dir ui.SpatialDir
			dirName := ""
			switch {
			case key.Matches(msg, Keys.SpatialUp):
				dir = ui.DirUp
				dirName = "Up"
			case key.Matches(msg, Keys.SpatialDown):
				dir = ui.DirDown
				dirName = "Down"
			case key.Matches(msg, Keys.SpatialLeft):
				dir = ui.DirLeft
				dirName = "Left"
			case key.Matches(msg, Keys.SpatialRight):
				dir = ui.DirRight
				dirName = "Right"
			}
			m.minimap.LastNavDebug = "key=" + msg.String() + " dir=" + dirName
			paneID, isClaude := m.minimap.NavigateSpatial(dir)
			if paneID == "" {
				return m, nil
			}
			if isClaude && m.list.SelectByPaneID(paneID) {
				if s, ok := m.list.SelectedItem(); ok {
					return m, tea.Batch(
						capturePreview(s.PaneID),
						m.fetchTranscript(s.PaneID, s.SessionID),
						m.fetchDiffStats(s.PaneID, s.SessionID),
						m.fetchCachedSummary(s.PaneID, s.SessionID),
						switchPaneQuiet(s.TmuxSession, s.TmuxWindow, s.TmuxPane),
					)
				}
			}
			return m, nil

		case key.Matches(msg, Keys.Up):
			m.list.MoveUp()
			if s, ok := m.list.SelectedItem(); ok {
				cmds := []tea.Cmd{
					capturePreview(s.PaneID),
					m.fetchTranscript(s.PaneID, s.SessionID),
					m.fetchDiffStats(s.PaneID, s.SessionID),
					m.fetchCachedSummary(s.PaneID, s.SessionID),
					switchPaneQuiet(s.TmuxSession, s.TmuxWindow, s.TmuxPane),
				}
				if m.showMinimap {
					if s.TmuxSession != m.minimapSession {
						cmds = append(cmds, m.fetchMinimapData(s.TmuxSession))
					} else {
						m.minimap.UpdateSelected(s.PaneID)
					}
				}
				return m, tea.Batch(cmds...)
			}
			return m, nil

		case key.Matches(msg, Keys.Down):
			m.list.MoveDown()
			if s, ok := m.list.SelectedItem(); ok {
				cmds := []tea.Cmd{
					capturePreview(s.PaneID),
					m.fetchTranscript(s.PaneID, s.SessionID),
					m.fetchDiffStats(s.PaneID, s.SessionID),
					m.fetchCachedSummary(s.PaneID, s.SessionID),
					switchPaneQuiet(s.TmuxSession, s.TmuxWindow, s.TmuxPane),
				}
				if m.showMinimap {
					if s.TmuxSession != m.minimapSession {
						cmds = append(cmds, m.fetchMinimapData(s.TmuxSession))
					} else {
						m.minimap.UpdateSelected(s.PaneID)
					}
				}
				return m, tea.Batch(cmds...)
			}
			return m, nil

		case key.Matches(msg, Keys.Enter):
			if m.showHooks {
				m.preview.ToggleExpand()
				return m, nil
			}
			if s, ok := m.list.SelectedItem(); ok {
				tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
				return m, tea.Quit
			}
			return m, nil

		case key.Matches(msg, Keys.PromptRelay):
			if s, ok := m.list.SelectedItem(); ok {
				if s.Status == claude.StatusDone {
					m.state = StatePromptRelay
					m.relay.Activate()
				} else {
					return m, func() tea.Msg { return flashErrorMsg("session is busy") }
				}
			}
			return m, nil

		case key.Matches(msg, Keys.Filter):
			m.state = StateFiltering
			m.filter.Activate()
			return m, nil

		case key.Matches(msg, Keys.Defer):
			if _, ok := m.list.SelectedItem(); ok {
				m.state = StateDeferPrompt
				m.deferPrompt.Activate()
			}
			return m, nil

		case key.Matches(msg, Keys.Undefer):
			if s, ok := m.list.SelectedItem(); ok {
				return m, func() tea.Msg {
					m.client.Undefer(s.PaneID)
					return nil
				}
			}
			return m, nil

		case key.Matches(msg, Keys.Hooks):
			m.showHooks = !m.showHooks
			m.preview.SetShowHooks(m.showHooks)
			if m.showHooks {
				if s, ok := m.list.SelectedItem(); ok {
					return m, m.fetchHooks(s.PaneID)
				}
			}
			return m, nil

		case key.Matches(msg, Keys.GroupMode):
			newMode := !m.list.GroupByProject()
			m.list.SetGroupByProject(newMode)
			savePrefBool("groupByProject", newMode)
			return m, nil

		case key.Matches(msg, Keys.Minimap):
			m.showMinimap = !m.showMinimap
			savePrefBool("minimap", m.showMinimap)
			if m.showMinimap {
				if s, ok := m.list.SelectedItem(); ok {
					return m, m.fetchMinimapData(s.TmuxSession)
				}
			}
			return m, nil

		case key.Matches(msg, Keys.Summarize):
			if s, ok := m.list.SelectedItem(); ok && s.SessionID != "" {
				m.list.SetSummaryLoading(s.PaneID, true)
				return m, m.fetchSummarize(s.PaneID, s.SessionID)
			}
			return m, nil

		case key.Matches(msg, Keys.SummarizeAll):
			var latestPaneID string
			var latestTime time.Time
			for _, sess := range m.sessions {
				if sess.LastChanged.After(latestTime) {
					latestTime = sess.LastChanged
					latestPaneID = sess.PaneID
				}
			}
			// Mark all as loading
			for _, sess := range m.sessions {
				if sess.PaneID != latestPaneID && sess.SessionID != "" {
					m.list.SetSummaryLoading(sess.PaneID, true)
				}
			}
			return m, m.fetchSummarizeAll(latestPaneID)

		case key.Matches(msg, Keys.Rename):
			if s, ok := m.list.SelectedItem(); ok && !m.renaming {
				m.renaming = true
				return m, m.fetchRenameWindow(s.TmuxSession, s.TmuxWindow)
			}
			return m, nil

		case key.Matches(msg, Keys.Kill):
			if s, ok := m.list.SelectedItem(); ok {
				m.state = StateKillConfirm
				m.killTargetPaneID = s.PaneID
				m.killTargetPID = s.PID
				m.killTargetTitle = sessionDisplayTitle(s)
			}
			return m, nil

		case key.Matches(msg, Keys.CommitAndDone):
			if s, ok := m.list.SelectedItem(); ok {
				if s.Status != claude.StatusDone {
					return m, func() tea.Msg { return flashErrorMsg("session is busy") }
				}
				m.state = StateCommitAndDone
				m.commitDonePaneID = s.PaneID
				m.commitDonePID = s.PID
				m.commitDoneTitle = sessionDisplayTitle(s)
				return m, sendPromptRelay(s.PaneID, "/commit-commands:commit")
			}
			return m, nil

		case key.Matches(msg, Keys.Debug):
			m.debugMode = !m.debugMode
			return m, nil

		case key.Matches(msg, Keys.Fullscreen):
			return m, reopenPopup(m.binaryPath, m.inFullscreenPopup)

		case key.Matches(msg, Keys.ListShrink):
			m.listWidthPct = max(m.listWidthPct-5, 10)
			listWidth := max(m.width*m.listWidthPct/100, 20)
			contentHeight := m.height - 2
			m.list.SetSize(listWidth-1, contentHeight) // -1 for ListPanelStyle right border
			m.preview.SetSize(m.width-listWidth, contentHeight)
			return m, nil

		case key.Matches(msg, Keys.ListGrow):
			m.listWidthPct = min(m.listWidthPct+5, 60)
			listWidth := max(m.width*m.listWidthPct/100, 20)
			contentHeight := m.height - 2
			m.list.SetSize(listWidth-1, contentHeight) // -1 for ListPanelStyle right border
			m.preview.SetSize(m.width-listWidth, contentHeight)
			return m, nil

		case key.Matches(msg, Keys.Refresh):
			// In daemon mode, sessions are pushed — but we can still force a preview refresh
			if s, ok := m.list.SelectedItem(); ok {
				return m, capturePreview(s.PaneID)
			}
			return m, nil

		case key.Matches(msg, Keys.ScrollDown):
			m.preview.ScrollDown()
			return m, nil

		case key.Matches(msg, Keys.ScrollUp):
			m.preview.ScrollUp()
			return m, nil

		case !m.showHooks && key.Matches(msg, Keys.MsgNext):
			m.preview.NavigateMsg(1)
			return m, nil

		case !m.showHooks && key.Matches(msg, Keys.MsgPrev):
			m.preview.NavigateMsg(-1)
			return m, nil
		}
	}

	return m, nil
}
