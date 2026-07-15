package copilot

import (
	"fmt"
	"strings"

	"github.com/huylenq/spirit/internal/laxicon"
)

// planTagPrefix is the Lulu-maintained session↔plan correlation tag convention
// (spec Decision 13): Lulu records an inferred or confirmed association as a
// `plan:<slug>` tag via set_tags. Spirit surfaces it; it never asserts one.
const planTagPrefix = "plan:"

// maxPlansPerProject bounds the fleet snapshot's per-project plan lines so a
// plan-heavy repo cannot flood Lulu's context; the remainder is counted.
const maxPlansPerProject = 8

// PlanTagSlug extracts the slug from a session's `plan:<slug>` tag, or "".
func PlanTagSlug(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, planTagPrefix) {
			return strings.TrimPrefix(tag, planTagPrefix)
		}
	}
	return ""
}

// BuildPlansBlock renders the intent-altitude companion to the fleet snapshot:
// per project root, the active (non-terminal-status) plans with progress
// tallies, plus spec names as held truth. Returns "" when no project has any
// laxicon documents, so the block simply doesn't exist for plan-less fleets.
// It rides the same digest-delta injection as the fleet snapshot — see
// laxicon.Fingerprint.
func BuildPlansBlock(projects []laxicon.ProjectPlans) string {
	var body strings.Builder
	for _, p := range projects {
		active := p.ActivePlans()
		if len(active) == 0 && len(p.Specs) == 0 {
			continue
		}
		fmt.Fprintf(&body, "%s\n", p.Root)
		shown := active
		if len(shown) > maxPlansPerProject {
			shown = shown[:maxPlansPerProject]
		}
		for _, d := range shown {
			body.WriteString("- ")
			body.WriteString(planLine(d))
			body.WriteString("\n")
		}
		if rest := len(active) - len(shown); rest > 0 {
			fmt.Fprintf(&body, "- (+%d more active plans)\n", rest)
		}
		if len(p.Specs) > 0 {
			var specs []string
			for _, s := range p.Specs {
				entry := s.Slug
				if s.Status != "" {
					entry += " [" + s.Status + "]"
				}
				specs = append(specs, entry)
			}
			fmt.Fprintf(&body, "specs: %s\n", strings.Join(specs, ", "))
		}
	}
	if body.Len() == 0 {
		return ""
	}
	return "<active-plans>\n" + body.String() + "</active-plans>"
}

// planLine renders one plan as `slug [status] 3/10 done — "Title"`.
func planLine(d laxicon.Doc) string {
	var b strings.Builder
	b.WriteString(d.Slug)
	if d.Status != "" {
		b.WriteString(" [" + d.Status + "]")
	}
	if tally := d.Progress.String(); tally != "" {
		b.WriteString(" " + tally)
	}
	if d.Title != "" && d.Title != d.Slug {
		b.WriteString(" — \"" + d.Title + "\"")
	}
	return b.String()
}

// dossierPlanSection renders the plan-awareness lines of the selected-session
// dossier (spec Decision 13). The `plan:<slug>` tag is the only asserted
// correlation — it is Lulu-maintained truth. Everything else is offered as a
// hint (project adjacency) and explicitly labeled as such, never asserted.
func dossierPlanSection(tags []string, plans *laxicon.ProjectPlans) []string {
	var lines []string
	slug := PlanTagSlug(tags)

	if slug != "" {
		if plans != nil {
			if d := plans.PlanBySlug(slug); d != nil {
				lines = append(lines, "plan: "+planLine(*d)+" (correlated via plan tag)")
				return lines
			}
		}
		lines = append(lines, fmt.Sprintf("plan: %s (tagged plan:%s, but no matching plan file found in the project)", slug, slug))
		return lines
	}

	if plans == nil {
		return nil
	}
	active := plans.ActivePlans()
	if len(active) == 0 {
		return nil
	}
	var slugs []string
	for i, d := range active {
		if i == 5 {
			slugs = append(slugs, fmt.Sprintf("+%d more", len(active)-i))
			break
		}
		slugs = append(slugs, d.Slug)
	}
	lines = append(lines, fmt.Sprintf(
		"plan-hint: project has active plans (%s) — cwd/branch adjacency only, not an asserted correlation; if you infer the plan this session serves, record it with set_tags as plan:<slug>",
		strings.Join(slugs, ", ")))
	return lines
}
