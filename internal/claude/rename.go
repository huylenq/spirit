package claude

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/huylenq/spirit/internal/tmux"
)

type WindowKey struct {
	Session     string
	WindowIndex int
}

func (k WindowKey) String() string {
	return fmt.Sprintf("%s:%d", k.Session, k.WindowIndex)
}

// extractJSONObject trims surrounding text or markdown fences around a JSON
// object — Haiku occasionally wraps output despite instructions otherwise.
func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			return raw[start : end+1]
		}
	}
	return raw
}

// WindowPaneInfo describes one pane in a tmux window, regardless of whether it runs Claude.
type WindowPaneInfo struct {
	PaneID      string
	CWD         string
	DirBasename string
	ProcessName string // deepest non-shell child process
	GitBranch   string
	IsClaude    bool

	// Claude-specific fields (empty if !IsClaude)
	CustomTitle      string // user-set via Claude Code's /rename — strongest signal
	SynthesizedTitle string
	FirstMessage     string
	LastUserMessage  string
	Status           string
	Project          string
}

// GatherAllClaudeWindowPanes collects pane info for every tmux window that
// contains at least one tracked Claude session, using a single tmux call.
func GatherAllClaudeWindowPanes(sessions []ClaudeSession) (map[WindowKey][]WindowPaneInfo, error) {
	allPanes, err := tmux.ListAllPanes()
	if err != nil {
		return nil, err
	}

	procTree := buildProcessTree()

	sessionByPane := make(map[string]*ClaudeSession, len(sessions))
	for i := range sessions {
		sessionByPane[sessions[i].PaneID] = &sessions[i]
	}

	claudeWindows := make(map[WindowKey]bool)
	for _, cs := range sessions {
		if cs.IsPhantom || cs.TmuxSession == "" {
			continue
		}
		claudeWindows[WindowKey{Session: cs.TmuxSession, WindowIndex: cs.TmuxWindow}] = true
	}

	result := make(map[WindowKey][]WindowPaneInfo, len(claudeWindows))
	for _, p := range allPanes {
		key := WindowKey{Session: p.SessionName, WindowIndex: p.WindowIndex}
		if !claudeWindows[key] {
			continue
		}
		info := WindowPaneInfo{
			PaneID:      p.PaneID,
			CWD:         p.CurrentPath,
			DirBasename: filepath.Base(p.CurrentPath),
			GitBranch:   getGitBranchCached(p.CurrentPath),
			ProcessName: findDeepestProcess(procTree, p.PanePID),
		}
		if cs, ok := sessionByPane[p.PaneID]; ok {
			info.IsClaude = true
			info.CustomTitle = cs.CustomTitle
			info.SynthesizedTitle = cs.SynthesizedTitle
			info.FirstMessage = cs.FirstMessage
			info.LastUserMessage = cs.LastUserMessage
			info.Status = cs.Status.String()
			info.Project = cs.Project
		}
		result[key] = append(result[key], info)
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

func validateGeneratedName(name string) (string, error) {
	name = strings.TrimSpace(name)
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

// GenerateAllWindowNames calls Haiku once to name every supplied window.
// Returns a map from window key to generated name. Windows whose generated
// name fails validation are omitted.
func GenerateAllWindowNames(windows map[WindowKey][]WindowPaneInfo) (map[WindowKey]string, error) {
	if len(windows) == 0 {
		return map[WindowKey]string{}, nil
	}

	keys := make([]WindowKey, 0, len(windows))
	for k := range windows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Session != keys[j].Session {
			return keys[i].Session < keys[j].Session
		}
		return keys[i].WindowIndex < keys[j].WindowIndex
	})

	var b strings.Builder
	b.WriteString("You are naming tmux windows. For each window below, choose a short\n")
	b.WriteString("kebab-case name (2-4 words, e.g.: api-refactor, debug-auth, react-dashboard).\n")
	b.WriteString("Output ONLY a single JSON object mapping each window key (the quoted string\n")
	b.WriteString("after \"Window\") to its name. Use ONLY the keys listed, no extras, no commentary,\n")
	b.WriteString("no markdown fences. Names must contain only letters, digits, and hyphens.\n\n")

	for _, k := range keys {
		fmt.Fprintf(&b, "Window %q:\n", k.String())
		for i, p := range windows[k] {
			fmt.Fprintf(&b, "  Pane %d:\n", i+1)
			fmt.Fprintf(&b, "    directory: %s\n", p.DirBasename)
			if p.ProcessName != "" {
				fmt.Fprintf(&b, "    process: %s\n", p.ProcessName)
			}
			if p.GitBranch != "" {
				fmt.Fprintf(&b, "    git branch: %s\n", p.GitBranch)
			}
			if p.IsClaude {
				fmt.Fprintf(&b, "    running: Claude Code\n")
				switch {
				case p.CustomTitle != "":
					fmt.Fprintf(&b, "    user-set title: %s\n", p.CustomTitle)
				case p.SynthesizedTitle != "":
					fmt.Fprintf(&b, "    task: %s\n", p.SynthesizedTitle)
				case p.FirstMessage != "":
					msg := p.FirstMessage
					if len(msg) > 200 {
						msg = msg[:200]
					}
					fmt.Fprintf(&b, "    task: %s\n", msg)
				case p.LastUserMessage != "":
					msg := p.LastUserMessage
					if len(msg) > 200 {
						msg = msg[:200]
					}
					fmt.Fprintf(&b, "    task: %s\n", msg)
				}
				fmt.Fprintf(&b, "    status: %s\n", p.Status)
			}
		}
		b.WriteString("\n")
	}

	out, err := LightweightJSON("Output ONLY a single JSON object. No markdown, no explanation.", b.String())
	if err != nil {
		return nil, fmt.Errorf("lightweight infer: %w", err)
	}

	raw := extractJSONObject(out)
	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("parse JSON %q: %w", raw, err)
	}

	result := make(map[WindowKey]string, len(parsed))
	for _, k := range keys {
		v, ok := parsed[k.String()]
		if !ok {
			continue
		}
		name, err := validateGeneratedName(v)
		if err != nil {
			continue
		}
		result[k] = name
	}
	return result, nil
}
