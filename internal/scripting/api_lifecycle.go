package scripting

import (
	"github.com/huylenq/spirit/internal/agent"
	lua "github.com/yuin/gopher-lua"
)

// spawn(cwd, [{provider, tmux_session, message, split_from_pane, remote_control}]) -> {ok, operation, session_id, pane_id}
// Category: Lifecycle
// Spawn a new session in the given directory. Blocks up to 30s. provider selects
// the agent ("claude" default, "codex", …); unknown providers are rejected by the
// daemon's provider registry. remote_control=true launches Claude with the native
// --remote-control flag and fails explicitly for unsupported providers. If
// split_from_pane is set (e.g. "%145"), the new pane is split next to it in the
// same tmux window; otherwise a new window is created.
func luaSpawn(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		tmuxSession := ""
		message := ""
		splitFromPane := ""
		remoteControl := false
		providerID := agent.ProviderClaude

		if L.GetTop() >= 2 {
			opts := L.CheckTable(2)
			if p := opts.RawGetString("provider"); p != lua.LNil {
				providerID = agent.ProviderID(p.String())
			}
			if s := opts.RawGetString("tmux_session"); s != lua.LNil {
				tmuxSession = s.String()
			}
			if m := opts.RawGetString("message"); m != lua.LNil {
				message = m.String()
			}
			if p := opts.RawGetString("split_from_pane"); p != lua.LNil {
				splitFromPane = p.String()
			}
			if rc := opts.RawGetString("remote_control"); rc != lua.LNil {
				remoteControl = lua.LVAsBool(rc)
			}
		}

		result, err := deps.Client.SpawnProvider(providerID, cwd, tmuxSession, message, splitFromPane, remoteControl)
		if err != nil {
			L.RaiseError("spawn: %v", err)
			return 0
		}

		t := mutationResult(L, "spawn", result.SessionID)
		t.RawSetString("session_id", lua.LString(result.SessionID))
		t.RawSetString("pane_id", lua.LString(result.PaneID))
		L.Push(t)
		return 1
	}
}

// kill(id) -> {ok, operation, target}
// Category: Lifecycle
// Send SIGTERM to session and clean up.
func luaKill(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := deps.Client.Kill(id); err != nil {
			L.RaiseError("kill: %v", err)
			return 0
		}
		result := mutationResult(L, "kill", id)
		attachObserved(L, deps, result, id) // alive=false is the expected observation
		L.Push(result)
		return 1
	}
}
