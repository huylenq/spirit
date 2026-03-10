package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// newSandboxedVM creates a Lua VM with restricted standard libraries.
// No os, io, debug, package — only base, table, string, math.
func newSandboxedVM() *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})

	// Open safe subset of standard libraries
	for _, pair := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		L.Push(L.NewFunction(pair.fn))
		L.Push(lua.LString(pair.name))
		L.Call(1, 0)
	}

	// Remove dangerous base functions
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring"} {
		L.SetGlobal(name, lua.LNil)
	}

	return L
}
