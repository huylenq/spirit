package scripting

import (
	"encoding/json"
	"fmt"

	"github.com/huylenq/spirit/internal/batch"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/runbook"
	lua "github.com/yuin/gopher-lua"
)

// The runbook build phase (W8, spec Decision 4): a runbook's Lua script
// COMPUTES a batch of steps; it can never execute one. Dry-run is therefore
// structural, not simulated — the build VM registers only snapshot-backed
// read functions (sessions/session), the params global, and log. The emitted
// steps ride the same batch plan/action pipeline as every other surface.

// BuildRunbookSteps runs a runbook script's build phase against a fleet
// snapshot and returns the batch steps it emits. The script returns either a
// bare step array or a table with an `actions` array. No daemon connection,
// no side-effect verbs, no clock — pure computation over (fleet, params).
func BuildRunbookSteps(script string, fleet []claude.ClaudeSession, params map[string]string) ([]batch.Step, error) {
	L := newSandboxedVM()
	defer L.Close()

	// sessions() -> fleet snapshot (same table shape as the full Lua API).
	L.SetGlobal("sessions", L.NewFunction(func(L *lua.LState) int {
		L.Push(sessionsToLuaTable(L, fleet))
		return 1
	}))
	// session(id) -> one session or nil.
	L.SetGlobal("session", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckString(1)
		for _, s := range fleet {
			if s.SessionID == id {
				L.Push(sessionToTable(L, s))
				return 1
			}
		}
		L.Push(lua.LNil)
		return 1
	}))
	// log(msg) -> no-op sink (kept so scripts can be debugged under eval later).
	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int { return 0 }))

	paramsTable := L.NewTable()
	for k, v := range params {
		paramsTable.RawSetString(k, lua.LString(v))
	}
	L.SetGlobal("params", paramsTable)

	wrapped := "return (function() " + script + " end)()"
	if err := L.DoString(wrapped); err != nil {
		return nil, fmt.Errorf("runbook build phase: %w", err)
	}
	ret := L.Get(-1)
	if ret == nil || ret == lua.LNil {
		return nil, fmt.Errorf("runbook build phase returned nothing (expected a step array)")
	}
	goVal := luaValueToGo(ret)
	raw, err := json.Marshal(goVal)
	if err != nil {
		return nil, fmt.Errorf("runbook build phase result: %w", err)
	}
	b, err := batch.ParseBatch(raw)
	if err != nil {
		return nil, fmt.Errorf("runbook build phase result: %w", err)
	}
	return b.Actions, nil
}

// checkDeclaredActions enforces the header contract: an emitted side-effect op
// the runbook did not declare rejects the run (fail-fast — the declaration is
// what surfaces use to mark a runbook destructive before running anything).
// The read-only wait op never needs declaring.
func checkDeclaredActions(rb runbook.Runbook, steps []batch.Step) error {
	declared := make(map[string]bool, len(rb.Actions))
	for _, a := range rb.Actions {
		declared[a] = true
	}
	for i, step := range steps {
		if step.Op == batch.OpWait {
			continue
		}
		if !declared[string(step.Op)] {
			return fmt.Errorf("runbook %s: step %d emits undeclared action %q (header declares: %v)", rb.Name, i+1, step.Op, rb.Actions)
		}
	}
	return nil
}

// RunbookSteps loads a runbook, validates params, runs the build phase over
// the live fleet, and returns the runbook plus its emitted steps. Shared by
// plan (dry-run) and run.
func RunbookSteps(ops batch.Ops, name string, params map[string]string) (runbook.Runbook, []batch.Step, error) {
	rb, err := runbook.Load(name)
	if err != nil {
		return rb, nil, err
	}
	if err := runbook.CheckParams(rb, params); err != nil {
		return rb, nil, err
	}
	fleet, err := ops.Sessions()
	if err != nil {
		return rb, nil, fmt.Errorf("resolve fleet: %w", err)
	}
	steps, err := BuildRunbookSteps(rb.Script, fleet, params)
	if err != nil {
		return rb, nil, err
	}
	if err := checkDeclaredActions(rb, steps); err != nil {
		return rb, nil, err
	}
	return rb, steps, nil
}

// RunbookPlan is the runbook dry-run: build phase + batch.BuildPlan. Nothing
// executes; the output IS a batch plan.
func RunbookPlan(ops batch.Ops, name string, params map[string]string) (runbook.Runbook, *batch.Plan, error) {
	rb, steps, err := RunbookSteps(ops, name, params)
	if err != nil {
		return rb, nil, err
	}
	plan, err := batch.BuildPlan(ops, batch.Batch{Actions: steps})
	if err != nil {
		return rb, nil, err
	}
	return rb, plan, nil
}

// RunbookRun executes a runbook: build phase, then the emitted batch through
// the shared action pipeline — per-step ActionReceipts, stop-on-failure with
// a resubmittable remainder.
func RunbookRun(ops batch.Ops, name string, params map[string]string) (runbook.Runbook, *batch.Result, error) {
	rb, steps, err := RunbookSteps(ops, name, params)
	if err != nil {
		return rb, nil, err
	}
	result, err := batch.Execute(ops, batch.Batch{Actions: steps})
	if err != nil {
		return rb, nil, err
	}
	return rb, result, nil
}
