package laxicon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePlan writes root/laxicon/plans/<name> and returns its path.
func writePlan(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, "laxicon", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSpec(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, "laxicon", "specs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseDocFrontmatterVariants(t *testing.T) {
	cases := []struct {
		name    string
		content string
		status  string
		title   string
	}{
		{
			name:    "standard frontmatter",
			content: "---\nstatus: active\nwave: 6\n---\n\n# My Plan\n",
			status:  "active",
			title:   "My Plan",
		},
		{
			name:    "unknown keys ignored",
			content: "---\ncreated: 2026-07-15\nscope: cmd/spirit, internal/app\nstatus: adopted\nnested:\n  key: value\n- list item\n---\n## Thesis\n",
			status:  "adopted",
			title:   "Thesis",
		},
		{
			name:    "missing frontmatter tolerated",
			content: "# Graphify adoption — execution plan\n\nbody text\n",
			status:  "",
			title:   "Graphify adoption — execution plan",
		},
		{
			name:    "unclosed frontmatter fence treated as body",
			content: "---\nstatus: active\n# Heading After Broken Fence\n",
			status:  "",
			title:   "Heading After Broken Fence",
		},
		{
			name:    "empty file",
			content: "",
			status:  "",
			title:   "doc", // slug fallback
		},
		{
			name:    "status with extra colon keeps full value",
			content: "---\nstatus: active: really\n---\n",
			status:  "active: really",
			title:   "doc",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseDoc("/x/doc.md", KindPlan, []byte(tc.content))
			if doc.Status != tc.status {
				t.Errorf("status = %q, want %q", doc.Status, tc.status)
			}
			if doc.Title != tc.title {
				t.Errorf("title = %q, want %q", doc.Title, tc.title)
			}
			if doc.Slug != "doc" {
				t.Errorf("slug = %q, want doc", doc.Slug)
			}
		})
	}
}

func TestParseDocCheckboxTallies(t *testing.T) {
	content := `---
status: active
---

# Plan

- [x] done one
- [X] done two (capital)
- [~] deferred
- [ ] open one
  - [ ] nested open
* [x] star bullet done
+ [ ] plus bullet open
- [] not a checkbox (no space)
- plain list item
-[x] no space after bullet is not a checkbox
`
	doc := parseDoc("/x/p.md", KindPlan, []byte(content))
	if doc.Progress.Done != 3 {
		t.Errorf("done = %d, want 3", doc.Progress.Done)
	}
	if doc.Progress.Partial != 1 {
		t.Errorf("partial = %d, want 1", doc.Progress.Partial)
	}
	if doc.Progress.Open != 3 {
		t.Errorf("open = %d, want 3", doc.Progress.Open)
	}
	if got := doc.Progress.String(); got != "3/7 done, 1 in progress" {
		t.Errorf("progress string = %q", got)
	}
}

func TestProgressStringEmpty(t *testing.T) {
	if got := (Progress{}).String(); got != "" {
		t.Errorf("empty progress renders %q, want empty", got)
	}
}

func TestLoadDiscoversPlansAndSpecs(t *testing.T) {
	root := t.TempDir()
	writePlan(t, root, "b-plan.md", "---\nstatus: active\n---\n# B\n- [ ] item\n")
	writePlan(t, root, "a-plan.md", "---\nstatus: done\n---\n# A\n")
	writePlan(t, root, "notes.txt", "not markdown") // ignored
	writeSpec(t, root, "design.md", "---\nstatus: adopted\n---\n# Design\n")

	var r Reader
	pp := r.Load(root)
	if len(pp.Plans) != 2 {
		t.Fatalf("plans = %d, want 2", len(pp.Plans))
	}
	// Directory order is sorted by filename.
	if pp.Plans[0].Slug != "a-plan" || pp.Plans[1].Slug != "b-plan" {
		t.Errorf("plan order = %s, %s", pp.Plans[0].Slug, pp.Plans[1].Slug)
	}
	if len(pp.Specs) != 1 || pp.Specs[0].Slug != "design" || pp.Specs[0].Status != "adopted" {
		t.Errorf("specs = %+v", pp.Specs)
	}

	active := pp.ActivePlans()
	if len(active) != 1 || active[0].Slug != "b-plan" {
		t.Errorf("active plans = %+v, want only b-plan", active)
	}
}

func TestLoadMissingLaxiconDirIsEmpty(t *testing.T) {
	var r Reader
	pp := r.Load(t.TempDir())
	if !pp.Empty() {
		t.Errorf("expected empty inventory, got %+v", pp)
	}
	if pp2 := r.Load(""); !pp2.Empty() {
		t.Errorf("empty root should yield empty inventory")
	}
}

