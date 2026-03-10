package scripting

import (
	"fmt"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

const defaultWaitTimeout = 300 // seconds

// registerSessionAPIs registers sessions(), session(), and wait() into the VM.
func registerSessionAPIs(L *lua.LState, client *daemon.Client) {
	L.SetGlobal("sessions", L.NewFunction(luaSessions(client)))
	L.SetGlobal("session", L.NewFunction(luaSession(client)))
	L.SetGlobal("wait", L.NewFunction(luaWait(client)))
}

// sessions() or sessions({status = "idle"})
func luaSessions(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		filter := ""
		if L.GetTop() >= 1 {
			opts := L.CheckTable(1)
			if s := opts.RawGetString("status"); s != lua.LNil {
				filter = s.String()
			}
		}

		sessions, err := client.Sessions(filter)
		if err != nil {
			L.RaiseError("sessions: %v", err)
			return 0
		}

		L.Push(sessionsToLuaTable(L, sessions))
		return 1
	}
}

// session("id") — returns single session or nil
func luaSession(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)

		sessions, err := client.Sessions("")
		if err != nil {
			L.RaiseError("session: %v", err)
			return 0
		}

		for _, s := range sessions {
			if s.SessionID == id {
				L.Push(sessionToTable(L, s))
				return 1
			}
		}

		L.Push(lua.LNil)
		return 1
	}
}

// wait(id) or wait(id, {timeout = 30})
// Blocks until session reaches idle (user-turn).
func luaWait(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		timeout := defaultWaitTimeout
		if L.GetTop() >= 2 {
			opts := L.CheckTable(2)
			if t := opts.RawGetString("timeout"); t != lua.LNil {
				timeout = int(lua.LVAsNumber(t))
			}
		}

		s, err := pollUntilStatus(client, id, claude.StatusUserTurn, timeout)
		if err != nil {
			L.RaiseError("wait: %v", err)
			return 0
		}

		L.Push(sessionToTable(L, *s))
		return 1
	}
}

// pollUntilStatus polls the daemon until a session reaches the target status.
func pollUntilStatus(client *daemon.Client, sessionID string, target claude.Status, timeoutSecs int) (*claude.ClaudeSession, error) {
	deadline := time.Now().Add(time.Duration(timeoutSecs) * time.Second)
	for time.Now().Before(deadline) {
		sessions, err := client.Sessions("")
		if err != nil {
			return nil, err
		}
		for _, s := range sessions {
			if s.SessionID == sessionID && s.Status == target {
				return &s, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("timeout waiting for session %s to reach %s", sessionID, target)
}
