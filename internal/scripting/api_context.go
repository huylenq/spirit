package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// registerContextAPIs registers selected() which returns the TUI's currently selected session ID.
func registerContextAPIs(L *lua.LState, ctx EvalContext) {
	L.SetGlobal("selected", L.NewFunction(func(L *lua.LState) int {
		if ctx.SelectedSessionID == "" {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(ctx.SelectedSessionID))
		return 1
	}))
}
