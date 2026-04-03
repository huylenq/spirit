package scripting

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/huylenq/spirit/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// EvalContext carries TUI context into Lua script execution.
type EvalContext struct {
	SelectedSessionID string // session ID of the currently selected session (empty from CLI)
}

// Deps bundles all dependencies for Lua API functions.
type Deps struct {
	Client *daemon.Client
	Stderr io.Writer
	Msgs   *Msgs
	Ctx    EvalContext
}

//go:generate go run ../../cmd/gen-lua-help

// RunEval executes a Lua script in a sandboxed VM with the spirit API available.
// Returns the JSON-encoded result, any flash/toast messages emitted, and any error.
func RunEval(script string, client *daemon.Client, stderr io.Writer) (string, Msgs, error) {
	return RunEvalWithContext(script, client, stderr, EvalContext{})
}

// RunEvalWithContext executes a Lua script with additional TUI context (e.g. selected session).
func RunEvalWithContext(script string, client *daemon.Client, stderr io.Writer, ctx EvalContext) (string, Msgs, error) {
	L := newSandboxedVM()
	defer L.Close()

	var msgs Msgs

	// Register all API functions
	deps := Deps{Client: client, Stderr: stderr, Msgs: &msgs, Ctx: ctx}
	registerAllAPIs(L, deps)

	// Try wrapping in anonymous function to capture return value
	wrapped := "return (function() " + script + " end)()"
	err := L.DoString(wrapped)
	if err != nil {
		// Fall back to raw execution (e.g. if script has syntax that doesn't
		// work inside a function wrapper)
		err = L.DoString(script)
		if err != nil {
			return "", msgs, err
		}
	}

	// Get return value from stack
	ret := L.Get(-1)
	if ret == nil || ret == lua.LNil {
		return "", msgs, nil
	}

	// Convert to JSON
	goVal := luaValueToGo(ret)
	if goVal == nil {
		return "", msgs, nil
	}

	data, err := json.Marshal(goVal)
	if err != nil {
		return "", msgs, fmt.Errorf("marshal result: %w", err)
	}
	return string(data), msgs, nil
}
