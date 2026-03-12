package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// register_orchestrator(id)
// Category: Orchestrator
// Exclude session from sessions() results. For orchestrator self-exclusion.
func luaRegisterOrchestrator(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := deps.Client.RegisterOrchestrator(id); err != nil {
			L.RaiseError("register_orchestrator: %v", err)
		}
		return 0
	}
}

// unregister_orchestrator(id)
// Category: Orchestrator
// Re-include a previously excluded session in sessions() results.
func luaUnregisterOrchestrator(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := deps.Client.UnregisterOrchestrator(id); err != nil {
			L.RaiseError("unregister_orchestrator: %v", err)
		}
		return 0
	}
}
