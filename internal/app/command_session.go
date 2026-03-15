package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

func (m Model) execSwitchPane() (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.IsPhantom {
		laterID, cwd := s.LaterID, s.CWD
		tmuxSession := m.origPane.Session
		return m, func() tea.Msg {
			if err := m.client.OpenLater(laterID, cwd, tmuxSession); err != nil {
				return flashErrorMsg("open failed: " + err.Error())
			}
			return tea.QuitMsg{}
		}
	}
	if s.LaterID != "" {
		m.client.Unlater(s.LaterID) //nolint:errcheck
	}
	tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
	return m, tea.Quit
}

func (m Model) execKill() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		if s.IsPhantom && s.LaterID != "" {
			laterID := s.LaterID
			return m, func() tea.Msg {
				claude.RemoveLaterRecord(laterID)
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
		m.killTargetLaterID = s.LaterID
	}
	return m, nil
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

func (m Model) execNewSessionAtPath() (Model, tea.Cmd) {
	if !m.origPane.Captured {
		return m, func() tea.Msg { return flashErrorMsg("no tmux session detected") }
	}
	m.newSessionTmuxSess = m.origPane.Session
	m.state = StateNewSessionPathInput
	m.pathInput.Activate()
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

func (m Model) execRename() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok && !m.renaming {
		m.renaming = true
		return m, m.fetchRenameWindow(s.TmuxSession, s.TmuxWindow)
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
