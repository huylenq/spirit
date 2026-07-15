package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
)

// planRepo creates a temp project root with one laxicon plan file and returns
// (root, planPath).
func planRepo(t *testing.T, content string) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "laxicon", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "w6-plan.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, path
}

// rewritePlan replaces the plan file's content with a bumped mtime so the
// laxicon reader's mtime cache sees the change.
func rewritePlan(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bump := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, bump, bump); err != nil {
		t.Fatal(err)
	}
}

// A plan change with an otherwise identical fleet is a material change: the
// fleet snapshot (with its <active-plans> block) must be re-injected exactly
// once, then suppressed again (W6 Track B — plan state joins the digest).
func TestFleetSnapshotDeltaReinjectsOnPlanChange(t *testing.T) {
	root, planPath := planRepo(t, "---\nstatus: active\n---\n# W6 Plan\n- [ ] step one\n- [ ] step two\n")

	d := &Daemon{}
	d.copilotPreamble.Store(true)
	sessions := []agent.Session{{SessionID: "sess-1", CWD: root, FirstMessage: "work"}}
	req := CopilotChatData{Message: "hi"}

	p1 := d.buildCopilotPrompt(req, nil, sessions)
	if !strings.Contains(p1, "<live-sessions") || !strings.Contains(p1, "<active-plans>") {
		t.Fatalf("first prompt should carry fleet snapshot + plans block:\n%s", p1)
	}
	if !strings.Contains(p1, "w6-plan [active] 0/2 done") {
		t.Fatalf("plans block missing parsed plan state:\n%s", p1)
	}

	if p2 := d.buildCopilotPrompt(req, nil, sessions); strings.Contains(p2, "<live-sessions") {
		t.Fatalf("unchanged fleet+plans re-injected the snapshot:\n%s", p2)
	}

	// A checkbox flips: material change, snapshot returns exactly once.
	rewritePlan(t, planPath, "---\nstatus: active\n---\n# W6 Plan\n- [x] step one\n- [ ] step two\n")
	p3 := d.buildCopilotPrompt(req, nil, sessions)
	if !strings.Contains(p3, "<active-plans>") || !strings.Contains(p3, "w6-plan [active] 1/2 done") {
		t.Fatalf("plan checkbox change should re-inject the snapshot with fresh tallies:\n%s", p3)
	}
	if p4 := d.buildCopilotPrompt(req, nil, sessions); strings.Contains(p4, "<live-sessions") {
		t.Fatalf("suppression should resume after the plan-change injection:\n%s", p4)
	}

	// A status flip to terminal is also material (the plan leaves the block).
	rewritePlan(t, planPath, "---\nstatus: done\n---\n# W6 Plan\n- [x] step one\n- [x] step two\n")
	p5 := d.buildCopilotPrompt(req, nil, sessions)
	if !strings.Contains(p5, "<live-sessions") {
		t.Fatalf("plan status change should re-inject the snapshot:\n%s", p5)
	}
	if strings.Contains(p5, "<active-plans>") {
		t.Fatalf("a done plan should no longer render an active-plans block:\n%s", p5)
	}
}

// The scoped dossier carries the selected session's project plans: the
// plan:<slug> tag as correlation, adjacency as a labeled hint otherwise.
func TestBuildCopilotPromptDossierPlanSection(t *testing.T) {
	root, _ := planRepo(t, "---\nstatus: active\n---\n# W6 Plan\n- [ ] step\n")

	d := &Daemon{}
	tagged := agent.Session{SessionID: "sess-1", CWD: root, Tags: []string{"plan:w6-plan"}}
	req := CopilotChatData{Message: "where does this fit?", Scope: &CopilotScope{SelectedSessionID: "sess-1"}}

	p := d.buildCopilotPrompt(req, &tagged, []agent.Session{tagged})
	if !strings.Contains(p, "plan: w6-plan [active] 0/1 done") || !strings.Contains(p, "(correlated via plan tag)") {
		t.Fatalf("dossier missing tag-correlated plan line:\n%s", p)
	}

	untagged := agent.Session{SessionID: "sess-2", CWD: root}
	req2 := CopilotChatData{Message: "review this", Scope: &CopilotScope{SelectedSessionID: "sess-2"}}
	p2 := d.buildCopilotPrompt(req2, &untagged, []agent.Session{untagged})
	if !strings.Contains(p2, "plan-hint:") || !strings.Contains(p2, "not an asserted correlation") {
		t.Fatalf("dossier missing labeled adjacency hint:\n%s", p2)
	}
}

// Worktree sessions resolve plans from the root project path — laxicon/ is
// untracked and exists only in the main checkout.
func TestPlanRootForWorktreeUsesRootProjectPath(t *testing.T) {
	root, _ := planRepo(t, "# P\n")
	wt := t.TempDir() // the worktree itself has no laxicon/

	s := agent.Session{SessionID: "s", CWD: wt, IsWorktree: true, WorktreeRootProjectPath: root}
	if got := planRootFor(s); got != root {
		t.Errorf("planRootFor(worktree session) = %q, want %q", got, root)
	}
}
