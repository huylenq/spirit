package scripting

import (
	"github.com/huylenq/spirit/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// reactive_status() -> {enabled, paused, leased, durable_reactive, subscribers, gate_reason, quiet_hours_active, quiet_hours, llm_budget_remaining, llm_budget_total}
// Category: Features
// Read the durable-reactivity status (W9): whether durable reactivity is
// enabled/paused, whether a lease is held, why the reactive engine is currently
// eligible (gate_reason = subscriber | durable | none), the live subscriber
// count, quiet-hours state, and the remaining global daily provider budget.
// Read-only — enabling durable reactivity is a human act (spirit reactive
// enable), never a script/Lulu action.
func luaReactiveStatus(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		st, err := deps.Client.ReactiveStatus()
		if err != nil {
			L.RaiseError("reactive_status: %v", err)
			return 0
		}
		L.Push(reactiveStatusToTable(L, st))
		return 1
	}
}

// reactive_pause() -> status
// Category: Features
// Pause durable reactive processing: the lease is kept (the daemon stays awake,
// ingest continues, watches keep triggering and persisting) but nothing is
// claimed or dispatched. Returns the updated status. A thin wrapper over the
// control RPC — for Gate E automation and eval scripting.
func luaReactivePause(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		st, err := deps.Client.ReactiveControl("pause")
		if err != nil {
			L.RaiseError("reactive_pause: %v", err)
			return 0
		}
		L.Push(reactiveStatusToTable(L, st))
		return 1
	}
}

// reactive_resume() -> status
// Category: Features
// Resume durable reactive processing after a pause: already-triggered watches
// are processed on the next tick (deferred, not dropped). Returns the updated
// status.
func luaReactiveResume(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		st, err := deps.Client.ReactiveControl("resume")
		if err != nil {
			L.RaiseError("reactive_resume: %v", err)
			return 0
		}
		L.Push(reactiveStatusToTable(L, st))
		return 1
	}
}

func reactiveStatusToTable(L *lua.LState, st daemon.ReactiveStatusData) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("enabled", lua.LBool(st.Enabled))
	t.RawSetString("paused", lua.LBool(st.Paused))
	t.RawSetString("leased", lua.LBool(st.Leased))
	t.RawSetString("durable_reactive", lua.LBool(st.DurableReactive))
	t.RawSetString("subscribers", lua.LNumber(st.Subscribers))
	t.RawSetString("gate_reason", lua.LString(st.GateReason))
	t.RawSetString("quiet_hours_active", lua.LBool(st.QuietHoursActive))
	t.RawSetString("quiet_hours", lua.LString(st.QuietHours))
	t.RawSetString("llm_budget_remaining", lua.LNumber(st.LLMBudgetRemaining))
	t.RawSetString("llm_budget_total", lua.LNumber(st.LLMBudgetTotal))
	return t
}
