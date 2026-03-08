package claude

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/huylenq/claude-mission-control/internal/tmux"
)

// WindowPaneInfo describes one pane in a tmux window, regardless of whether it runs Claude.
type WindowPaneInfo struct {
	PaneID      string
	CWD         string
	DirBasename string
	ProcessName string // deepest non-shell child process
	GitBranch   string
	IsClaude    bool

	// Claude-specific fields (empty if !IsClaude)
	Headline        string
	LastUserMessage string
	Status          string
	Project         string
}

// GatherWindowPanes collects info about all panes in a tmux window.
func GatherWindowPanes(sessionName string, windowIndex int, sessions []ClaudeSession) ([]WindowPaneInfo, error) {
	panes, err := tmux.ListWindowPanes(sessionName, windowIndex)
	if err != nil {
		return nil, err
	}

	procTree := buildProcessTree()

	// Index sessions by PaneID for O(1) lookup
	sessionByPane := make(map[string]*ClaudeSession, len(sessions))
	for i := range sessions {
		sessionByPane[sessions[i].PaneID] = &sessions[i]
	}

	var result []WindowPaneInfo
	for _, p := range panes {
		info := WindowPaneInfo{
			PaneID:      p.PaneID,
			CWD:         p.CurrentPath,
			DirBasename: filepath.Base(p.CurrentPath),
			GitBranch:   getGitBranchCached(p.CurrentPath),
			ProcessName: findDeepestProcess(procTree, p.PanePID),
		}

		if cs, ok := sessionByPane[p.PaneID]; ok {
			info.IsClaude = true
			info.Headline = cs.Headline
			info.LastUserMessage = cs.LastUserMessage
			info.Status = cs.Status.String()
			info.Project = cs.Project
		}

		result = append(result, info)
	}
	return result, nil
}

// findDeepestProcess walks the process tree from parentPID, skipping shell processes,
// and returns the name of the first non-shell child. Returns empty string if only shells found.
func findDeepestProcess(tree map[int][]processInfo, parentPID int) string {
	for _, child := range tree[parentPID] {
		comm := strings.TrimLeft(child.Comm, "-")
		switch comm {
		case "zsh", "bash", "fish", "sh", "dash":
			// Recurse through shell layers
			if deeper := findDeepestProcess(tree, child.PID); deeper != "" {
				return deeper
			}
		default:
			return child.Comm
		}
	}
	return ""
}

var validWindowName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\- ]{0,28}[a-zA-Z0-9]$`)

// GenerateWindowName calls Haiku to generate a short kebab-case window name from pane context.
func GenerateWindowName(panes []WindowPaneInfo) (string, error) {
	var b strings.Builder
	b.WriteString("You are naming a tmux window. Based on the panes below, output ONLY a short\n")
	b.WriteString("kebab-case name (2-4 words, e.g.: api-refactor, debug-auth, react-dashboard).\n")
	b.WriteString("No quotes, no explanation, no punctuation except hyphens.\n\n")

	for i, p := range panes {
		fmt.Fprintf(&b, "Pane %d:\n", i+1)
		fmt.Fprintf(&b, "  directory: %s\n", p.DirBasename)
		if p.ProcessName != "" {
			fmt.Fprintf(&b, "  process: %s\n", p.ProcessName)
		}
		if p.GitBranch != "" {
			fmt.Fprintf(&b, "  git branch: %s\n", p.GitBranch)
		}
		if p.IsClaude {
			fmt.Fprintf(&b, "  running: Claude Code\n")
			if p.Headline != "" {
				fmt.Fprintf(&b, "  task: %s\n", p.Headline)
			} else if p.LastUserMessage != "" {
				// Truncate long messages
				msg := p.LastUserMessage
				if len(msg) > 200 {
					msg = msg[:200]
				}
				fmt.Fprintf(&b, "  task: %s\n", msg)
			}
			fmt.Fprintf(&b, "  status: %s\n", p.Status)
		}
		b.WriteString("\n")
	}

	cmd := newLightweightClaude("Output ONLY a short kebab-case name. No quotes, no explanation.", b.String())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude CLI: %w", err)
	}

	name := strings.TrimSpace(string(out))
	// Strip surrounding quotes if present
	name = strings.Trim(name, "\"'`")
	name = strings.TrimSpace(name)

	if len(name) < 2 || len(name) > 30 {
		return "", fmt.Errorf("generated name %q has invalid length (%d chars)", name, len(name))
	}
	if !validWindowName.MatchString(name) {
		return "", fmt.Errorf("generated name %q doesn't match allowed pattern", name)
	}

	return name, nil
}
