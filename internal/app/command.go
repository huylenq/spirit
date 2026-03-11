package app

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/scripting"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

// Command represents a single dispatchable action for the command palette.
type Command struct {
	Name    string                          // display name shown in palette
	Hotkey  string                          // key hint: "w"
	Enabled func(m *Model) bool             // nil = always enabled
	Execute func(m *Model) (Model, tea.Cmd) // run the action
}

// --- Predicate helpers ---

func hasSelection(m *Model) bool {
	_, ok := m.sidebar.SelectedItem()
	return ok
}

func hasSessionID(m *Model) bool {
	s, ok := m.sidebar.SelectedItem()
	return ok && s.SessionID != ""
}

func canCommit(m *Model) bool {
	s, ok := m.sidebar.SelectedItem()
	return ok && s.Status == claude.StatusUserTurn && !s.CommitDonePending
}

// --- Exec methods (extracted from handleKey case blocks) ---

func (m Model) execSwitchPane() (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.IsPhantom {
		bookmarkID, cwd := s.LaterBookmarkID, s.CWD
		tmuxSession := m.origPane.Session
		return m, func() tea.Msg {
			if err := m.client.OpenLater(bookmarkID, cwd, tmuxSession); err != nil {
				return flashErrorMsg("open failed: " + err.Error())
			}
			return tea.QuitMsg{}
		}
	}
	if s.LaterBookmarkID != "" {
		m.client.Unlater(s.LaterBookmarkID) //nolint:errcheck
	}
	tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
	return m, tea.Quit
}

func (m Model) execPromptRelay() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); ok {
		m.state = StatePromptRelay
		m.relay.Activate()
	}
	return m, nil
}

func (m Model) execQueue() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); ok {
		m.state = StateQueueRelay
		m.queueCursor = -1 // start with text input focused
		m.queueRelay.Activate()
	}
	return m, nil
}

func (m Model) execSearch() (Model, tea.Cmd) {
	m.state = StateSearching
	m.search.Activate()
	return m, nil
}

func (m Model) execLater() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		if s.LaterBookmarkID != "" {
			// Toggle: unlater to restore real status
			paneID, bookmarkID := s.PaneID, s.LaterBookmarkID
			return m, func() tea.Msg {
				// Bookmark ID may not be populated yet; look it up
				if bookmarkID == "" {
					bookmarkID = claude.FindBookmarkIDByPane(paneID)
				}
				if bookmarkID == "" {
					return flashErrorMsg("no bookmark found")
				}
				if err := m.client.Unlater(bookmarkID); err != nil {
					return flashErrorMsg("unlater failed: " + err.Error())
				}
				return flashInfoMsg("restored from later")
			}
		}
		paneID, sessionID := s.PaneID, s.SessionID
		return m, func() tea.Msg {
			if err := m.client.Later(paneID, sessionID); err != nil {
				return flashErrorMsg("later failed: " + err.Error())
			}
			return flashInfoMsg("saved for later")
		}
	}
	return m, nil
}

func (m Model) execLaterKill() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		paneID, pid, sessionID := s.PaneID, s.PID, s.SessionID
		return m, func() tea.Msg {
			if err := m.client.LaterKill(paneID, pid, sessionID); err != nil {
				return flashErrorMsg("later+kill failed: " + err.Error())
			}
			return flashInfoMsg("saved for later, pane killed")
		}
	}
	return m, nil
}

func (m Model) execTranscript() (Model, tea.Cmd) {
	m.hideTranscript = !m.hideTranscript
	m.detail.SetHideTranscript(m.hideTranscript)
	return m, nil
}

func (m Model) execGroupMode() (Model, tea.Cmd) {
	newMode := !m.sidebar.GroupByProject()
	m.sidebar.SetGroupByProject(newMode)
	savePrefBool("groupByProject", newMode)
	return m, nil
}

func (m Model) execMinimap() (Model, tea.Cmd) {
	m.showMinimap = !m.showMinimap
	savePrefBool("minimap", m.showMinimap)
	if m.showMinimap {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchMinimapData(s.TmuxSession)
		}
	}
	return m, nil
}

func (m Model) execSynthesize() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok && s.SessionID != "" {
		m.sidebar.SetSummaryLoading(s.PaneID, true)
		return m, m.fetchSynthesize(s.PaneID, s.SessionID)
	}
	return m, nil
}

func (m Model) execSynthesizeAll() (Model, tea.Cmd) {
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
			m.sidebar.SetSummaryLoading(sess.PaneID, true)
		}
	}
	return m, m.fetchSynthesizeAll(latestPaneID)
}

