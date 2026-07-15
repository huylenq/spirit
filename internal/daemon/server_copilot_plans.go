package daemon

// Plan awareness (W6 Track B, spec Decision 13): the daemon-side glue between
// live sessions and the read-only laxicon reader. Sessions define the known
// project roots; the reader turns them into plan/spec inventories that ride
// Lulu's fleet snapshot (digest-gated) and the selected-session dossier.

import (
	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/laxicon"
)

// planRootFor resolves the laxicon discovery root for one session. Worktree
// sessions resolve via the root project path: laxicon/ is untracked, so it
// exists only in the main checkout, not in per-agent worktrees.
func planRootFor(s agent.Session) string {
	if s.IsWorktree && s.WorktreeRootProjectPath != "" {
		return laxicon.ResolveRoot(s.WorktreeRootProjectPath)
	}
	return laxicon.ResolveRoot(s.CWD)
}

// copilotPlanProjects loads the laxicon inventories for every distinct project
// root among the given sessions, in stable (sorted) order. Projects without
// laxicon documents are omitted.
func (d *Daemon) copilotPlanProjects(sessions []agent.Session) []laxicon.ProjectPlans {
	roots := make([]string, 0, len(sessions))
	for _, s := range sessions {
		roots = append(roots, planRootFor(s))
	}
	return d.laxiconReader.LoadAll(roots)
}

// planProjectFor picks the inventory matching one session's project root, or nil.
func planProjectFor(projects []laxicon.ProjectPlans, s agent.Session) *laxicon.ProjectPlans {
	root := planRootFor(s)
	if root == "" {
		return nil
	}
	for i := range projects {
		if projects[i].Root == root {
			return &projects[i]
		}
	}
	return nil
}
