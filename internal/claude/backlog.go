package claude

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backlog represents a project backlog item stored as a markdown file.
type Backlog struct {
	ID          string    // filename stem (hex)
	Body        string    // full text content (including frontmatter)
	Tags        []string  // parsed from frontmatter; not stored separately
	contentBody string    // cached body without frontmatter (populated at load time)
	CWD         string    // project root directory
	Project     string    // filepath.Base(CWD)
	CreatedAt   time.Time // file modtime
	UpdatedAt   time.Time // file modtime
}

// parseFrontmatter splits body into tags and content.
// Frontmatter format: ---\ntags: a, b\n---\ncontent
// Returns nil tags and full body if no valid frontmatter found.
func parseFrontmatter(body string) (tags []string, content string) {
	if !strings.HasPrefix(body, "---\n") {
		return nil, body
	}
	rest := body[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, body
	}
	fm := rest[:end]
	// Content starts after \n--- plus optional trailing newline
	after := rest[end+4:]
	content = strings.TrimPrefix(after, "\n")

	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "tags:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
			for _, t := range strings.Split(val, ",") {
				t = strings.TrimSpace(strings.ToLower(t))
				if t != "" {
					tags = append(tags, t)
				}
			}
		}
	}
	sort.Strings(tags)
	return
}

// buildFrontmatter reconstructs a body with tags in YAML frontmatter.
// If tags is empty, returns content as-is (no frontmatter).
func buildFrontmatter(tags []string, content string) string {
	if len(tags) == 0 {
		return content
	}
	return "---\ntags: " + strings.Join(tags, ", ") + "\n---\n" + content
}

// ToggleBacklogTag adds or removes a tag in the body's YAML frontmatter.
// Returns the modified body and true if the tag was removed (false if added).
func ToggleBacklogTag(body, tag string) (string, bool) {
	tag = strings.ToLower(tag)
	tags, content := parseFrontmatter(body)
	for i, t := range tags {
		if t == tag {
			tags = append(tags[:i], tags[i+1:]...)
			return buildFrontmatter(tags, content), true
		}
	}
	tags = append(tags, tag)
	sort.Strings(tags)
	return buildFrontmatter(tags, content), false
}

// ContentBody returns the body text without YAML frontmatter.
// Uses a cached value populated at load time to avoid re-parsing on every render.
func (b Backlog) ContentBody() string {
	if b.contentBody != "" {
		return b.contentBody
	}
	_, content := parseFrontmatter(b.Body)
	return content
}

// DisplayTitle returns the first line of content (after frontmatter), or "(empty)" if blank.
func (b Backlog) DisplayTitle() string {
	content := b.ContentBody()
	if content == "" {
		return "(empty)"
	}
	line := strings.SplitN(content, "\n", 2)[0]
	line = strings.TrimSpace(line)
	if line == "" {
		return "(empty)"
	}
	return line
}

// BacklogDir returns the backlog directory path for a given project root.
func BacklogDir(cwd string) string {
	return filepath.Join(cwd, ".cmc", "backlog")
}

// GenerateBacklogID creates a random hex ID for a backlog file.
func GenerateBacklogID() string {
	return GenerateBookmarkID()
}

// WriteBacklog persists a backlog item to disk as a .md file.
func WriteBacklog(cwd string, backlog Backlog) error {
	dir := BacklogDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, backlog.ID+".md")
	return os.WriteFile(path, []byte(backlog.Body), 0o644)
}

// ReadBacklog reads a single backlog file by ID.
func ReadBacklog(cwd, id string) (*Backlog, error) {
	path := filepath.Join(BacklogDir(cwd), id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	body := string(data)
	tags, content := parseFrontmatter(body)
	return &Backlog{
		ID:          id,
		Body:        body,
		Tags:        tags,
		contentBody: content,
		CWD:         cwd,
		Project:     filepath.Base(cwd),
		CreatedAt:   info.ModTime(),
		UpdatedAt:   info.ModTime(),
	}, nil
}

// ReadAllBacklog reads all backlog files from a project's .cmc/backlog/ directory.
func ReadAllBacklog(cwd string) ([]Backlog, error) {
	dir := BacklogDir(cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var backlogs []Backlog
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		backlog, err := ReadBacklog(cwd, id)
		if err != nil {
			continue
		}
		backlogs = append(backlogs, *backlog)
	}
	sort.Slice(backlogs, func(i, j int) bool {
		return backlogs[i].CreatedAt.Before(backlogs[j].CreatedAt)
	})
	return backlogs, nil
}

// RemoveBacklog deletes a backlog file.
func RemoveBacklog(cwd, id string) error {
	path := filepath.Join(BacklogDir(cwd), id+".md")
	return os.Remove(path)
}

// BacklogFilePath returns the full filesystem path for a backlog item.
func BacklogFilePath(cwd, id string) string {
	return filepath.Join(BacklogDir(cwd), id+".md")
}

// CollectUniqueCWDs extracts deduplicated CWDs from a session list.
func CollectUniqueCWDs(sessions []ClaudeSession) []string {
	seen := make(map[string]bool)
	var cwds []string
	for _, s := range sessions {
		if s.CWD != "" && !seen[s.CWD] {
			seen[s.CWD] = true
			cwds = append(cwds, s.CWD)
		}
	}
	return cwds
}

// migrateIdeaDir renames <cwd>/.cmc/ideas to <cwd>/.cmc/backlog.
// Silently no-ops if the source does not exist or the destination is non-empty.
func migrateIdeaDir(cwd string) {
	_ = os.Rename(filepath.Join(cwd, ".cmc", "ideas"), BacklogDir(cwd))
}

// DiscoverBacklogs reads all backlog items from the CWDs of the given sessions.
// It also auto-migrates any legacy .cmc/ideas/ directories to .cmc/backlog/.
func DiscoverBacklogs(sessions []ClaudeSession) []Backlog {
	var all []Backlog
	for _, cwd := range CollectUniqueCWDs(sessions) {
		migrateIdeaDir(cwd)
		backlogs, _ := ReadAllBacklog(cwd)
		all = append(all, backlogs...)
	}
	return all
}
