package app

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (m Model) handleKeyBacklogPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.promptEditor.Deactivate()
		m.activeBacklogID = ""
		m.activeBacklogCWD = ""
		m.backlogOverlay = false
		return m, nil
	case msg.Type == tea.KeyEnter && msg.Alt:
		// Alt+Enter: insert newline (textarea won't do this on its own for alt+enter)
		cmd := m.promptEditor.Update(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
		return m, cmd
	case msg.String() == "ctrl+s":
		// Ctrl+S: save backlog
		body := m.promptEditor.Confirm()
		m.state = StateNormal
		m.backlogOverlay = false
		if strings.TrimSpace(body) == "" {
			m.activeBacklogID = ""
			m.activeBacklogCWD = ""
			return m, nil
		}
		id := m.activeBacklogID
		cwd := m.activeBacklogCWD
		if id == "" {
			id = claude.GenerateBacklogID()
		}
		m.activeBacklogID = ""
		m.activeBacklogCWD = ""
		sessions := m.sessions
		return m, tea.Batch(
			func() tea.Msg {
				err := claude.WriteBacklog(cwd, claude.Backlog{ID: id, Body: body})
				if err != nil {
					return flashErrorMsg("save backlog: " + err.Error())
				}
				return flashInfoMsg("backlog saved")
			},
			m.discoverBacklogs(sessions),
		)
	case key.Matches(msg, Keys.CtrlEnter):
		// Ctrl+Enter: save backlog then open prompt submission to a session
		body := m.promptEditor.Confirm()
		m.backlogOverlay = false
		if strings.TrimSpace(body) == "" {
			m.state = StateNormal
			m.activeBacklogID = ""
			m.activeBacklogCWD = ""
			return m, nil
		}
		id := m.activeBacklogID
		cwd := m.activeBacklogCWD
		if id == "" {
			id = claude.GenerateBacklogID()
		}
		// Find tmux session for spawning
		tmuxSession := m.findTmuxSessionForCWD(cwd)
		if tmuxSession == "" {
			m.state = StateNormal
			m.activeBacklogID = ""
			m.activeBacklogCWD = ""
			return m, func() tea.Msg { return flashErrorMsg("no tmux session detected") }
		}
		// Transition to new session prompt with backlog content
		m.state = StateNewSessionPrompt
		m.newSessionProject = filepath.Base(cwd)
		m.newSessionCWD = cwd
		m.newSessionTmuxSess = tmuxSession
		m.activeBacklogID = id
		m.activeBacklogCWD = cwd
		m.promptEditor.ActivateForBacklogSubmit(body)
		sessions := m.sessions
		return m, tea.Batch(
			func() tea.Msg {
				err := claude.WriteBacklog(cwd, claude.Backlog{ID: id, Body: body})
				if err != nil {
					return flashErrorMsg("save backlog: " + err.Error())
				}
				return nil
			},
			m.discoverBacklogs(sessions),
		)
	default:
		cmd := m.promptEditor.Update(msg)
		return m, cmd
	}
}

func (m Model) handleKeyBacklogDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		return m.confirmDeleteBacklog()
	case "n", "esc":
		m.state = StateNormal
		m.deleteTargetBacklog = claude.Backlog{}
		return m, nil
	default:
		return m, nil
	}
}
