package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	case "yc":
		text := ansi.Strip(m.View())
		return m, copyToClipboard(text)
	case "ih":
		m.showHooks = !m.showHooks
		m.showRawTranscript = false
		m.preview.SetShowHooks(m.showHooks)
		m.preview.SetShowRawTranscript(false)
		if m.showHooks {
			if s, ok := m.list.SelectedItem(); ok {
				return m, m.fetchHooks(s.PaneID)
			}
		}
		return m, nil
	case "it":
		m.showRawTranscript = !m.showRawTranscript
		m.showHooks = false
		m.preview.SetShowRawTranscript(m.showRawTranscript)
		m.preview.SetShowHooks(false)
		if m.showRawTranscript {
			if s, ok := m.list.SelectedItem(); ok {
				return m, m.fetchRawTranscript(s.PaneID, s.SessionID)
			}
		}
		return m, nil
	}
	return m, nil
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
func killPaneCmd(paneID string, pid int, bookmarkID string) tea.Cmd {
	return func() tea.Msg {
		if pid > 0 {
			syscall.Kill(pid, syscall.SIGTERM) //nolint:errcheck
		}
		tmux.KillPane(paneID) //nolint:errcheck
		claude.RemoveStatus(paneID)
		if bookmarkID != "" {
			claude.RemoveLaterBookmark(bookmarkID)
		}
		return PaneKilledMsg{}
	}
}

type flashInfoMsg string
type flashErrorMsg string

// reopenPopup schedules a new tmux popup to open after the current one closes.
// It persists the new fullscreen state to prefs so `cmc popup` picks it up,
// then uses run-shell with a short sleep so the new popup opens after the old one exits.
func reopenPopup(bin string, currentlyFullscreen bool) tea.Cmd {
	// Persist the toggled state so future `cmc popup` invocations use it
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

// tryInitialSelection auto-selects a pane on launch.
//
// Two modes controlled by env vars:
//   - selectActive (ctrl-space): select the originating pane's session
//   - rotateNext   (ctrl-tab):   skip originating pane, rotate to next YOUR TURN
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

	items := m.list.Items()

	if m.rotateNext {
		// ctrl-tab: skip originating pane, rotate to next YOUR TURN session
		origIdx := -1
		for i, s := range items {
			if s.PaneID == m.origPane.PaneID {
				origIdx = i
				break
			}
		}
		if origIdx >= 0 {
			n := len(items)
			for offset := 1; offset < n; offset++ {
				idx := (origIdx + offset) % n
				if items[idx].Status == claude.StatusDone {
					return m.list.SelectByPaneID(items[idx].PaneID)
				}
			}
			// No other YOUR TURN — fall back to origPane
			return m.list.SelectByPaneID(m.origPane.PaneID)
		}
	} else {
		// ctrl-space: exact match on originating pane (any status)
		for _, s := range m.sessions {
			if s.PaneID == m.origPane.PaneID {
				return m.list.SelectByPaneID(m.origPane.PaneID)
			}
		}
	}

	// Fallback: first session in same tmux session (already sorted)
	for _, s := range items {
		if s.TmuxSession == m.origPane.Session {
			return m.list.SelectByPaneID(s.PaneID)
		}
	}

	// Default: cursor stays at 0
	return false
}

