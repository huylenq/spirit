package scripting

import (
	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// registerLifecycleAPIs registers spawn() and kill() into the VM.
func registerLifecycleAPIs(L *lua.LState, client *daemon.Client) {
	L.SetGlobal("spawn", L.NewFunction(luaSpawn(client)))
	L.SetGlobal("kill", L.NewFunction(luaKill(client)))
}

// spawn("/path", {tmux_session = "main", message = "fix bug"})
func luaSpawn(client *daemon.Client) lua.LGFunction {
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

		result, err := client.Spawn(cwd, tmuxSession, message)
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
func luaKill(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := client.Kill(id); err != nil {
			L.RaiseError("kill: %v", err)
			return 0
		}
		return 0
	}
}
