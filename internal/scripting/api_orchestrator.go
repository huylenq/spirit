package scripting

import (
	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// registerOrchestratorAPIs registers register_orchestrator() and unregister_orchestrator().
func registerOrchestratorAPIs(L *lua.LState, client *daemon.Client) {
	L.SetGlobal("register_orchestrator", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := client.RegisterOrchestrator(id); err != nil {
			L.RaiseError("register_orchestrator: %v", err)
		}
		return 0
	}))

	L.SetGlobal("unregister_orchestrator", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := client.UnregisterOrchestrator(id); err != nil {
			L.RaiseError("unregister_orchestrator: %v", err)
		}
		return 0
	}))
}
