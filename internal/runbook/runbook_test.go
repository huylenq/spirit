package runbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHeader(t *testing.T) {
	script := `-- name: park-idle
-- description: Park idle sessions of a project
-- param: project! repo root path
-- param: note optional note to attach
-- actions: later, note

local steps = {}
return steps`
	name, desc, params, actions := ParseHeader(script)
	if name != "park-idle" || desc != "Park idle sessions of a project" {
		t.Fatalf("name/desc = %q / %q", name, desc)
	}
	if len(params) != 2 || params[0].Name != "project" || !params[0].Required || params[0].Description != "repo root path" {
		t.Fatalf("params = %+v", params)
	}
	if params[1].Name != "note" || params[1].Required {
		t.Fatalf("params[1] = %+v", params[1])
	}
	if len(actions) != 2 || actions[0] != "later" || actions[1] != "note" {
		t.Fatalf("actions = %+v", actions)
	}
}

func TestParseHeaderStopsAtCode(t *testing.T) {
	script := "local x = 1\n-- name: sneaky\nreturn {}"
	name, _, _, _ := ParseHeader(script)
	if name != "" {
		t.Fatalf("header parsed past code: %q", name)
	}
}

func TestListUserOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	restore := OverrideDirForTest(dir)
	defer restore()

	// The builtin broadcast is visible.
	rb, err := Load("broadcast")
	if err != nil || !rb.BuiltIn {
		t.Fatalf("builtin broadcast: %+v, %v", rb, err)
	}
	if !containsAction(rb.Actions, "queue") {
		t.Fatalf("broadcast actions = %v", rb.Actions)
	}

	// A user file with the same name wins.
	user := "-- name: broadcast\n-- description: user override\n-- actions: note\nreturn {}"
	if err := os.WriteFile(filepath.Join(dir, "broadcast.lua"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	rb, err = Load("broadcast")
	if err != nil || rb.BuiltIn || rb.Description != "user override" {
		t.Fatalf("user override not applied: %+v, %v", rb, err)
	}
}

func TestLoadUnknownIsPreciseError(t *testing.T) {
	restore := OverrideDirForTest(t.TempDir())
	defer restore()
	_, err := Load("no-such-runbook")
	if err == nil || !strings.Contains(err.Error(), "no-such-runbook") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckParams(t *testing.T) {
	rb := Runbook{Name: "x", Params: []Param{{Name: "message", Required: true}, {Name: "project"}}}
	if err := CheckParams(rb, map[string]string{"project": "/x"}); err == nil || !strings.Contains(err.Error(), "message") {
		t.Fatalf("missing required param must fail: %v", err)
	}
	if err := CheckParams(rb, map[string]string{"message": "hi", "typo": "y"}); err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("unknown param must fail: %v", err)
	}
	if err := CheckParams(rb, map[string]string{"message": "hi"}); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
}

func TestDestructive(t *testing.T) {
	if (Runbook{Actions: []string{"queue", "note"}}).Destructive() {
		t.Fatal("queue/note is not destructive")
	}
	if !(Runbook{Actions: []string{"queue", "kill"}}).Destructive() {
		t.Fatal("kill declaration is destructive")
	}
}

func containsAction(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}
