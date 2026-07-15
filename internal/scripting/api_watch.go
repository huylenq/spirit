package scripting

import (
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/ledger"
	lua "github.com/yuin/gopher-lua"
)

// watch(id, [{condition, response, project, action_id, expires_in_minutes, cooldown_seconds, max_firings, llm_budget}]) -> watch
// Category: Features
// Create a reactive watch on a session (W7). Defaults: condition
// "completed_turn", response "inspect_and_recommend", 24h expiry, 60s
// cooldown, 20 firings. Pass "" as id with opts.project for a project-wide
// watch, or "" with no project for fleet-wide. opts.action_id (with condition
// "action_reconciled") anchors the watch to ONE action — e.g. a batch step's
// action_id — firing exactly when that action's delivery/failure signal lands.
// While a TUI client is attached, Spirit reacts: inbox records, notify raises
// one coalesced notification, inspect_and_recommend attaches a bounded LLM
// proposal to the attention item.
func luaWatch(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		req := daemon.WatchCreateData{
			SessionID: id,
			Condition: string(ledger.ConditionCompletedTurn),
			Response:  string(ledger.ResponseRecommend),
			CreatedBy: "lua",
			// Traceability: the Lua surface has no turn correlation, so the
			// request id is minted here per creation.
			CreatedByRequestID: daemon.NewCorrelationID(),
		}
		if L.GetTop() >= 2 {
			opts := L.CheckTable(2)
			if v := opts.RawGetString("condition"); v != lua.LNil {
				req.Condition = v.String()
			}
			if v := opts.RawGetString("response"); v != lua.LNil {
				req.Response = v.String()
			}
			if v := opts.RawGetString("project"); v != lua.LNil {
				req.Project = v.String()
			}
			if v := opts.RawGetString("action_id"); v != lua.LNil {
				req.ActionID = v.String()
			}
			if v, ok := opts.RawGetString("expires_in_minutes").(lua.LNumber); ok {
				req.ExpiresInMinutes = int(v)
			}
			if v, ok := opts.RawGetString("cooldown_seconds").(lua.LNumber); ok {
				req.CooldownSeconds = int(v)
			}
			if v, ok := opts.RawGetString("max_firings").(lua.LNumber); ok {
				req.MaxFirings = int(v)
			}
			if v, ok := opts.RawGetString("llm_budget").(lua.LNumber); ok {
				req.LLMBudget = int(v)
			}
		}
		w, err := deps.Client.WatchCreate(req)
		if err != nil {
			L.RaiseError("watch: %v", err)
			return 0
		}
		L.Push(watchToTable(L, w))
		return 1
	}
}

// watches() -> []watch
// Category: Features
// List reactive watches: scope, condition, response, FSM state, firing/LLM
// budgets, last outcome. Includes recently expired/cancelled/failed watches.
func luaWatches(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		watches, err := deps.Client.WatchList()
		if err != nil {
			L.RaiseError("watches: %v", err)
			return 0
		}
		t := L.NewTable()
		for _, w := range watches {
			t.Append(watchToTable(L, w))
		}
		L.Push(t)
		return 1
	}
}

// unwatch(watch_id) -> {ok, operation, target}
// Category: Features
// Cancel a live reactive watch by its watch_id.
func luaUnwatch(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if _, err := deps.Client.WatchCancel(id); err != nil {
			L.RaiseError("unwatch: %v", err)
			return 0
		}
		L.Push(mutationResult(L, "unwatch", id))
		return 1
	}
}

// attention() -> {items=[]item, watches=[]watch}
// Category: Features
// The attention inbox: unresolved attention items (with description, any
// recommendation, and the causal audit chain) plus all known watches.
func luaAttention(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		data, err := deps.Client.AttentionList()
		if err != nil {
			L.RaiseError("attention: %v", err)
			return 0
		}
		out := L.NewTable()
		items := L.NewTable()
		for _, it := range data.Items {
			items.Append(attentionItemToTable(L, it, data.Descriptions[it.ID]))
		}
		out.RawSetString("items", items)
		watches := L.NewTable()
		for _, w := range data.Watches {
			watches.Append(watchToTable(L, w))
		}
		out.RawSetString("watches", watches)
		L.Push(out)
		return 1
	}
}

// watchToTable converts a ledger.Watch to a Lua table.
func watchToTable(L *lua.LState, w ledger.Watch) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("watch_id", lua.LString(w.ID))
	t.RawSetString("session_id", lua.LString(w.Scope.SessionID))
	t.RawSetString("project", lua.LString(w.Scope.Project))
	t.RawSetString("action_id", lua.LString(w.Scope.ActionID))
	t.RawSetString("condition", lua.LString(string(w.Condition)))
	t.RawSetString("response", lua.LString(string(w.Response)))
	t.RawSetString("autonomy_level", lua.LString(w.AutonomyLevel))
	t.RawSetString("state", lua.LString(string(w.State)))
	t.RawSetString("firings", lua.LNumber(w.Firings))
	t.RawSetString("max_firings", lua.LNumber(w.MaxFirings))
	t.RawSetString("llm_used", lua.LNumber(w.LLMUsed))
	t.RawSetString("llm_budget", lua.LNumber(w.LLMBudget))
	t.RawSetString("cooldown_seconds", lua.LNumber(w.CooldownSeconds))
	t.RawSetString("last_outcome", lua.LString(w.LastOutcome))
	t.RawSetString("created_by", lua.LString(w.CreatedBy))
	t.RawSetString("created_by_request_id", lua.LString(w.CreatedByRequestID))
	if !w.ExpiresAt.IsZero() {
		t.RawSetString("expires_at", lua.LNumber(w.ExpiresAt.Unix()))
	}
	return t
}

// attentionItemToTable converts a ledger.AttentionItem (+ its one-line
// description) to a Lua table, including the causal audit chain.
func attentionItemToTable(L *lua.LState, it ledger.AttentionItem, desc string) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("id", lua.LString(it.ID))
	t.RawSetString("category", lua.LString(string(it.Category)))
	t.RawSetString("severity", lua.LString(string(it.Severity)))
	t.RawSetString("status", lua.LString(string(it.Status)))
	t.RawSetString("session_id", lua.LString(it.Scope.SessionID))
	t.RawSetString("project", lua.LString(it.Scope.Project))
	t.RawSetString("description", lua.LString(desc))
	t.RawSetString("recommendation", lua.LString(it.Recommendation))
	t.RawSetString("resolution", lua.LString(it.Resolution))
	signals := L.NewTable()
	for _, sid := range it.SignalIDs {
		signals.Append(lua.LString(sid))
	}
	t.RawSetString("signal_ids", signals)
	audit := L.NewTable()
	for _, ev := range it.Audit {
		row := L.NewTable()
		row.RawSetString("kind", lua.LString(ev.Kind))
		row.RawSetString("watch_id", lua.LString(ev.WatchID))
		row.RawSetString("detail", lua.LString(ev.Detail))
		row.RawSetString("at", lua.LNumber(ev.At.Unix()))
		audit.Append(row)
	}
	t.RawSetString("audit", audit)
	if !it.CreatedAt.IsZero() {
		t.RawSetString("created_at", lua.LNumber(it.CreatedAt.Unix()))
	}
	return t
}
