package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/scripting"
)

// handleKeyMacro handles input when the macro palette is shown.
func (m Model) handleKeyMacro(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		return m, nil

	case msg.String() == "=" || msg.String() == "+":
		m.state = StateMacroEdit
		m.macroEditor.Activate()
		return m, nil

	default:
		k := msg.String()

		// alt+<key>: open macro in $EDITOR
		if after, ok := strings.CutPrefix(k, "alt+"); ok && len(after) == 1 {
			m.state = StateNormal
			// createIfMissing=true so alt+<new key> bootstraps a template file
			return m, m.openMacroInEditor(after, true)
		}

		// Single printable character: look up and run macro
		if len(k) == 1 {
			for _, macro := range m.macros {
				if macro.Key == k {
					m.state = StateNormal
					return m, m.execMacro(macro)
				}
			}
			// Unknown key — flash and dismiss
			m.state = StateNormal
			return m, m.setFlash("no macro for '"+k+"'", true, 2*time.Second)
		}
		return m, nil
	}
}

// handleKeyMacroEdit handles input when the macro editor is shown.
func (m Model) handleKeyMacroEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.macroEditor.Deactivate()
		m.state = StateNormal
		return m, nil

	case msg.String() == "ctrl+s":
		k, name, body := m.macroEditor.ParseHeader()
		if k == "" {
			return m, m.setFlash("macro key is required (-- key: x)", true, 3*time.Second)
		}
		if len(k) != 1 {
			return m, m.setFlash("macro key must be a single character", true, 3*time.Second)
		}
		if name == "" {
			name = k
		}
		if err := claude.SaveMacro(k, name, body); err != nil {
			return m, m.setFlash("save macro: "+err.Error(), true, 3*time.Second)
		}
		m.macros = claude.LoadMacros(nil)
		m.macroEditor.Deactivate()
		m.state = StateNormal
		return m, m.setFlash("macro '"+k+"' saved", false, 3*time.Second)

	default:
		cmd := m.macroEditor.Update(msg)
		return m, cmd
	}
}

// execMacro runs a macro's Lua script with the selected session context.
func (m Model) execMacro(macro claude.Macro) tea.Cmd {
	ctx := scripting.EvalContext{}
	if s, ok := m.sidebar.SelectedItem(); ok {
		ctx.SelectedSessionID = s.SessionID
	}
	return evalLuaWithContext(m.client, macro.Script, ctx)
}

// openMacroInEditor opens a macro file in $EDITOR via tmux split-window.
// If createIfMissing is true and the file does not exist, a template is written first.
func (m Model) openMacroInEditor(macroKey string, createIfMissing bool) tea.Cmd {
	path := claude.MacroFilePath(macroKey)
	tmuxSession := m.origPane.Session
	return func() tea.Msg {
		if createIfMissing {
			if err := claude.SaveMacro(macroKey, "", ""); err != nil {
				return flashErrorMsg("create macro: " + err.Error())
			}
		}
		if err := openInEditorSplit(path, tmuxSession); err != nil {
			return flashErrorMsg("open editor: " + err.Error())
		}
		return MacroEditorExitedMsg{}
	}
}

// openInEditorSplit opens a file in $EDITOR (defaulting to vim) in a new tmux split-window.
func openInEditorSplit(path, tmuxSession string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	return exec.Command("tmux", "split-window", "-t", tmuxSession,
		fmt.Sprintf("%s %s", editor, path)).Run()
}
