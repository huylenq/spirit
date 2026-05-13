package app

import "strings"

const projectMRUCap = 16

// recordProjectFilter pushes name to the front of the project-filter MRU,
// deduping. Called when the user commits a `p:NAME` search.
func (m *Model) recordProjectFilter(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	out := make([]string, 0, len(m.projectMRU)+1)
	out = append(out, name)
	for _, n := range m.projectMRU {
		if strings.EqualFold(n, name) {
			continue
		}
		out = append(out, n)
		if len(out) >= projectMRUCap {
			break
		}
	}
	m.projectMRU = out
}

// orderedProjectCandidates returns the project names ordered for ghost
// completion and Tab cycling: MRU first (filtered to projects that still
// exist in the session list), then the rest of the projects alphabetically.
func (m *Model) orderedProjectCandidates() []string {
	all := m.sidebar.AllProjectNames()
	live := make(map[string]bool, len(all))
	for _, n := range all {
		live[n] = true
	}
	out := make([]string, 0, len(all))
	seen := make(map[string]bool, len(all))
	for _, n := range m.projectMRU {
		if live[n] && !seen[n] {
			out = append(out, n)
			seen[n] = true
		}
	}
	for _, n := range all {
		if !seen[n] {
			out = append(out, n)
			seen[n] = true
		}
	}
	return out
}

// extractProjectFilter pulls the project name out of a committed search
// value. Returns "" if the value isn't a project filter (text query, empty,
// or combined search).
func extractProjectFilter(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Shorthand: `p NAME` (single name only).
	if strings.HasPrefix(raw, "p ") && !strings.ContainsRune(raw[2:], ' ') {
		return raw[2:]
	}
	// Directive form: any `p:NAME` token wins (return the first).
	for _, tok := range strings.Fields(raw) {
		if rest, ok := strings.CutPrefix(tok, "p:"); ok && rest != "" {
			return rest
		}
	}
	return ""
}

// cycleActiveProjectFilter rotates the currently-applied `p:` filter through
// the MRU. delta=+1 advances to next, delta=-1 to previous. Returns the new
// filter name, or "" if no rotation happened (no active filter / empty MRU).
func (m *Model) cycleActiveProjectFilter(delta int) string {
	candidates := m.orderedProjectCandidates()
	if len(candidates) == 0 {
		return ""
	}
	current := extractProjectFilter(m.sidebar.Narrow())
	idx := -1
	for i, n := range candidates {
		if strings.EqualFold(n, current) {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 0
	} else {
		idx = (idx + delta + len(candidates)) % len(candidates)
	}
	next := candidates[idx]
	m.sidebar.SetNarrow("p:" + next)
	return next
}

