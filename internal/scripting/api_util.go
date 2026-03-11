package scripting

import (
	"fmt"
	"io"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Msgs carries messages emitted by flash() and toast() during Lua script execution.
type Msgs struct {
	Flashes []string // each becomes a TUI footer flash (setFlash); CLI prints to stderr
	Toasts  []string // each becomes a TUI toast overlay entry; CLI prints to stderr
}

// registerUtilAPIs registers sleep(), log(), flash(), and toast() into the VM.
func registerUtilAPIs(L *lua.LState, stderr io.Writer, msgs *Msgs) {
	L.SetGlobal("sleep", L.NewFunction(func(L *lua.LState) int {
		secs := L.CheckNumber(1)
		time.Sleep(time.Duration(float64(secs) * float64(time.Second)))
		return 0
	}))

	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int {
		n := L.GetTop()
		for i := 1; i <= n; i++ {
			if i > 1 {
				fmt.Fprint(stderr, "\t")
			}
			fmt.Fprint(stderr, L.Get(i).String())
		}
		fmt.Fprintln(stderr)
		return 0
	}))

	// flash(msg) — sets the TUI footer flash bar. In CLI, prints to stderr.
	L.SetGlobal("flash", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		msgs.Flashes = append(msgs.Flashes, msg)
		return 0
	}))

	// toast(msg) — adds an entry to the TUI toast overlay. In CLI, prints to stderr.
	L.SetGlobal("toast", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		msgs.Toasts = append(msgs.Toasts, msg)
		return 0
	}))
}
