package copilot

import (
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/laxicon"
)

func demoProjects() []laxicon.ProjectPlans {
	return []laxicon.ProjectPlans{
		{
			Root: "/repo/alpha",
			Plans: []laxicon.Doc{
				{Slug: "w6-plan", Kind: laxicon.KindPlan, Status: "active", Title: "W6 — holistic substrate", Progress: laxicon.Progress{Done: 3, Open: 7}},
				{Slug: "old-plan", Kind: laxicon.KindPlan, Status: "done", Title: "Old"},
			},
			Specs: []laxicon.Doc{
				{Slug: "design", Kind: laxicon.KindSpec, Status: "adopted", Title: "Design"},
			},
		},
		{
			Root:  "/repo/beta",
			Plans: []laxicon.Doc{{Slug: "beta-plan", Kind: laxicon.KindPlan, Title: "beta-plan"}}, // no status → live
		},
	}
}

func TestBuildPlansBlock(t *testing.T) {
	block := BuildPlansBlock(demoProjects())

	for _, want := range []string{
		"<active-plans>",
		"</active-plans>",
		"/repo/alpha",
		`w6-plan [active] 3/10 done — "W6 — holistic substrate"`,
		"specs: design [adopted]",
		"/repo/beta",
		"- beta-plan",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("plans block missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "old-plan") {
		t.Errorf("terminal-status plan leaked into active plans:\n%s", block)
	}
}

func TestBuildPlansBlockEmpty(t *testing.T) {
	if got := BuildPlansBlock(nil); got != "" {
		t.Errorf("no projects should render no block, got %q", got)
	}
	// A project whose plans are all terminal and has no specs renders nothing.
	got := BuildPlansBlock([]laxicon.ProjectPlans{{
		Root:  "/repo",
		Plans: []laxicon.Doc{{Slug: "p", Status: "done"}},
	}})
	if got != "" {
		t.Errorf("all-terminal project should render no block, got %q", got)
	}
}

func TestBuildPlansBlockCapsPerProject(t *testing.T) {
	var plans []laxicon.Doc
	for _, slug := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		plans = append(plans, laxicon.Doc{Slug: slug, Status: "active"})
	}
	block := BuildPlansBlock([]laxicon.ProjectPlans{{Root: "/repo", Plans: plans}})
	if !strings.Contains(block, "(+2 more active plans)") {
		t.Errorf("expected overflow counter for 10 plans (cap 8):\n%s", block)
	}
	if strings.Contains(block, "- i\n") || strings.Contains(block, "- j\n") {
		t.Errorf("plans beyond the cap should be counted, not listed:\n%s", block)
	}
}

func TestPlanTagSlug(t *testing.T) {
	if got := PlanTagSlug([]string{"urgent", "plan:w6-plan"}); got != "w6-plan" {
		t.Errorf("PlanTagSlug = %q, want w6-plan", got)
	}
	if got := PlanTagSlug([]string{"urgent"}); got != "" {
		t.Errorf("PlanTagSlug without plan tag = %q, want empty", got)
	}
}

func TestDossierPlanCorrelationViaTag(t *testing.T) {
	projects := demoProjects()
	s := agent.Session{SessionID: "sess-1", Tags: []string{"plan:w6-plan"}}

	dossier := BuildDossier(s, "", "", "", &projects[0])
	if !strings.Contains(dossier, `plan: w6-plan [active] 3/10 done — "W6 — holistic substrate" (correlated via plan tag)`) {
		t.Errorf("dossier missing tag-correlated plan line:\n%s", dossier)
	}
	if strings.Contains(dossier, "plan-hint") {
		t.Errorf("a tagged session should not also get the adjacency hint:\n%s", dossier)
	}
}

func TestDossierPlanTagWithoutMatchingFile(t *testing.T) {
	projects := demoProjects()
	s := agent.Session{SessionID: "sess-1", Tags: []string{"plan:ghost"}}

	dossier := BuildDossier(s, "", "", "", &projects[0])
	if !strings.Contains(dossier, "tagged plan:ghost, but no matching plan file") {
		t.Errorf("dossier should surface a dangling plan tag:\n%s", dossier)
	}
}

func TestDossierPlanHintIsExplicitlyAHint(t *testing.T) {
	projects := demoProjects()
	s := agent.Session{SessionID: "sess-1"} // no plan tag

	dossier := BuildDossier(s, "", "", "", &projects[0])
	if !strings.Contains(dossier, "plan-hint: project has active plans (w6-plan)") {
		t.Errorf("dossier missing adjacency hint:\n%s", dossier)
	}
	if !strings.Contains(dossier, "not an asserted correlation") {
		t.Errorf("hint must be labeled as a hint, never asserted truth:\n%s", dossier)
	}
	if !strings.Contains(dossier, "set_tags as plan:<slug>") {
		t.Errorf("hint should point at the set_tags correlation convention:\n%s", dossier)
	}
}

func TestDossierNoPlansNoLines(t *testing.T) {
	s := agent.Session{SessionID: "sess-1"}
	dossier := BuildDossier(s, "", "", "", nil)
	if strings.Contains(dossier, "plan") {
		t.Errorf("plan-less project should render no plan lines:\n%s", dossier)
	}
}
