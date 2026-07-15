package scripting

import (
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/receipt"
	lua "github.com/yuin/gopher-lua"
)

// mutationResult builds the standard structured return value for a mutating Lua
// function. Since W8 it speaks the receipt.ActionReceipt vocabulary —
// {ok, status, operation, target, action_id, outcome} — while keeping the
// legacy ok/status/target keys so existing scripts (and Gate A) stay green.
// Session-targeted verbs additionally attach observed post-action state via
// attachObserved. Extra fields can be attached by the caller before pushing.
func mutationResult(L *lua.LState, operation, target string) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("ok", lua.LBool(true))
	t.RawSetString("status", lua.LString("ok"))
	t.RawSetString("operation", lua.LString(operation))
	t.RawSetString("action_id", lua.LString(receipt.NewActionID()))
	t.RawSetString("outcome", lua.LString(string(receipt.OutcomeCompleted)))
	if target != "" {
		t.RawSetString("target", lua.LString(target))
	}
	return t
}

// setOutcome overrides the default "completed" delivery outcome (send →
// delivered, queue → queued), matching the MCP receipt semantics.
func setOutcome(t *lua.LTable, outcome receipt.Outcome) {
	t.RawSetString("outcome", lua.LString(string(outcome)))
}

// attachObserved captures the target session's post-action state for
// reconciliation (Decision 5): {status, is_waiting, queue_len, alive}.
// Alive=false after a kill is the expected observation. Best-effort — a
// fleet-read failure attaches nothing rather than failing the mutation result.
func attachObserved(L *lua.LState, deps Deps, t *lua.LTable, sessionID string) {
	if sessionID == "" {
		return
	}
	sessions, err := deps.Client.Sessions("")
	if err != nil {
		return
	}
	obs := L.NewTable()
	obs.RawSetString("alive", lua.LBool(false))
	for _, s := range sessions {
		if s.SessionID != sessionID {
			continue
		}
		obs.RawSetString("alive", lua.LBool(true))
		if s.Status == claude.StatusAgentTurn {
			obs.RawSetString("status", lua.LString("working"))
		} else {
			obs.RawSetString("status", lua.LString("idle"))
		}
		obs.RawSetString("is_waiting", lua.LBool(s.IsWaiting))
		obs.RawSetString("queue_len", lua.LNumber(len(s.QueuePending)))
		break
	}
	t.RawSetString("observed", obs)
}

// sessionToTable converts a ClaudeSession to a Lua table.
// NOTE: Field names are extracted by cmd/gen-lua-help via AST analysis of RawSetString calls.
// Keep field names as string literals (not variables/constants) or the generator will miss them.
func sessionToTable(L *lua.LState, s claude.ClaudeSession) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("id", lua.LString(s.SessionID))
	t.RawSetString("pane_id", lua.LString(s.PaneID))
	t.RawSetString("project", lua.LString(s.Project))
	t.RawSetString("project_code", lua.LString(s.ProjectCode))
	t.RawSetString("cwd", lua.LString(s.CWD))
	t.RawSetString("git_branch", lua.LString(s.GitBranch))
	t.RawSetString("tmux_session", lua.LString(s.TmuxSession))
	t.RawSetString("tmux_window", lua.LNumber(s.TmuxWindow))
	t.RawSetString("tmux_pane", lua.LNumber(s.TmuxPane))
	t.RawSetString("pid", lua.LNumber(s.PID))

	if s.Status == claude.StatusAgentTurn {
		t.RawSetString("status", lua.LString("working"))
	} else {
		t.RawSetString("status", lua.LString("idle"))
	}

	t.RawSetString("first_message", lua.LString(s.FirstMessage))
	t.RawSetString("last_user_message", lua.LString(s.LastUserMessage))
	t.RawSetString("synthesized_title", lua.LString(s.SynthesizedTitle))
	t.RawSetString("custom_title", lua.LString(s.CustomTitle))
	t.RawSetString("permission_mode", lua.LString(s.PermissionMode))
	t.RawSetString("stop_reason", lua.LString(s.StopReason))
	t.RawSetString("is_waiting", lua.LBool(s.IsWaiting))
	t.RawSetString("compact_count", lua.LNumber(s.CompactCount))
	t.RawSetString("commit_done_pending", lua.LBool(s.CommitDonePending))
	queueTable := L.NewTable()
	for _, msg := range s.QueuePending {
		queueTable.Append(lua.LString(msg))
	}
	t.RawSetString("queue_pending", queueTable)

	if !s.CreatedAt.IsZero() {
		t.RawSetString("created_at", lua.LNumber(s.CreatedAt.Unix()))
	}
	if !s.LastChanged.IsZero() {
		t.RawSetString("last_changed", lua.LNumber(s.LastChanged.Unix()))
	}

	// Display name (same priority as TUI)
	name := s.DisplayName()
	if name == "" {
		name = "(New session)"
	}
	t.RawSetString("display_name", lua.LString(name))

	return t
}

// luaValueToGo converts a Lua value to a Go value suitable for json.Marshal.
func luaValueToGo(v lua.LValue) any {
	switch v := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(v)
	case lua.LNumber:
		f := float64(v)
		if f == float64(int64(f)) {
			return int64(f)
		}
		return f
	case lua.LString:
		return string(v)
	case *lua.LTable:
		return luaTableToGo(v)
	default:
		return v.String()
	}
}

// luaTableToGo converts a Lua table to either a Go slice (if array-like) or map.
func luaTableToGo(t *lua.LTable) any {
	// Check if it's an array: sequential integer keys starting at 1
	maxN := t.MaxN()
	if maxN > 0 {
		// Verify there are no non-integer keys
		count := 0
		t.ForEach(func(k, v lua.LValue) {
			count++
		})
		if count == maxN {
			arr := make([]any, maxN)
			for i := 1; i <= maxN; i++ {
				arr[i-1] = luaValueToGo(t.RawGetInt(i))
			}
			return arr
		}
	}

	// Map
	m := make(map[string]any)
	t.ForEach(func(k, v lua.LValue) {
		m[k.String()] = luaValueToGo(v)
	})
	return m
}

// sessionsToLuaTable converts a slice of sessions to a Lua array table.
func sessionsToLuaTable(L *lua.LState, sessions []claude.ClaudeSession) *lua.LTable {
	t := L.NewTable()
	for _, s := range sessions {
		t.Append(sessionToTable(L, s))
	}
	return t
}

// backlogToTable converts a Backlog to a Lua table.
// NOTE: Field names are extracted by cmd/gen-lua-help via AST analysis (same as sessionToTable).
func backlogToTable(L *lua.LState, b claude.Backlog) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("id", lua.LString(b.ID))
	t.RawSetString("body", lua.LString(b.Body))
	t.RawSetString("cwd", lua.LString(b.CWD))
	t.RawSetString("project", lua.LString(b.Project))
	t.RawSetString("title", lua.LString(b.DisplayTitle()))
	t.RawSetString("created_at", lua.LNumber(float64(b.CreatedAt.Unix())))
	t.RawSetString("updated_at", lua.LNumber(float64(b.UpdatedAt.Unix())))
	return t
}