// claudeStatusToPane converts claude.Status to ui.PaneStatus* constant.
func claudeStatusToPane(s claude.Status) int {
	switch s {
	case claude.StatusWorking:
		return ui.PaneStatusWorking
	case claude.StatusDone:
		return ui.PaneStatusDone
	case claude.StatusLater:
		return ui.PaneStatusLater
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
		return m, nil

	case ui.UsageBarTickMsg:
		cmd := m.usageBar.Tick()
		return m, cmd

	case SessionsRefreshedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.sessions = msg.Sessions
		m.list.SetItems(m.sessions)
		m.tryInitialSelection()
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

	case RawTranscriptReadyMsg:
		if s, ok := m.list.SelectedItem(); ok && s.PaneID == msg.PaneID {
			m.preview.SetRawTranscript(msg.JSON)
		}
		return m, nil

	case SummaryReadyMsg:
		if msg.Err != nil {
			m.flashMsg = "Synthesize failed: " + msg.Err.Error()
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

	case SynthesizeAllReadyMsg:
		if msg.Err != nil {
			m.flashMsg = "Synthesize all failed: " + msg.Err.Error()
			m.flashIsError = true
			m.flashExpiry = time.Now().Add(5 * time.Second)
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
		}
		updated := false
		for _, r := range msg.Results {
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
		m.killTargetBookmarkID = ""
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

	case StateSearching:
		switch {
		case key.Matches(msg, Keys.Escape):
			m.state = StateNormal
			m.search.Deactivate()
			m.list.ClearNarrow()
			return m, nil
		case key.Matches(msg, Keys.Enter):
			m.search.Confirm()
			m.state = StateNormal
			// Remember selection, clear filter, re-select (search & jump)
			var selectedPaneID string
			if s, ok := m.list.SelectedItem(); ok {
				selectedPaneID = s.PaneID
			}
			m.list.ClearNarrow()
			m.list.SelectByPaneID(selectedPaneID)
			return m, nil
		case key.Matches(msg, Keys.MsgNext):
			m.list.MoveDown()
			if s, ok := m.list.SelectedItem(); ok {
				return m, tea.Batch(capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
			}
			return m, nil
		case key.Matches(msg, Keys.MsgPrev):
			m.list.MoveUp()
			if s, ok := m.list.SelectedItem(); ok {
				return m, tea.Batch(capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
			}
			return m, nil
		default:
			// Forward to textinput
			ti := m.search.TextInput()
			newTI, cmd := ti.Update(msg)
			*ti = newTI
			m.list.SetNarrow(m.search.Value())
			// Update preview for new selection
			if s, ok := m.list.SelectedItem(); ok {
				return m, tea.Batch(cmd, capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
			}
			return m, cmd
		}

	case StateKillConfirm:
		switch msg.String() {
		case "y":
			return m, killPaneCmd(m.killTargetPaneID, m.killTargetPID, m.killTargetBookmarkID)
		case "n", "esc":
			m.state = StateNormal
			m.killTargetPaneID = ""
			m.killTargetPID = 0
			m.killTargetTitle = ""
			m.killTargetBookmarkID = ""
			return m, nil
		default:
			return m, nil
		}

	case StatePromptRelay:
		switch {
		case key.Matches(msg, Keys.Escape):
			m.state = StateNormal
			m.relay.Deactivate()
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

	case StateQueueRelay:
		switch {
		case key.Matches(msg, Keys.Escape):
			m.state = StateNormal
			m.queueRelay.Deactivate()
			return m, nil
		case key.Matches(msg, Keys.Enter):
			val := m.queueRelay.Confirm()
			m.state = StateNormal
			s, ok := m.list.SelectedItem()
			if !ok {
				return m, nil
			}
			if val == "" {
				// Empty submit on a session with pending queue → cancel
				if s.QueuePending != "" {
					paneID := s.PaneID
					return m, func() tea.Msg {
						if err := m.client.CancelQueue(paneID); err != nil {
							return flashErrorMsg("cancel failed: " + err.Error())
						}
						return flashInfoMsg("queue cancelled")
					}
				}
				return m, nil
			}
			paneID := s.PaneID
			return m, func() tea.Msg {
				if err := m.client.Queue(paneID, val); err != nil {
					return flashErrorMsg("queue failed: " + err.Error())
				}
				return flashInfoMsg("message queued")
			}
		default:
			ti := m.queueRelay.TextInput()
			newTI, cmd := ti.Update(msg)
			*ti = newTI
			return m, cmd
		}

	case StatePalette:
		switch {
		case key.Matches(msg, Keys.Escape):
			m.state = StateNormal
			m.palette.Deactivate()
			return m, nil
		case key.Matches(msg, Keys.Enter):
			idx, ok := m.palette.SelectedIndex()
			m.state = StateNormal
			m.palette.Deactivate()
			if !ok {
				return m, nil
			}
			command := m.commands[idx]
			if command.Enabled != nil && !command.Enabled(&m) {
				return m, nil
			}
			m, c := command.Execute(&m)
			return m, c
		case msg.String() == "up", key.Matches(msg, Keys.MsgPrev):
			m.palette.MoveUp()
			return m, nil
		case msg.String() == "down", key.Matches(msg, Keys.MsgNext):
			m.palette.MoveDown()
			return m, nil
		default:
			ti := m.palette.TextInput()
			newTI, cmd := ti.Update(msg)
			*ti = newTI
			m.palette.Narrow()
			return m, cmd
		}

	default: // StateNormal
		// When help overlay is open, only ? and esc dismiss it; swallow everything else
		if m.showHelp {
			switch {
			case key.Matches(msg, Keys.Help), key.Matches(msg, Keys.Escape):
				m.showHelp = false
				return m, nil
			case key.Matches(msg, Keys.Palette):
				m.showHelp = false
				// fall through to palette handling below
			default:
				return m, nil
			}
		}

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
		case key.Matches(msg, Keys.Palette):
			items := make([]ui.PaletteItem, len(m.commands))
			for i, cmd := range m.commands {
				enabled := true
				if cmd.Enabled != nil {
					enabled = cmd.Enabled(&m)
				}
				items[i] = ui.PaletteItem{
					Name:    cmd.Name,
					Hotkey:  cmd.Hotkey,
					Enabled: enabled,
					Index:   i,
				}
			}
			m.state = StatePalette
			m.palette.Activate(items)
			return m, nil

		case key.Matches(msg, Keys.Escape) && (m.showHooks || m.showRawTranscript):
			m.showHooks = false
			m.showRawTranscript = false
			m.preview.SetShowHooks(false)
			m.preview.SetShowRawTranscript(false)
			return m, nil

		case m.showRawTranscript && msg.String() == "e":
			if s, ok := m.list.SelectedItem(); ok && s.SessionID != "" {
				return m, openTranscriptInEditor(m.origPane.Session, s.SessionID)
			}
			return m, nil

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
					cmds := []tea.Cmd{
						capturePreview(s.PaneID),
						m.fetchTranscript(s.PaneID, s.SessionID),
						m.fetchDiffStats(s.PaneID, s.SessionID),
						m.fetchCachedSummary(s.PaneID, s.SessionID),
						switchPaneQuiet(s.TmuxSession, s.TmuxWindow, s.TmuxPane),
					}
					cmds = append(cmds, m.fetchVisibleOverlays(s.PaneID, s.SessionID)...)
					return m, tea.Batch(cmds...)
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
				cmds = append(cmds, m.fetchVisibleOverlays(s.PaneID, s.SessionID)...)
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
				cmds = append(cmds, m.fetchVisibleOverlays(s.PaneID, s.SessionID)...)
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
				if s.IsPhantom {
					// Dead Later → create new window + remove bookmark
					bookmarkID, cwd := s.LaterBookmarkID, s.CWD
					tmuxSession := m.origPane.Session
					return m, func() tea.Msg {
						if err := m.client.OpenLater(bookmarkID, cwd, tmuxSession); err != nil {
							return flashErrorMsg("open failed: " + err.Error())
						}
						return tea.QuitMsg{}
					}
				}
				// Live Later → auto-remove bookmark before switching
				if s.LaterBookmarkID != "" {
					m.client.Unlater(s.LaterBookmarkID) //nolint:errcheck
				}
				tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
				return m, tea.Quit
			}
			return m, nil

		case key.Matches(msg, Keys.PromptRelay):
			if _, ok := m.list.SelectedItem(); ok {
				m.state = StatePromptRelay
				m.relay.Activate()
			}
			return m, nil

		case key.Matches(msg, Keys.Queue):
			if s, ok := m.list.SelectedItem(); ok {
				m.state = StateQueueRelay
				if s.QueuePending != "" {
					m.queueRelay.ActivateWithValue(s.QueuePending)
				} else {
					m.queueRelay.Activate()
				}
			}
			return m, nil

		case key.Matches(msg, Keys.Search):
			m.state = StateSearching
			m.search.Activate()
			return m, nil

		case key.Matches(msg, Keys.Later):
			return m.execLater()

		case key.Matches(msg, Keys.LaterKill):
			return m.execLaterKill()

		case key.Matches(msg, Keys.Transcript):
			m.hideTranscript = !m.hideTranscript
			m.preview.SetHideTranscript(m.hideTranscript)
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

		case key.Matches(msg, Keys.Synthesize):
			if s, ok := m.list.SelectedItem(); ok && s.SessionID != "" {
				m.list.SetSummaryLoading(s.PaneID, true)
				return m, m.fetchSynthesize(s.PaneID, s.SessionID)
			}
			return m, nil

		case key.Matches(msg, Keys.SynthesizeAll):
			var latestPaneID string
			var latestTime time.Time
			for _, sess := range m.sessions {
				if sess.LastChanged.After(latestTime) {
					latestTime = sess.LastChanged
					latestPaneID = sess.PaneID
				}
			}
			for _, sess := range m.sessions {
				if sess.PaneID != latestPaneID && sess.SessionID != "" {
					m.list.SetSummaryLoading(sess.PaneID, true)
				}
			}
			return m, m.fetchSynthesizeAll(latestPaneID)

		case key.Matches(msg, Keys.Rename):
			if s, ok := m.list.SelectedItem(); ok && !m.renaming {
				m.renaming = true
				return m, m.fetchRenameWindow(s.TmuxSession, s.TmuxWindow)
			}
			return m, nil

		case key.Matches(msg, Keys.Kill):
			if s, ok := m.list.SelectedItem(); ok {
				if s.IsPhantom && s.LaterBookmarkID != "" {
					// Phantom Later — no pane to kill, just remove bookmark
					bookmarkID := s.LaterBookmarkID
					return m, func() tea.Msg {
						claude.RemoveLaterBookmark(bookmarkID)
						return PaneKilledMsg{}
					}
				}
				m.state = StateKillConfirm
				m.killTargetPaneID = s.PaneID
				m.killTargetPID = s.PID
				m.killTargetTitle = sessionDisplayTitle(s)
				m.killTargetBookmarkID = s.LaterBookmarkID
			}
			return m, nil

		case key.Matches(msg, Keys.Commit):
			if s, ok := m.list.SelectedItem(); ok {
				if s.Status != claude.StatusDone {
					return m, func() tea.Msg { return flashErrorMsg("session is busy") }
				}
				if s.CommitDonePending {
					return m, func() tea.Msg { return flashInfoMsg("commit already pending") }
				}
				paneID, pid := s.PaneID, s.PID
				return m, func() tea.Msg {
					if err := m.client.CommitOnly(paneID, pid); err != nil {
						return flashErrorMsg("commit failed: " + err.Error())
					}
					return flashInfoMsg("commit started")
				}
			}
			return m, nil

		case key.Matches(msg, Keys.CommitAndDone):
			if s, ok := m.list.SelectedItem(); ok {
				if s.Status != claude.StatusDone {
					return m, func() tea.Msg { return flashErrorMsg("session is busy") }
				}
				if s.CommitDonePending {
					return m, func() tea.Msg { return flashInfoMsg("commit+done already pending") }
				}
				paneID, pid := s.PaneID, s.PID
				return m, func() tea.Msg {
					if err := m.client.CommitAndDone(paneID, pid); err != nil {
						return flashErrorMsg("commit+done failed: " + err.Error())
					}
					return flashInfoMsg("commit+done started")
				}
			}
			return m, nil

		case key.Matches(msg, Keys.Debug):
			m.debugMode = !m.debugMode
			return m, nil

		case key.Matches(msg, Keys.Help):
			m.showHelp = true
			return m, nil

		case key.Matches(msg, Keys.Fullscreen):
			return m, reopenPopup(m.binaryPath, m.inFullscreenPopup)

		case key.Matches(msg, Keys.ListShrink):
			m.listWidthPct = max(m.listWidthPct-5, 10)
			m.applyLayout()
			savePrefInt("listWidthPct", m.listWidthPct)
			return m, nil

		case key.Matches(msg, Keys.ListGrow):
			m.listWidthPct = min(m.listWidthPct+5, 60)
			m.applyLayout()
			savePrefInt("listWidthPct", m.listWidthPct)
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

		case key.Matches(msg, Keys.PageDown):
			m.preview.ScrollPageDown()
			return m, nil

		case key.Matches(msg, Keys.PageUp):
			m.preview.ScrollPageUp()
			return m, nil

		case key.Matches(msg, Keys.MsgNext):
			if m.showHooks || m.showRawTranscript {
				m.preview.ScrollDown()
			} else {
				m.preview.NavigateMsg(1)
			}
			return m, nil

		case key.Matches(msg, Keys.MsgPrev):
			if m.showHooks || m.showRawTranscript {
				m.preview.ScrollUp()
			} else {
				m.preview.NavigateMsg(-1)
			}
			return m, nil
		}
	}

	return m, nil
}
