package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// spawn(cwd, [{tmux_session, message}]) -> {session_id, pane_id}
// Category: Lifecycle
// Spawn a new Claude session in the given directory. Blocks up to 30s.
func luaSpawn(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		tmuxSession := ""
		message := ""

		if L.GetTop() >= 2 {
			opts := L.CheckTable(2)
			if s := opts.RawGetString("tmux_session"); s != lua.LNil {
				tmuxSession = s.String()
			}
			if m := opts.RawGetString("message"); m != lua.LNil {
				message = m.String()
			}
		}

		result, err := deps.Client.Spawn(cwd, tmuxSession, message)
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
