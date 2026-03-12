package app

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/scripting"
)

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
