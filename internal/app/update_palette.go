package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/claude"
)

func (m Model) handleKeyPalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.palette.Deactivate()
		return m, nil
	case key.Matches(msg, Keys.Enter):
		if m.palette.IsLuaMode() {
			script := m.palette.LuaScript()
			m.state = StateNormal
			m.palette.Deactivate()
			if strings.TrimSpace(script) == "" {
				return m, nil
			}
			return m, evalLua(m.client, script)
		}
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
		if !m.palette.IsLuaMode() {
			m.palette.MoveUp()
		}
		return m, nil
	case msg.String() == "down", key.Matches(msg, Keys.MsgNext):
		if !m.palette.IsLuaMode() {
			m.palette.MoveDown()
		}
		return m, nil
	default:
		// When input is empty and user types ";", enter Lua mode (;; = open palette + enter Lua)
		if !m.palette.IsLuaMode() && m.palette.LuaScript() == "" && msg.String() == ";" {
			m.palette.EnterLuaMode()
			return m, nil
		}
		// Digit 1–9 with empty input: jump directly to that position
		if !m.palette.IsLuaMode() && m.palette.LuaScript() == "" {
			if len(msg.String()) == 1 && msg.String() >= "1" && msg.String() <= "9" {
				n := int(msg.String()[0] - '0')
				if idx, ok := m.palette.IndexByPosition(n); ok {
					m.state = StateNormal
					m.palette.Deactivate()
					command := m.commands[idx]
					if command.Enabled != nil && !command.Enabled(&m) {
						return m, nil
					}
					m, c := command.Execute(&m)
					return m, c
				}
				return m, nil
			}
		}
		ti := m.palette.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		if !m.palette.IsLuaMode() {
			m.palette.Narrow()
		}
		return m, cmd
	}
}

func (m Model) handleKeyNewSessionPath(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.pathInput.Deactivate()
		return m, nil
	case msg.Type == tea.KeyEnter:
		rawPath := strings.TrimSpace(m.pathInput.ConfirmRaw())
		if rawPath == "" {
			m.state = StateNormal
			return m, nil
		}
		// Expand ~ (cheap env read — done synchronously before goroutine)
		if strings.HasPrefix(rawPath, "~/") {
			home, _ := os.UserHomeDir()
			rawPath = filepath.Join(home, rawPath[2:])
		} else if rawPath == "~" {
			rawPath, _ = os.UserHomeDir()
		}
		// Validate in a goroutine so os.Stat() doesn't block the event loop
		return m, func() tea.Msg {
			info, err := os.Stat(rawPath)
			if err != nil || !info.IsDir() {
				return flashErrorMsg("not a directory: " + rawPath)
			}
			return pathValidatedMsg{cwd: rawPath, project: filepath.Base(rawPath)}
		}
	default:
		ti := m.pathInput.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		return m, cmd
	}
}

func (m Model) handleKeyNewSession(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.promptEditor.Deactivate()
		m.activeBacklogID = ""
		m.activeBacklogCWD = ""
		// Restore previous session-level selection if we came from there
		if m.newSessionWasSession {
			m.sidebar.EnterSessionLevel()
			m.selectByPaneID(m.newSessionPrevPaneID)
			m.newSessionWasSession = false
		}
		return m, nil
	case msg.Type == tea.KeyEnter && msg.Alt:
		// Alt+Enter: insert newline
		cmd := m.promptEditor.Update(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
		return m, cmd
	case msg.String() == "alt+o":
		m.promptEditor.SetModel("opus")
		return m, nil
	case msg.String() == "alt+s":
		m.promptEditor.SetModel("sonnet")
		return m, nil
	case msg.String() == "alt+h":
		m.promptEditor.SetModel("haiku")
		return m, nil
	case msg.String() == "alt+p":
		m.promptEditor.TogglePlan()
		return m, nil
	case msg.String() == "alt+w":
		if m.promptEditor.WorktreeMode() {
			m.promptEditor.ClearWorktree()
		} else {
			m.promptEditor.SetWorktree(claude.GenerateWorktreeName(m.newSessionCWD))
		}
		return m, nil
	case msg.Type == tea.KeyEnter:
		model := m.promptEditor.SelectedModel()
		planning := m.promptEditor.PlanMode()
		worktree := ""
		if m.promptEditor.WorktreeMode() {
			worktree = m.promptEditor.WorktreeName()
		}
		prompt := m.promptEditor.Confirm()
		m.state = StateNormal
		// If submitting a backlog item, delete the file after spawning
		spawnCmd := m.spawnNewSession(prompt, model, planning, worktree)
		if m.activeBacklogID != "" {
			backlogCWD, backlogID := m.activeBacklogCWD, m.activeBacklogID
			m.activeBacklogID = ""
			m.activeBacklogCWD = ""
			return m, tea.Batch(spawnCmd, func() tea.Msg {
				claude.RemoveBacklog(backlogCWD, backlogID) //nolint:errcheck
				return nil
			})
		}
		// Keep newSessionWasSession alive — cleared in NewSessionCreatedMsg handler
		return m, spawnCmd
	default:
		cmd := m.promptEditor.Update(msg)
		return m, cmd
	}
}
