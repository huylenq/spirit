package scripting

import (
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Msgs carries messages emitted by flash() and toast() during Lua script execution.
type Msgs struct {
	Flashes []string // each becomes a TUI footer flash (setFlash); CLI prints to stderr
	Toasts  []string // each becomes a TUI toast overlay entry; CLI prints to stderr
}

// sleep(seconds)
// Category: Utilities
// Pause execution for the given number of seconds.
func luaSleep(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		secs := L.CheckNumber(1)
		time.Sleep(time.Duration(float64(secs) * float64(time.Second)))
		return 0
	}
}

// log(...)
// Category: Utilities
// Print arguments to stderr (tab-separated). Not included in JSON output.
func luaLog(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		n := L.GetTop()
		for i := 1; i <= n; i++ {
			if i > 1 {
				fmt.Fprint(deps.Stderr, "\t")
			}
			fmt.Fprint(deps.Stderr, L.Get(i).String())
		}
		fmt.Fprintln(deps.Stderr)
		return 0
	}
}

// flash(msg)
// Category: Utilities
// Set TUI footer flash message. In CLI, prints to stderr.
func luaFlash(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		msg := L.CheckString(1)
		deps.Msgs.Flashes = append(deps.Msgs.Flashes, msg)
		return 0
	}
}

// toast(msg)
// Category: Utilities
// Add entry to TUI toast overlay. In CLI, prints to stderr.
func luaToast(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		msg := L.CheckString(1)
		deps.Msgs.Toasts = append(deps.Msgs.Toasts, msg)
		return 0
	}
}
