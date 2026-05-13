package scripting

import (
	"fmt"

	"github.com/huylenq/spirit/internal/claude"
	lua "github.com/yuin/gopher-lua"
)

// send(id, msg, [{wait, timeout}])
// Category: Send & Wait
// Send message to session's tmux pane. Options: wait="idle"|"working"|"cycle", timeout=N.
// "cycle" waits until the session enters working then returns to idle (guards against
// pre-work false-idle right after sending a slash command).
func luaSend(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		msg := L.CheckString(2)

		if err := deps.Client.Send(id, msg); err != nil {
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

				s, err := waitForMode(deps, id, waitFor.String(), timeout)
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

// Wait modes accepted by send()/wait() — the Lua-visible vocabulary.
const (
	waitModeIdle    = "idle"
	waitModeWorking = "working"
	waitModeCycle   = "cycle"
)

func waitForMode(deps Deps, sessionID, mode string, timeoutSecs int) (*claude.ClaudeSession, error) {
	switch mode {
	case waitModeIdle:
		return pollUntilStatus(deps.Client, sessionID, claude.StatusUserTurn, timeoutSecs)
	case waitModeWorking:
		return pollUntilStatus(deps.Client, sessionID, claude.StatusAgentTurn, timeoutSecs)
	case waitModeCycle:
		return pollUntilCycle(deps.Client, sessionID, timeoutSecs)
	default:
		return nil, fmt.Errorf("invalid wait value %q (expected %q, %q, or %q)", mode, waitModeIdle, waitModeWorking, waitModeCycle)
	}
}

// queue(id, msg)
// Category: Send & Wait
// Queue message for delivery when session becomes idle.
func luaQueue(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		msg := L.CheckString(2)

		paneID := resolvePane(L, deps.Client, id)
		if err := deps.Client.Queue(paneID, id, msg); err != nil {
			L.RaiseError("queue: %v", err)
			return 0
		}
		return 0
	}
}

// cancel_queue(id, index)
// Category: Send & Wait
// Cancel a queued message by 1-based index.
func luaCancelQueue(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		idx := L.CheckInt(2) - 1 // convert from 1-based Lua to 0-based Go
		if err := deps.Client.CancelQueueItem(id, idx); err != nil {
			L.RaiseError("cancel_queue: %v", err)
			return 0
		}
		return 0
	}
}
