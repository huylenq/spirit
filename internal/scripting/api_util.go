package scripting

import (
	"fmt"
	"io"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// registerUtilAPIs registers sleep() and log() into the VM.
func registerUtilAPIs(L *lua.LState, stderr io.Writer) {
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
}
