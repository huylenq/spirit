package app

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

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
