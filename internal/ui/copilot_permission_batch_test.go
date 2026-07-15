package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// W8: the approval overlay renders a batch payload as a legible operations
// list — targets + operations + risk — never an opaque JSON blob.

func TestRenderCopilotPermissionBatchList(t *testing.T) {
	p := CopilotPermission{
		PermissionID: "perm-1",
		Title:        "spirit - run_actions",
		Kind:         "execute",
		BatchSteps: []CopilotPermissionBatchStep{
			{Index: 1, Op: "queue", Target: "fix tests (aaaabbbb)", Detail: `queue "run the linter"`, Risk: "reversible"},
			{Index: 2, Op: "wait", Target: "fix tests (aaaabbbb)", Detail: "wait for phase cycle", Risk: "read_only"},
			{Index: 3, Op: "kill", Target: "old spike (ccccdddd)", Detail: "kill session", Risk: "destructive"},
		},
		Options: []CopilotPermissionOption{
			{OptionID: "allow_once", Kind: "allow_once", Name: "Allow once", Key: "y"},
			{OptionID: "deny", Kind: "reject_once", Name: "Deny", Key: "n"},
		},
		DeadlineUnix: time.Now().Add(30 * time.Second).Unix(),
	}
	out := ansi.Strip(RenderCopilotPermission(p, 100, time.Now()))

	for _, want := range []string{
		"batch: 3 step(s)",
		"1 destructive",
		"1. queue → fix tests (aaaabbbb)",
		"run the linter",
		"3. kill → old spike (ccccdddd)",
		"wait for phase cycle",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered batch missing %q:\n%s", want, out)
		}
	}
	// Legible list, not a JSON dump.
	if strings.Contains(out, "{") || strings.Contains(out, "session_id") {
		t.Fatalf("batch rendered as raw JSON:\n%s", out)
	}
	// The option accelerators and countdown still render.
	if !strings.Contains(out, "Allow once") || !strings.Contains(out, "auto-deny in") {
		t.Fatalf("options/countdown missing:\n%s", out)
	}
}

func TestRenderCopilotPermissionNonBatchUnchanged(t *testing.T) {
	p := CopilotPermission{
		PermissionID: "perm-2",
		Title:        "Run command",
		Kind:         "execute",
		Command:      "rm -rf build",
		Options: []CopilotPermissionOption{
			{OptionID: "allow_once", Kind: "allow_once", Name: "Allow once", Key: "y"},
		},
	}
	out := ansi.Strip(RenderCopilotPermission(p, 80, time.Now()))
	if !strings.Contains(out, "$ rm -rf build") {
		t.Fatalf("command prompt lost:\n%s", out)
	}
	if strings.Contains(out, "batch:") {
		t.Fatalf("non-batch prompt rendered a batch header:\n%s", out)
	}
}