func (m Model) execRename() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok && !m.renaming {
		m.renaming = true
		return m, m.fetchRenameWindow(s.TmuxSession, s.TmuxWindow)
	}
	return m, nil
}

func (m Model) execKill() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		if s.IsPhantom && s.LaterBookmarkID != "" {
			bookmarkID := s.LaterBookmarkID
			return m, func() tea.Msg {
				claude.RemoveLaterBookmark(bookmarkID)
				return PaneKilledMsg{}
			}
		}
		m.state = StateKillConfirm
		m.killTargetPaneID = s.PaneID
		m.killTargetSessionID = s.SessionID
		m.killTargetPID = s.PID
		m.killTargetTitle = sessionDisplayTitle(s)
		m.killTargetAnimalIdx = s.AvatarAnimalIdx
		m.killTargetColorIdx = s.AvatarColorIdx
		m.killTargetBookmarkID = s.LaterBookmarkID
	}
	return m, nil
}

func (m Model) execCommit() (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.Status != claude.StatusUserTurn {
		return m, func() tea.Msg { return flashErrorMsg("session is busy") }
	}
	if s.CommitDonePending {
		return m, func() tea.Msg { return flashInfoMsg("commit already pending") }
	}
	paneID, sessionID, pid := s.PaneID, s.SessionID, s.PID
	return m, func() tea.Msg {
		if err := m.client.CommitOnly(paneID, sessionID, pid); err != nil {
			return flashErrorMsg("commit failed: " + err.Error())
		}
		return flashInfoMsg("commit started")
	}
}

func (m Model) execCommitAndDone() (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.Status != claude.StatusUserTurn {
		return m, func() tea.Msg { return flashErrorMsg("session is busy") }
	}
	if s.CommitDonePending {
		return m, func() tea.Msg { return flashInfoMsg("commit+done already pending") }
	}
	paneID, sessionID, pid := s.PaneID, s.SessionID, s.PID
	return m, func() tea.Msg {
		if err := m.client.CommitAndDone(paneID, sessionID, pid); err != nil {
			return flashErrorMsg("commit+done failed: " + err.Error())
		}
		return flashInfoMsg("commit+done started")
	}
}

func (m Model) execDebug() (Model, tea.Cmd) {
	m.debugMode = !m.debugMode
	if m.debugMode {
		return m, m.fetchGlobalEffects()
	}
	return m, nil
}

func (m Model) execHelp() (Model, tea.Cmd) {
	m.showHelp = true
	return m, nil
}

func (m Model) execFullscreen() (Model, tea.Cmd) {
	return m, reopenPopup(m.binaryPath, m.inFullscreenPopup)
}

func (m Model) execRefresh() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		return m, capturePreview(s.PaneID)
	}
	return m, nil
}

func (m Model) execCopySessionID() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok && s.SessionID != "" {
		return m, copyToClipboard(s.SessionID)
	}
	return m, nil
}

func (m Model) execGoTop() (Model, tea.Cmd) {
	m.recordJump()
	m.sidebar.MoveToTop()
	if s, ok := m.sidebar.SelectedItem(); ok {
		return m, tea.Batch(m.fetchForSelection(s, true)...)
	}
	return m, nil
}

func (m Model) execCaptureView() (Model, tea.Cmd) {
	text := ansi.Strip(m.View())
	return m, copyToClipboard(text)
}

func (m Model) execNewSession() (Model, tea.Cmd) {
	// Save session-level state for restore on cancel, then switch to project level
	m.newSessionWasSession = m.sidebar.SelectionLevel() == ui.LevelSession
	if m.newSessionWasSession {
		if s, ok := m.sidebar.SelectedItem(); ok {
			m.newSessionPrevPaneID = s.PaneID
		}
		m.sidebar.EnterProjectLevel()
	}

	pe, ok := m.sidebar.SelectedProject()
	if !ok {
		return m, nil
	}

	sessions := m.sidebar.SessionsInProject(pe)

	var cwd, tmuxSession string
	for _, s := range sessions {
		if cwd == "" && s.CWD != "" {
			// For worktree sessions, use the parent repo path so new sessions
			// start in the real project root, not the worktree subdir.
			if s.IsWorktree && s.WorktreeRootProjectPath != "" {
				cwd = s.WorktreeRootProjectPath
			} else {
				cwd = s.CWD
			}
		}
		if s.TmuxSession != "" && tmuxSession == "" {
			tmuxSession = s.TmuxSession
		}
	}

	if cwd == "" {
		return m, func() tea.Msg { return flashErrorMsg("no working directory for project") }
	}

	// Heuristic: use first live session's tmux session; fallback to origPane
	if tmuxSession == "" {
		if m.origPane.Captured {
			tmuxSession = m.origPane.Session
		} else {
			return m, func() tea.Msg { return flashErrorMsg("no tmux session detected") }
		}
	}

	// Open prompt editor overlay instead of immediately creating the window
	m.state = StateNewSessionPrompt
	m.newSessionProject = pe.Name
	m.newSessionCWD = cwd
	m.newSessionTmuxSess = tmuxSession
	m.promptEditor.Activate()
	return m, nil
}

