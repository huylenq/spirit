package scripting

import (
	"encoding/json"
	"fmt"

	"github.com/huylenq/spirit/internal/batch"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/runbook"
	lua "github.com/yuin/gopher-lua"
)

// Runbook + batch verbs (W8). Everything converges on the internal/batch
// schema: plan_actions/run_actions submit a raw batch; runbook_* drive the
// named-runbook pipeline whose execute phase EMITS a batch that rides the same
// action pipeline (one approval, per-action receipts, no second execution
// path).

// clientOps adapts the eval client to batch.Ops.
func clientOps(deps Deps) batch.Ops { return daemon.ClientOps{Client: deps.Client} }

// structToLua converts any JSON-marshalable Go value (plans, results,
// runbooks) into a Lua table via a JSON round trip, so Lua sees exactly the
// wire shape other surfaces see.
func structToLua(L *lua.LState, v any) (lua.LValue, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return lua.LNil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return lua.LNil, err
	}
	return jsonValueToLua(L, generic), nil
}

func jsonValueToLua(L *lua.LState, v any) lua.LValue {
	switch t := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(t)
	case float64:
		return lua.LNumber(t)
	case string:
		return lua.LString(t)
	case []any:
		tbl := L.NewTable()
		for _, item := range t {
			tbl.Append(jsonValueToLua(L, item))
		}
		return tbl
	case map[string]any:
		tbl := L.NewTable()
		for k, val := range t {
			tbl.RawSetString(k, jsonValueToLua(L, val))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", t))
	}
}

// stepsToBatchFromLua converts a Lua step array (and optional opts) to a Batch via
// the single schema's parser, so Lua-submitted batches obey exactly the same
// validation as JSON ones.
func stepsToBatchFromLua(steps *lua.LTable, opts *lua.LTable) (batch.Batch, error) {
	goSteps := luaTableToGo(steps)
	raw, err := json.Marshal(goSteps)
	if err != nil {
		return batch.Batch{}, err
	}
	b, err := batch.ParseBatch(raw)
	if err != nil {
		return batch.Batch{}, err
	}
	if opts != nil {
		if v := opts.RawGetString("on_error"); v != lua.LNil {
			b.OnError = batch.OnError(v.String())
			if b.OnError != batch.StopOnError && b.OnError != batch.ContinueOnError {
				return batch.Batch{}, fmt.Errorf("invalid on_error %q", v.String())
			}
		}
		if v := opts.RawGetString("resume_of"); v != lua.LNil {
			b.ResumeOf = v.String()
		}
	}
	return b, nil
}

// paramsFromLua reads an optional params table of string values.
func paramsFromLua(L *lua.LState, idx int) map[string]string {
	params := map[string]string{}
	if L.GetTop() >= idx {
		L.CheckTable(idx).ForEach(func(k, v lua.LValue) {
			params[k.String()] = v.String()
		})
	}
	return params
}

// plan_actions(steps) -> plan
// Category: Features
// Dry-run a batch of actions (W8): validates fail-fast (unknown session,
// capability-gated op, malformed step), resolves targets against the live
// fleet, and returns the ordered plan with a risk class per step (Decision 5)
// and approval points marked. Executes NOTHING. Steps use the shared schema:
// {op="send|queue|tag|note|later|kill|commit|spawn|wait", session_id=..., ...}.
func luaPlanActions(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		b, err := stepsToBatchFromLua(L.CheckTable(1), nil)
		if err != nil {
			L.RaiseError("plan_actions: %v", err)
			return 0
		}
		plan, err := batch.BuildPlan(clientOps(deps), b)
		if err != nil {
			L.RaiseError("plan_actions: %v", err)
			return 0
		}
		out, err := structToLua(L, plan)
		if err != nil {
			L.RaiseError("plan_actions: %v", err)
			return 0
		}
		L.Push(out)
		return 1
	}
}

// run_actions(steps, [{on_error, resume_of}]) -> result
// Category: Features
// Execute a batch of actions as ONE unit (W8): validates exactly like
// plan_actions (an invalid batch is rejected whole, never half-executed),
// then runs steps sequentially and returns one ActionReceipt per step.
// Partial failure: on_error="stop" (default) skips the steps after a failure
// and returns them verbatim in result.remainder — resume by resubmitting the
// remainder with resume_of=result.batch_id. on_error="continue" runs every
// step regardless.
func luaRunActions(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		var opts *lua.LTable
		if L.GetTop() >= 2 {
			opts = L.CheckTable(2)
		}
		b, err := stepsToBatchFromLua(L.CheckTable(1), opts)
		if err != nil {
			L.RaiseError("run_actions: %v", err)
			return 0
		}
		result, err := batch.Execute(clientOps(deps), b)
		if err != nil {
			L.RaiseError("run_actions: %v", err)
			return 0
		}
		out, err := structToLua(L, result)
		if err != nil {
			L.RaiseError("run_actions: %v", err)
			return 0
		}
		L.Push(out)
		return 1
	}
}

// runbooks() -> []runbook
// Category: Features
// List the named runbooks (~/.spirit/runbooks/*.lua plus builtins): name,
// description, declared params and action classes. A runbook's execute phase
// emits a batch that rides the same action pipeline as run_actions.
func luaRunbooks(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		out, err := structToLua(L, runbook.List())
		if err != nil {
			L.RaiseError("runbooks: %v", err)
			return 0
		}
		L.Push(out)
		return 1
	}
}

// runbook_explain(name) -> runbook
// Category: Features
// Explain a runbook without executing ANY of it (not even the build phase):
// metadata, declared params (required marked), and declared action classes.
func luaRunbookExplain(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		rb, err := runbook.Load(L.CheckString(1))
		if err != nil {
			L.RaiseError("runbook_explain: %v", err)
			return 0
		}
		out, err := structToLua(L, rb)
		if err != nil {
			L.RaiseError("runbook_explain: %v", err)
			return 0
		}
		L.Push(out)
		return 1
	}
}

// runbook_plan(name, [params]) -> plan
// Category: Features
// Dry-run a runbook (W8): runs its side-effect-free build phase over the live
// fleet snapshot and returns the emitted batch as a plan — targets resolved,
// risk classes marked, nothing executed.
func luaRunbookPlan(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		name := L.CheckString(1)
		_, plan, err := RunbookPlan(clientOps(deps), name, paramsFromLua(L, 2))
		if err != nil {
			L.RaiseError("runbook_plan: %v", err)
			return 0
		}
		out, err := structToLua(L, plan)
		if err != nil {
			L.RaiseError("runbook_plan: %v", err)
			return 0
		}
		L.Push(out)
		return 1
	}
}

// runbook_run(name, [params]) -> result
// Category: Features
// Execute a runbook: the build phase emits a batch which then rides the same
// action pipeline as run_actions — per-step ActionReceipts, stop-on-failure
// with a resubmittable remainder. Structured results, not terminal side
// effects.
func luaRunbookRun(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		name := L.CheckString(1)
		_, result, err := RunbookRun(clientOps(deps), name, paramsFromLua(L, 2))
		if err != nil {
			L.RaiseError("runbook_run: %v", err)
			return 0
		}
		out, err := structToLua(L, result)
		if err != nil {
			L.RaiseError("runbook_run: %v", err)
			return 0
		}
		L.Push(out)
		return 1
	}
}
