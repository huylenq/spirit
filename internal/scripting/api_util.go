package scripting

import (
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Msgs carries messages emitted during Lua script execution that the TUI
// applies after the script completes.
type Msgs struct {
	Flashes          []string // each becomes a TUI footer flash (setFlash); CLI prints to stderr
	Toasts           []string // each becomes a TUI toast overlay entry; CLI prints to stderr
	AutoJumpSkipPane string   // if non-empty, TUI will autoJump past this pane ID
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

// auto_jump(id)
// Category: Utilities
// Advance TUI selection past the given session (to the next idle/oldest).
// Honors the autoJump pref. Takes effect after the script returns.
func luaAutoJump(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		s := resolveSession(L, deps.Client, id)
		deps.Msgs.AutoJumpSkipPane = s.PaneID
		return 0
	}
}
