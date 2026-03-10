package scripting

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// RunEval executes a Lua script in a sandboxed VM with the cmc API available.
// Returns the JSON-encoded result of the last expression, or "" if no return value.
func RunEval(script string, client *daemon.Client, stderr io.Writer) (string, error) {
	L := newSandboxedVM()
	defer L.Close()

	// Register all API functions
	registerUtilAPIs(L, stderr)
	registerSessionAPIs(L, client)
	registerSendAPIs(L, client)
	registerLifecycleAPIs(L, client)
	registerOrchestratorAPIs(L, client)
	registerFeatureAPIs(L, client)

	// Try wrapping in anonymous function to capture return value
	wrapped := "return (function() " + script + " end)()"
	err := L.DoString(wrapped)
	if err != nil {
		// Fall back to raw execution (e.g. if script has syntax that doesn't
		// work inside a function wrapper)
		err = L.DoString(script)
		if err != nil {
			return "", err
		}
	}

	// Get return value from stack
	ret := L.Get(-1)
	if ret == nil || ret == lua.LNil {
		return "", nil
	}

	// Convert to JSON
	goVal := luaValueToGo(ret)
	if goVal == nil {
		return "", nil
	}

	data, err := json.Marshal(goVal)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(data), nil
}