// spawnNewSession creates the tmux window, launches claude, and optionally
// registers a pending prompt with the daemon for delivery once the session is ready.
func (m Model) spawnNewSession(prompt, model string, planning bool, worktree string) tea.Cmd {
	cwd, tmuxSession := m.newSessionCWD, m.newSessionTmuxSess
	return func() tea.Msg {
		paneID, err := tmux.NewWindow(tmuxSession, cwd)
		if err != nil {
			return flashErrorMsg("new window: " + err.Error())
		}
		cmd := "claude --dangerously-skip-permissions"
		if model != "" {
			cmd += " --model " + model
		}
		if worktree != "" {
			cmd += " --worktree " + worktree
		}
		tmux.SendKeysLiteral(paneID, cmd) //nolint:errcheck
		if prompt != "" || planning {
			// Register pending prompt with daemon for delivery when session is ready.
			// If planning, daemon will prepend "/plan " to the prompt text.
			if err := m.client.PendingPrompt(paneID, prompt, planning); err != nil {
				return flashErrorMsg("register prompt: " + err.Error())
			}
		}
		return NewSessionCreatedMsg{PaneID: paneID}
	}
}

func (m Model) execPrefsEditor() (Model, tea.Cmd) {
	m.state = StatePrefsEditor
	m.prefsEditor.Activate(prefsFileContent())
	return m, nil
}

// applyPrefsFromText parses raw text, persists via savePrefs, applies all known keys
// to the live model, and returns the count of unknown keys.
func (m *Model) applyPrefsFromText(text string) int {
	prefs := parsePrefsText(text)
	savePrefs(prefs)

	// Count unknown keys
	unknowns := len(prefs)
	for _, def := range PrefRegistry {
		if _, ok := prefs[def.Key]; ok {
			unknowns--
		}
	}

	// Apply each known key to live model state
	m.sidebar.SetGroupByProject(prefs["groupByProject"] == "true")
	m.sidebar.SetShowBacklog(prefs["showBacklog"] == "true")
	m.showMinimap = prefs["minimap"] == "true"
	if v := prefs["minimapMode"]; v != "" {
		m.minimapMode = v
	}
	if n, err := strconv.Atoi(prefs["minimapMaxH"]); err == nil {
		m.minimapMaxH = n
	}
	m.minimapCollapse = prefs["minimapCollapse"] == "true"
	if n, err := strconv.Atoi(prefs["sidebarWidthPct"]); err == nil {
		m.sidebarWidthPct = n
	}
	m.applyLayout()
	return unknowns
}

func (m Model) execNewBacklogForCWD(cwd string) (Model, tea.Cmd) {
	m.state = StateBacklogPrompt
	m.activeBacklogCWD = cwd
	m.activeBacklogID = ""
	m.backlogOverlay = true
	m.promptEditor.ActivateForBacklog()
	return m, nil
}

func (m Model) execNewBacklog() (Model, tea.Cmd) {
	pe, ok := m.sidebar.SelectedProject()
	if !ok {
		return m, nil
	}
	// Backlog project/section: use full right-pane editor
	cwd := m.sidebar.FirstBacklogCWDInProject(pe.Name)
	if cwd == "" {
		return m, func() tea.Msg { return flashErrorMsg("no working directory for project") }
	}
	m.state = StateBacklogPrompt
	m.activeBacklogCWD = cwd
	m.activeBacklogID = ""
	m.backlogOverlay = false
	m.promptEditor.ActivateForBacklog()
	return m, nil
}

func (m Model) execEditBacklog() (Model, tea.Cmd) {
	backlog, ok := m.sidebar.SelectedBacklog()
	if !ok {
		return m, nil
	}
	m.state = StateBacklogPrompt
	m.activeBacklogID = backlog.ID
	m.activeBacklogCWD = backlog.CWD
	m.backlogOverlay = false
	m.promptEditor.ActivateForBacklogEdit(backlog.Body)
	return m, nil
}

