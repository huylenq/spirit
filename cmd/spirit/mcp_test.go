package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generated skill is deterministic and carries the full operation surface:
// the registry-driven tool list (including wait_session) and the W6 intent
// playbooks.
func TestGenHermesSkillMDDeterministicAndComplete(t *testing.T) {
	first := genHermesSkillMD()
	if second := genHermesSkillMD(); second != first {
		t.Fatal("skill generation is not deterministic")
	}

	for _, want := range []string{
		// registry-driven tools, including the W6 addition
		"`list_sessions`",
		"`wait_session`",
		// plan awareness
		"## Plan awareness",
		"<active-plans>",
		"plan:<slug>",
		"Spirit never writes plan files",
		// intent playbooks
		"## Intent playbooks",
		"### Review — verification brokering",
		"delegate_task",
		"### Triage — plan-grounded standup",
		"**unblock**",
		"### Plan hygiene — you are the PM of the board",
		"never edit a spec unprompted",
		"### Reconciliation — a receipt is not proof",
		"### Correlation — record what you infer",
		"set_tags",
		// global MCP promotion: tools-only scope, context bridge stays Copilot-only
		"## Scope: tools only — the Copilot context bridge is not MCP",
		"`spirit mcp install`",
		"no implicit selected session and no injected fleet context",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("generated skill missing %q", want)
		}
	}
}

// wait_session is a blocking observation, not a mutation: it must be listed
// under the read-only tools, not among the receipt-bearing side effects.
func TestGenHermesSkillMDWaitSessionIsReadOnly(t *testing.T) {
	skill := genHermesSkillMD()
	readIdx := strings.Index(skill, "### Read-only")
	effectIdx := strings.Index(skill, "### Side-effect")
	waitIdx := strings.Index(skill, "`wait_session`")
	if readIdx < 0 || effectIdx < 0 || waitIdx < 0 {
		t.Fatal("skill structure changed; sections not found")
	}
	if !(readIdx < waitIdx && waitIdx < effectIdx) {
		t.Error("wait_session not listed in the read-only section")
	}
}

func TestInstallHermesSkillIdempotent(t *testing.T) {
	t.Setenv("HERMES_HOME", t.TempDir())

	changed, err := installHermesSkill()
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if !changed {
		t.Fatal("first install should report a change")
	}

	path := filepath.Join(hermesSkillDir(), "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("installed skill unreadable: %v", err)
	}
	if string(content) != genHermesSkillMD() {
		t.Fatal("installed content diverges from the generator")
	}

	// Re-running with nothing changed is a no-op.
	changed, err = installHermesSkill()
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Fatal("unchanged content should not be rewritten")
	}

	// A drifted file (hand-edited, older version) is restored.
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = installHermesSkill()
	if err != nil {
		t.Fatalf("reinstall over drift: %v", err)
	}
	if !changed {
		t.Fatal("drifted content should be restored")
	}
}
