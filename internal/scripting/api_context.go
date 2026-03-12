package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// selected() -> string|nil
// Category: Context
// Return session ID currently selected in TUI. Returns nil from CLI.
func luaSelected(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		if deps.Ctx.SelectedSessionID == "" {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(deps.Ctx.SelectedSessionID))
		return 1
	}
}
