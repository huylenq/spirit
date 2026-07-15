// Package laxicon is Spirit's read-only view of the intent altitude (spec
// Decision 13): per-repo laxicon/plans/*.md and laxicon/specs/*.md documents
// with tolerant frontmatter, first-heading titles, and checkbox progress
// tallies. Spirit NEVER writes these files — Lulu edits plans with her own
// Hermes file tools under the W4 approval flow; Spirit only reads and exposes.
//
// Everything here is fail-soft by design: a malformed, unreadable, or missing
// plan file is skipped silently, never an error — plan awareness must not be
// able to break Lulu's context assembly.
package laxicon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind distinguishes the two laxicon document families.
type Kind string

const (
	KindPlan Kind = "plan"
	KindSpec Kind = "spec"
)

// Progress tallies a document's checkbox items: `- [x]` done, `- [~]` partial
// (in progress / deliberately deferred), `- [ ]` open.
type Progress struct {
	Done    int `json:"done"`
	Partial int `json:"partial"`
	Open    int `json:"open"`
}

// Total is the number of checkbox items found.
func (p Progress) Total() int { return p.Done + p.Partial + p.Open }

// String renders a compact human tally like "3/10 done" or "3/10 done, 1 in progress".
// Empty when the document has no checkboxes.
func (p Progress) String() string {
	if p.Total() == 0 {
		return ""
	}
	s := fmt.Sprintf("%d/%d done", p.Done, p.Total())
	if p.Partial > 0 {
		s += fmt.Sprintf(", %d in progress", p.Partial)
	}
	return s
}

// Doc is one parsed laxicon document.
type Doc struct {
	Path     string   `json:"path"`
	Slug     string   `json:"slug"` // filename without .md
	Kind     Kind     `json:"kind"`
	Title    string   `json:"title"`  // first markdown heading; falls back to the slug
	Status   string   `json:"status"` // frontmatter `status:`; "" when absent
	Progress Progress `json:"progress"`
}

// ProjectPlans is the laxicon inventory of one project root.
type ProjectPlans struct {
	Root  string `json:"root"`
	Plans []Doc  `json:"plans"`
	Specs []Doc  `json:"specs"`
}

// Empty reports whether the project has no laxicon documents at all.
func (p ProjectPlans) Empty() bool { return len(p.Plans) == 0 && len(p.Specs) == 0 }

// PlanBySlug returns the plan with the given slug, or nil.
func (p ProjectPlans) PlanBySlug(slug string) *Doc {
	for i := range p.Plans {
		if p.Plans[i].Slug == slug {
			return &p.Plans[i]
		}
	}
	return nil
}

// terminalStatuses are plan statuses considered closed for context purposes: they
// are excluded from the "active plans" view (but still parsed and inspectable).
// Unknown statuses — including the empty string — are treated as live, so a plan
// with missing or custom frontmatter is surfaced rather than hidden.
var terminalStatuses = map[string]struct{}{
	"done": {}, "superseded": {}, "archived": {}, "rejected": {},
	"abandoned": {}, "shelved": {}, "cancelled": {},
}

// Active reports whether a plan document counts as active/live.
func (d Doc) Active() bool {
	_, terminal := terminalStatuses[strings.ToLower(d.Status)]
	return !terminal
}

// ActivePlans returns the project's non-terminal plans.
func (p ProjectPlans) ActivePlans() []Doc {
	var out []Doc
	for _, d := range p.Plans {
		if d.Active() {
			out = append(out, d)
		}
	}
	return out
}

// ResolveRoot walks up from dir to the nearest ancestor that contains a
// `laxicon/` directory or a `.git` entry, returning that ancestor. It returns
// "" when neither is found (dir is not inside any recognizable project).
// A `laxicon/` hit is preferred at each level so a repo-in-repo layout still
// finds the laxicon that governs the given directory.
func ResolveRoot(dir string) string {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return ""
	}
	for {
		if dirExists(filepath.Join(dir, "laxicon")) || pathExists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pathExists tolerates both a `.git` directory and a `.git` file (worktrees).
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Reader loads laxicon documents with a per-file cache keyed by (mtime, size),
// so unchanged files are parsed once. The zero value is ready to use.
type Reader struct {
	mu    sync.Mutex
	cache map[string]cacheEntry

	// parses counts actual file parses (cache misses), for tests.
	parses int
}

type cacheEntry struct {
	modTime time.Time
	size    int64
	doc     Doc
	ok      bool // false: the file was unreadable last time; retried on mtime change
}

// Load discovers and parses root/laxicon/plans/*.md and root/laxicon/specs/*.md.
// Missing directories yield an empty (never nil-erroring) inventory. Document
// order is stable (directory order, i.e. sorted by filename).
func (r *Reader) Load(root string) ProjectPlans {
	pp := ProjectPlans{Root: root}
	if root == "" {
		return pp
	}
	pp.Plans = r.loadDir(filepath.Join(root, "laxicon", "plans"), KindPlan)
	pp.Specs = r.loadDir(filepath.Join(root, "laxicon", "specs"), KindSpec)
	return pp
}

// LoadAll loads a sorted, deduplicated set of project roots. Empty roots and
// projects without laxicon documents are dropped; order is stable by root path
// so the result is fingerprint-friendly.
func (r *Reader) LoadAll(roots []string) []ProjectPlans {
	seen := map[string]struct{}{}
	var uniq []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		if _, dup := seen[root]; dup {
			continue
		}
		seen[root] = struct{}{}
		uniq = append(uniq, root)
	}
	sort.Strings(uniq)

	var out []ProjectPlans
	for _, root := range uniq {
		if pp := r.Load(root); !pp.Empty() {
			out = append(out, pp)
		}
	}
	return out
}

