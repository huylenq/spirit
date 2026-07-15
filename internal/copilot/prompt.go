package copilot

import (
	"fmt"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/laxicon"
)

// BuildSessionsPreamble constructs a lightweight preamble with just live session state.
// This is the only daemon-only data worth injecting — everything else the agent can
// fetch via spirit agent commands or already knows from Hermes' context.
func BuildSessionsPreamble(sessions []claude.ClaudeSession) string {
	var b strings.Builder
	b.WriteString("<live-sessions time=\"")
	b.WriteString(time.Now().Format("2006-01-02T15:04:05-07:00"))
	b.WriteString("\">\n")

	if len(sessions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, s := range sessions {
			b.WriteString(formatSession(s))
			b.WriteString("\n")
		}
	}
	b.WriteString("</live-sessions>")
	return b.String()
}

// maxDossierMessage bounds the last-user-intent excerpt so a long prompt can't
// blow the dossier's context budget.
const maxDossierMessage = 600

// BuildDossier constructs the request-scoped `<selected-session>` block for one
// Lulu turn (spec Decision 2). It is built from the daemon's fresh, validated
// copy of the session — not a name lookup — so "review this" / "tell it to fix
// it" resolves to exactly this session even when a title-match sibling exists.
// The block carries identity, lifecycle/queue state, provider/model, cwd/branch,
// display name, tags/note, waiting/overlap, and a bounded last-user-intent
// excerpt; heavier evidence (transcript tail, diff) is fetched on demand.
//
// view/lane/project are the originating client's local attention state; empty
// strings are omitted.
//
// plans is the laxicon inventory of the session's project root (nil when the
// project has none): the session's `plan:<slug>` tag is surfaced as the
// correlation when present, and project adjacency as an explicit hint (spec
// Decision 13 — Spirit hints, Lulu asserts via set_tags).
func BuildDossier(s claude.ClaudeSession, view, lane, project string, plans *laxicon.ProjectPlans) string {
	var b strings.Builder
	b.WriteString("<selected-session id=\"")
	b.WriteString(s.SessionID)
	b.WriteString("\">\n")

	name := s.DisplayName()
	if name == "" {
		name = "(new)"
	}
	line := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\n")
	}

	line("name", name)
	line("provider", string(s.Provider))
	line("status", sessionStatusLabel(s))
	line("lane", sessionLane(s))
	line("project", s.Project)
	line("branch", s.GitBranch)
	line("cwd", s.CWD)
	line("model", s.Model)
	if s.IsWorktree && s.WorktreeName != "" {
		line("worktree", s.WorktreeName)
	}

	var flags []string
	if s.IsWaiting {
		flags = append(flags, "waiting-for-input")
	}
	if s.HasOverlap {
		flags = append(flags, "file-overlap")
	}
	if s.CommitDonePending {
		flags = append(flags, "commit-pending")
	}
	if s.CompactCount > 0 {
		flags = append(flags, fmt.Sprintf("compacted:%d", s.CompactCount))
	}
	if len(flags) > 0 {
		line("flags", strings.Join(flags, ", "))
	}
	if len(s.QueuePending) > 0 {
		line("queued", fmt.Sprintf("%d message(s) pending delivery", len(s.QueuePending)))
	}
	if len(s.Tags) > 0 {
		line("tags", strings.Join(s.Tags, ", "))
	}
	line("note", s.Note)
	line("last-user-intent", truncate(s.LastUserMessage, maxDossierMessage))

	// Plan awareness: the intent altitude this session serves (Decision 13).
	for _, planLine := range dossierPlanSection(s.Tags, s.GitBranch, plans) {
		b.WriteString(planLine)
		b.WriteString("\n")
	}

	// Local UI attention context from the originating client.
	line("ui-view", view)
	line("ui-lane", lane)
	line("ui-project", project)

	b.WriteString("</selected-session>")
	return b.String()
}

// FleetDigest is a stable fingerprint of the fleet's material state, used to
// decide whether the fleet snapshot changed enough to re-inject into the
// persistent Hermes session. It intentionally excludes volatile fields (e.g.
// timestamps) so identical fleets across turns produce an identical digest.
func FleetDigest(sessions []claude.ClaudeSession) string {
	var b strings.Builder
	for _, s := range sessions {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%t|%t|%d\n",
			s.SessionID, sessionLane(s), sessionStatusLabel(s), s.DisplayName(),
			s.IsWaiting, s.HasOverlap, s.CompactCount)
	}
	return b.String()
}

// sessionStatusLabel maps a session's status to the coarse working/idle label
// used across Lulu's context.
func sessionStatusLabel(s claude.ClaudeSession) string {
	if s.Status == claude.StatusAgentTurn {
		return "working"
	}
	return "idle"
}

// sessionLane mirrors the sidebar's work-queue classification: a Later record
// keeps its session idle-but-parked, working sessions are their own lane, and
// everything else is awaiting the human.
func sessionLane(s claude.ClaudeSession) string {
	switch {
	case s.LaterID != "":
		return "later"
	case s.Status == claude.StatusAgentTurn:
		return "working"
	default:
		return "your-turn"
	}
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func formatSession(s claude.ClaudeSession) string {
	status := sessionStatusLabel(s)
	lane := sessionLane(s)

	name := s.DisplayName()
	if name == "" {
		name = "(new)"
	}

	var flags []string
	if s.IsWaiting {
		flags = append(flags, "waiting")
	}
	if s.HasOverlap {
		flags = append(flags, "overlap")
	}
	if s.CompactCount > 0 {
		flags = append(flags, fmt.Sprintf("compact:%d", s.CompactCount))
	}

	line := fmt.Sprintf("- [lane=%s, status=%s] %s %s/%s \"%s\"", lane, status, s.SessionID, s.Project, s.GitBranch, name)
	if len(flags) > 0 {
		line += " (" + strings.Join(flags, ", ") + ")"
	}
	return line
}
