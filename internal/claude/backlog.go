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
	ID        string    // filename stem (hex)
	Body      string    // full text content
	CWD       string    // project root directory
	Project   string    // filepath.Base(CWD)
	CreatedAt time.Time // file modtime
	UpdatedAt time.Time // file modtime
}

// DisplayTitle returns the first line of Body, or "(empty)" if blank.
func (i Backlog) DisplayTitle() string {
	if i.Body == "" {
		return "(empty)"
	}
	line := strings.SplitN(i.Body, "\n", 2)[0]
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
	return &Backlog{
		ID:        id,
		Body:      string(data),
		CWD:       cwd,
		Project:   filepath.Base(cwd),
		CreatedAt: info.ModTime(),
		UpdatedAt: info.ModTime(),
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

// migrateIdeaDir renames <cwd>/.cmc/ideas to <cwd>/.cmc/backlog if the old dir
// exists and the new one does not. Silent no-op on any error.
func migrateIdeaDir(cwd string) {
	oldDir := filepath.Join(cwd, ".cmc", "ideas")
	newDir := BacklogDir(cwd)
	if _, err := os.Stat(oldDir); err != nil {
		return // old dir doesn't exist
	}
	if _, err := os.Stat(newDir); err == nil {
		return // new dir already exists, don't overwrite
	}
	os.Rename(oldDir, newDir) //nolint:errcheck
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