func (r *Reader) loadDir(dir string, kind Kind) []Doc {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no laxicon dir (or unreadable) — fail-soft
	}
	var docs []Doc
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if doc, ok := r.loadFile(filepath.Join(dir, e.Name()), kind); ok {
			docs = append(docs, doc)
		}
	}
	return docs
}

// loadFile parses one document through the mtime cache. The bool is false only
// when the file could not be read at all; parse "failures" don't exist — every
// readable file yields a Doc with whatever could be extracted.
func (r *Reader) loadFile(path string, kind Kind) (Doc, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return Doc{}, false
	}

	r.mu.Lock()
	if entry, hit := r.cache[path]; hit && entry.modTime.Equal(info.ModTime()) && entry.size == info.Size() {
		r.mu.Unlock()
		return entry.doc, entry.ok
	}
	r.mu.Unlock()

	content, err := os.ReadFile(path)
	doc, ok := Doc{}, false
	if err == nil {
		doc, ok = parseDoc(path, kind, content), true
	}

	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]cacheEntry{}
	}
	r.cache[path] = cacheEntry{modTime: info.ModTime(), size: info.Size(), doc: doc, ok: ok}
	r.parses++
	r.mu.Unlock()
	return doc, ok
}

// parseDoc extracts frontmatter status, first-heading title, and checkbox
// tallies from a document. Tolerant by construction: missing frontmatter, an
// unclosed frontmatter fence, unknown keys, nested YAML, and absent headings
// all degrade to zero values instead of failing.
func parseDoc(path string, kind Kind, content []byte) Doc {
	doc := Doc{
		Path: path,
		Slug: strings.TrimSuffix(filepath.Base(path), ".md"),
		Kind: kind,
	}

	lines := strings.Split(string(content), "\n")
	body := lines

	// Tolerant frontmatter: only when the very first line is a `---` fence.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		closed := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				closed = i
				break
			}
		}
		if closed > 0 {
			for _, line := range lines[1:closed] {
				key, value, found := strings.Cut(line, ":")
				if !found {
					continue // list items, nested maps, garbage — ignored
				}
				if strings.TrimSpace(key) == "status" {
					doc.Status = strings.TrimSpace(value)
				}
			}
			body = lines[closed+1:]
		}
		// Unclosed fence: treat the whole file as body (no frontmatter).
	}

	for _, line := range body {
		if doc.Title == "" {
			if title, ok := headingTitle(line); ok {
				doc.Title = title
			}
		}
		switch checkboxState(line) {
		case 'x':
			doc.Progress.Done++
		case '~':
			doc.Progress.Partial++
		case ' ':
			doc.Progress.Open++
		}
	}
	if doc.Title == "" {
		doc.Title = doc.Slug
	}
	return doc
}

// headingTitle extracts the text of a markdown ATX heading line.
func headingTitle(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	if title == "" {
		return "", false
	}
	return title, true
}

// checkboxState classifies a line as a checkbox item, returning 'x' (done,
// case-insensitive), '~' (partial), ' ' (open), or 0 (not a checkbox). Bullets
// may be -, *, or + at any indentation.
func checkboxState(line string) byte {
	s := strings.TrimLeft(line, " \t")
	if len(s) < 5 {
		return 0
	}
	if s[0] != '-' && s[0] != '*' && s[0] != '+' {
		return 0
	}
	if s[1] != ' ' || s[2] != '[' || s[4] != ']' {
		return 0
	}
	switch s[3] {
	case 'x', 'X':
		return 'x'
	case '~':
		return '~'
	case ' ':
		return ' '
	}
	return 0
}

// Fingerprint reduces the material plan state of a set of projects to a stable
// string, suitable for joining Lulu's fleet-snapshot digest: any status or
// checkbox change — and any plan appearing or disappearing — changes the
// fingerprint, while re-reads of unchanged files do not.
func Fingerprint(projects []ProjectPlans) string {
	var b strings.Builder
	for _, p := range projects {
		for _, d := range append(append([]Doc{}, p.Plans...), p.Specs...) {
			fmt.Fprintf(&b, "%s|%s|%s|%s|%d.%d.%d\n",
				p.Root, d.Kind, d.Slug, d.Status,
				d.Progress.Done, d.Progress.Partial, d.Progress.Open)
		}
	}
	return b.String()
}
