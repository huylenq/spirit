package scripting

import (
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// registerSendAPIs registers send(), queue(), and cancel_queue() into the VM.
func registerSendAPIs(L *lua.LState, client *daemon.Client) {
	L.SetGlobal("send", L.NewFunction(luaSend(client)))
	L.SetGlobal("queue", L.NewFunction(luaQueue(client)))
	L.SetGlobal("cancel_queue", L.NewFunction(luaCancelQueue(client)))
}

// send(id, msg) or send(id, msg, {wait = "idle", timeout = 60})
func luaSend(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		msg := L.CheckString(2)

		if err := client.Send(id, msg); err != nil {
			L.RaiseError("send: %v", err)
			return 0
		}

		// Check for wait option
		if L.GetTop() >= 3 {
			opts := L.CheckTable(3)
			waitFor := opts.RawGetString("wait")
			if waitFor != lua.LNil {
				timeout := defaultWaitTimeout
				if t := opts.RawGetString("timeout"); t != lua.LNil {
					timeout = int(lua.LVAsNumber(t))
				}

				var target claude.Status
				switch waitFor.String() {
				case "idle":
					target = claude.StatusUserTurn
				case "working":
					target = claude.StatusAgentTurn
				default:
					L.RaiseError("send: invalid wait value %q (expected \"idle\" or \"working\")", waitFor.String())
					return 0
				}

				s, err := pollUntilStatus(client, id, target, timeout)
				if err != nil {
					L.RaiseError("send wait: %v", err)
					return 0
				}
				L.Push(sessionToTable(L, *s))
				return 1
			}
		}

		return 0
	}
}

// queue(id, msg)
func luaQueue(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		msg := L.CheckString(2)

		paneID := resolvePane(L, client, id)
		if err := client.Queue(paneID, id, msg); err != nil {
			L.RaiseError("queue: %v", err)
			return 0
		}
		return 0
	}
}

// cancel_queue(id, index) — index is 1-based (Lua convention)
func luaCancelQueue(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		idx := L.CheckInt(2) - 1 // convert from 1-based Lua to 0-based Go
		if err := client.CancelQueueItem(id, idx); err != nil {
			L.RaiseError("cancel_queue: %v", err)
			return 0
		}
		return 0
	}
}
