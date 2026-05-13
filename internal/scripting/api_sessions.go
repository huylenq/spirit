package scripting

import (
	"fmt"
	"time"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

const defaultWaitTimeout = 300 // seconds

// sessions([{status}]) -> []session
// Category: Session Discovery
// List active sessions. Optional filter: status="idle"|"working".
func luaSessions(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		filter := ""
		if L.GetTop() >= 1 {
			opts := L.CheckTable(1)
			if s := opts.RawGetString("status"); s != lua.LNil {
				filter = s.String()
			}
		}

		sessions, err := deps.Client.Sessions(filter)
		if err != nil {
			L.RaiseError("sessions: %v", err)
			return 0
		}

		L.Push(sessionsToLuaTable(L, sessions))
		return 1
	}
}

// session(id) -> session|nil
// Category: Session Discovery
// Get a single session by ID, or nil if not found.
func luaSession(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)

		sessions, err := deps.Client.Sessions("")
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

// wait(id, [{mode, timeout}]) -> session
// Category: Session Discovery
// Block until session reaches a state. mode="idle" (default), "working", or "cycle"
// (waits for working then idle — useful right after sending a slash command).
// Default timeout 300s.
func luaWait(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		timeout := defaultWaitTimeout
		mode := waitModeIdle
		if L.GetTop() >= 2 {
			opts := L.CheckTable(2)
			if t := opts.RawGetString("timeout"); t != lua.LNil {
				timeout = int(lua.LVAsNumber(t))
			}
			if m := opts.RawGetString("mode"); m != lua.LNil {
				mode = m.String()
			}
		}

		s, err := waitForMode(deps, id, mode, timeout)
		if err != nil {
			L.RaiseError("wait: %v", err)
			return 0
		}

		L.Push(sessionToTable(L, *s))
		return 1
	}
}

// pollSession polls the daemon at 500ms cadence, applying done(session) on
// each tick. The predicate returns the session to surface back to the caller
// when satisfied; nil means "keep waiting". Returns timeoutErr on deadline.
func pollSession(client *daemon.Client, sessionID string, timeoutSecs int, timeoutErr error, done func(claude.ClaudeSession) *claude.ClaudeSession) (*claude.ClaudeSession, error) {
	deadline := time.Now().Add(time.Duration(timeoutSecs) * time.Second)
	for time.Now().Before(deadline) {
		sessions, err := client.Sessions("")
		if err != nil {
			return nil, err
		}
		for _, s := range sessions {
			if s.SessionID != sessionID {
				continue
			}
			if hit := done(s); hit != nil {
				return hit, nil
			}
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, timeoutErr
}

// pollUntilStatus polls the daemon until a session reaches the target status.
func pollUntilStatus(client *daemon.Client, sessionID string, target claude.Status, timeoutSecs int) (*claude.ClaudeSession, error) {
	return pollSession(client, sessionID, timeoutSecs,
		fmt.Errorf("timeout waiting for session %s to reach %s", sessionID, target),
		func(s claude.ClaudeSession) *claude.ClaudeSession {
			if s.Status == target {
				return &s
			}
			return nil
		})
}

// pollUntilCycle waits for the session to enter agent-turn (working) and then
// return to user-turn (idle). Guards against returning prematurely when the
// session hasn't started processing yet (e.g. right after sending a slash
// command).
func pollUntilCycle(client *daemon.Client, sessionID string, timeoutSecs int) (*claude.ClaudeSession, error) {
	sawWorking := false
	return pollSession(client, sessionID, timeoutSecs,
		fmt.Errorf("timeout waiting for session %s to complete cycle", sessionID),
		func(s claude.ClaudeSession) *claude.ClaudeSession {
			switch s.Status {
			case claude.StatusAgentTurn:
				sawWorking = true
			case claude.StatusUserTurn:
				if sawWorking {
					return &s
				}
			}
			return nil
		})
}