func (m Model) execDeleteBacklog() (Model, tea.Cmd) {
	backlog, ok := m.sidebar.SelectedBacklog()
	if !ok {
		return m, nil
	}
	m.state = StateBacklogDeleteConfirm
	m.deleteTargetBacklog = backlog
	return m, nil
}

func (m Model) confirmDeleteBacklog() (Model, tea.Cmd) {
	b := m.deleteTargetBacklog
	m.state = StateNormal
	m.deleteTargetBacklog = claude.Backlog{}
	sessions := m.sessions
	return m, tea.Batch(
		func() tea.Msg {
			if err := claude.RemoveBacklog(b.CWD, b.ID); err != nil {
				return flashErrorMsg("delete backlog: " + err.Error())
			}
			return flashInfoMsg("backlog deleted")
		},
		m.discoverBacklogs(sessions),
	)
}

func (m Model) execSubmitBacklog() (Model, tea.Cmd) {
	backlog, ok := m.sidebar.SelectedBacklog()
	if !ok {
		return m, nil
	}

	// Need a tmux session to create the window in
	var tmuxSession string
	for _, s := range m.sessions {
		if s.CWD == backlog.CWD && s.TmuxSession != "" {
			tmuxSession = s.TmuxSession
			break
		}
	}
	if tmuxSession == "" && m.origPane.Captured {
		tmuxSession = m.origPane.Session
	}
	if tmuxSession == "" {
		return m, func() tea.Msg { return flashErrorMsg("no tmux session detected") }
	}

	m.state = StateNewSessionPrompt
	m.newSessionProject = backlog.Project
	m.newSessionCWD = backlog.CWD
	m.newSessionTmuxSess = tmuxSession
	m.activeBacklogID = backlog.ID
	m.activeBacklogCWD = backlog.CWD
	m.promptEditor.ActivateForBacklogSubmit(backlog.Body)
	return m, nil
}

func (m Model) execOpenBacklogInEditor() (Model, tea.Cmd) {
	backlog, ok := m.sidebar.SelectedBacklog()
	if !ok {
		return m, nil
	}
	path := claude.BacklogFilePath(backlog.CWD, backlog.ID)
	tmuxSession := m.origPane.Session
	return m, func() tea.Msg {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vim"
		}
		cmd := fmt.Sprintf("%s %s", editor, path)
		if err := exec.Command("tmux", "split-window", "-t", tmuxSession, cmd).Run(); err != nil {
			return flashErrorMsg("open editor: " + err.Error())
		}
		return tea.QuitMsg{}
	}
}

func (m Model) execToggleDiffs() (Model, tea.Cmd) {
	m.showDiffs = !m.showDiffs
	m.showHooks = false
	m.showRawTranscript = false
	m.detail.SetShowDiffs(m.showDiffs)
	m.detail.SetShowHooks(false)
	m.detail.SetShowRawTranscript(false)
	if m.showDiffs {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchDiffHunks(s.PaneID, s.SessionID, s.CWD)
		}
	}
	return m, nil
}

func (m Model) execToggleHooks() (Model, tea.Cmd) {
	m.showHooks = !m.showHooks
	m.showRawTranscript = false
	m.showDiffs = false
	m.detail.SetShowHooks(m.showHooks)
	m.detail.SetShowRawTranscript(false)
	m.detail.SetShowDiffs(false)
	if m.showHooks {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchHooks(s.PaneID, s.SessionID)
		}
	}
	return m, nil
}

func (m Model) execToggleRawTranscript() (Model, tea.Cmd) {
	m.showRawTranscript = !m.showRawTranscript
	m.showHooks = false
	m.showDiffs = false
	m.detail.SetShowRawTranscript(m.showRawTranscript)
	m.detail.SetShowHooks(false)
	m.detail.SetShowDiffs(false)
	if m.showRawTranscript {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchRawTranscript(s.PaneID, s.SessionID)
		}
	}
	return m, nil
}

func (m Model) execShowSpiritAnimal() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); !ok {
		return m, nil
	}
	m.showSpiritAnimal = true
	return m, nil
}

// evalLua runs a Lua script async against the daemon and returns a LuaEvalDoneMsg.
func evalLua(client *daemon.Client, script string) tea.Cmd {
	return evalLuaWithContext(client, script, scripting.EvalContext{})
}

// evalLuaWithContext runs a Lua script with TUI context (e.g. selected session).
func evalLuaWithContext(client *daemon.Client, script string, ctx scripting.EvalContext) tea.Cmd {
	return func() tea.Msg {
		result, msgs, err := scripting.RunEvalWithContext(script, client, os.Stderr, ctx)
		return LuaEvalDoneMsg{Result: result, Msgs: msgs, Err: err}
	}
}
