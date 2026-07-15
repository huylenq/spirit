package scripting

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// TestMutationResult pins the structured shape returned by mutating Lua
// functions (the receipt seam). W3's ActionReceipt adoption should update this
// test deliberately.
func TestMutationResult(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tbl := mutationResult(L, "send", "sess-1")
	if got := tbl.RawGetString("ok"); got != lua.LTrue {
		t.Fatalf("ok = %v, want true", got)
	}
	if got := tbl.RawGetString("status").String(); got != "ok" {
		t.Fatalf("status = %q, want ok", got)
	}
	if got := tbl.RawGetString("operation").String(); got != "send" {
		t.Fatalf("operation = %q, want send", got)
	}
	if got := tbl.RawGetString("target").String(); got != "sess-1" {
		t.Fatalf("target = %q, want sess-1", got)
	}

	// Empty target is omitted (nil).
	noTarget := mutationResult(L, "synthesize", "")
	if got := noTarget.RawGetString("target"); got != lua.LNil {
		t.Fatalf("empty target should be omitted, got %v", got)
	}
}