// A malformed or unreadable file must never break discovery of its siblings.
func TestLoadFailSoftPerFile(t *testing.T) {
	root := t.TempDir()
	writePlan(t, root, "good.md", "---\nstatus: active\n---\n# Good\n")
	// Malformed content parses fail-soft into a Doc.
	writePlan(t, root, "garbage.md", "\x00\xff binary---\nnot: yaml\n]] [[")
	// Unreadable file is skipped entirely.
	unreadable := writePlan(t, root, "secret.md", "---\nstatus: active\n---\n")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(unreadable, 0o644) }) //nolint:errcheck

	var r Reader
	pp := r.Load(root)
	if got := len(pp.Plans); got != 2 {
		t.Fatalf("plans = %d, want 2 (good + garbage; secret skipped)", got)
	}
	if pp.PlanBySlug("good") == nil {
		t.Error("good plan missing")
	}
	if g := pp.PlanBySlug("garbage"); g == nil || g.Status != "" {
		t.Errorf("garbage plan should parse fail-soft with empty status, got %+v", g)
	}
}

func TestReaderCachesByMtime(t *testing.T) {
	root := t.TempDir()
	path := writePlan(t, root, "p.md", "---\nstatus: active\n---\n# P\n- [ ] a\n")

	var r Reader
	r.Load(root)
	if r.parses != 1 {
		t.Fatalf("first load parses = %d, want 1", r.parses)
	}
	pp := r.Load(root)
	if r.parses != 1 {
		t.Fatalf("unchanged file re-parsed: parses = %d, want 1", r.parses)
	}
	if pp.Plans[0].Progress.Open != 1 {
		t.Fatalf("cached doc lost content: %+v", pp.Plans[0])
	}

	// Touch the file with new content and a bumped mtime: must re-parse.
	if err := os.WriteFile(path, []byte("---\nstatus: done\n---\n# P\n- [x] a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bump := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, bump, bump); err != nil {
		t.Fatal(err)
	}
	pp = r.Load(root)
	if r.parses != 2 {
		t.Fatalf("changed file not re-parsed: parses = %d, want 2", r.parses)
	}
	if pp.Plans[0].Status != "done" || pp.Plans[0].Progress.Done != 1 {
		t.Fatalf("stale cache served after change: %+v", pp.Plans[0])
	}
}

func TestLoadAllDedupesAndSorts(t *testing.T) {
	rootB := t.TempDir()
	rootA := t.TempDir()
	writePlan(t, rootA, "a.md", "# A\n")
	writePlan(t, rootB, "b.md", "# B\n")
	empty := t.TempDir() // no laxicon → dropped

	var r Reader
	got := r.LoadAll([]string{rootB, rootA, rootB, "", empty})
	if len(got) != 2 {
		t.Fatalf("projects = %d, want 2", len(got))
	}
	if !(got[0].Root < got[1].Root) {
		t.Errorf("projects not sorted by root: %s, %s", got[0].Root, got[1].Root)
	}
}

func TestResolveRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "laxicon"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveRoot(sub); got != root {
		t.Errorf("ResolveRoot(%s) = %q, want %q", sub, got, root)
	}

	// A .git file (worktree layout) also marks a root.
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveRoot(wt); got != wt {
		t.Errorf("ResolveRoot(worktree) = %q, want %q", got, wt)
	}

	if got := ResolveRoot(""); got != "" {
		t.Errorf("ResolveRoot(\"\") = %q, want empty", got)
	}
}

func TestFingerprintChangesOnMaterialChange(t *testing.T) {
	base := []ProjectPlans{{
		Root: "/repo",
		Plans: []Doc{
			{Slug: "p1", Kind: KindPlan, Status: "active", Progress: Progress{Done: 1, Open: 2}},
		},
		Specs: []Doc{{Slug: "s1", Kind: KindSpec, Status: "adopted"}},
	}}
	f0 := Fingerprint(base)
	if f0 != Fingerprint(base) {
		t.Fatal("fingerprint not deterministic")
	}

	statusChanged := []ProjectPlans{{
		Root:  "/repo",
		Plans: []Doc{{Slug: "p1", Kind: KindPlan, Status: "done", Progress: Progress{Done: 1, Open: 2}}},
		Specs: base[0].Specs,
	}}
	if Fingerprint(statusChanged) == f0 {
		t.Error("status change did not change fingerprint")
	}

	checkboxChanged := []ProjectPlans{{
		Root:  "/repo",
		Plans: []Doc{{Slug: "p1", Kind: KindPlan, Status: "active", Progress: Progress{Done: 2, Open: 1}}},
		Specs: base[0].Specs,
	}}
	if Fingerprint(checkboxChanged) == f0 {
		t.Error("checkbox change did not change fingerprint")
	}

	if Fingerprint(nil) != "" {
		t.Error("nil projects should fingerprint to empty")
	}
}
