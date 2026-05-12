package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// spawn(cwd, [{tmux_session, message, split_from_pane}]) -> {session_id, pane_id}
// Category: Lifecycle
// Spawn a new Claude session in the given directory. Blocks up to 30s.
// If split_from_pane is set (e.g. "%145"), the new pane is split next to it
// in the same tmux window; otherwise a new window is created.
func luaSpawn(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		tmuxSession := ""
		message := ""
		splitFromPane := ""

		if L.GetTop() >= 2 {
			opts := L.CheckTable(2)
			if s := opts.RawGetString("tmux_session"); s != lua.LNil {
				tmuxSession = s.String()
			}
			if m := opts.RawGetString("message"); m != lua.LNil {
				message = m.String()
			}
			if p := opts.RawGetString("split_from_pane"); p != lua.LNil {
				splitFromPane = p.String()
			}
		}

		result, err := deps.Client.Spawn(cwd, tmuxSession, message, splitFromPane)
		if err != nil {
			L.RaiseError("spawn: %v", err)
			return 0
		}

		t := L.NewTable()
		t.RawSetString("session_id", lua.LString(result.SessionID))
		t.RawSetString("pane_id", lua.LString(result.PaneID))
		L.Push(t)
		return 1
	}
}

// kill(id)
// Category: Lifecycle
// Send SIGTERM to session and clean up.
func luaKill(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := deps.Client.Kill(id); err != nil {
			L.RaiseError("kill: %v", err)
			return 0
		}
		return 0
	}
}
