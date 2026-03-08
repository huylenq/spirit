package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huylenq/claude-mission-control/internal/tmux"
)

type processInfo struct {
	PID  int
	Comm string
}

// buildProcessTree runs a single `ps` command and returns a map of PPID → children.
// Replaces per-pane pgrep+ps calls with one subprocess.
func buildProcessTree() map[int][]processInfo {
	out, err := exec.Command("ps", "-eo", "pid,ppid,comm").Output()
	if err != nil {
		return nil
	}

	tree := make(map[int][]processInfo)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue // skip header and malformed lines
		}
		comm := filepath.Base(strings.Join(fields[2:], " "))
		tree[ppid] = append(tree[ppid], processInfo{PID: pid, Comm: comm})
	}
	return tree
}

func findClaudeInTree(tree map[int][]processInfo, parentPID int) int {
	for _, child := range tree[parentPID] {
		if child.Comm == "claude" {
			return child.PID
		}
	}
	return 0
}

func hasNonShellChildInTree(tree map[int][]processInfo, parentPID int) bool {
	for _, child := range tree[parentPID] {
		comm := strings.TrimLeft(child.Comm, "-")
		switch comm {
		case "zsh", "bash", "fish", "sh", "dash":
			continue
		default:
			return true
		}
	}
	return false
}

var (
	gitBranchCache   = make(map[string]gitBranchCacheEntry)
	gitBranchCacheMu sync.Mutex
)

type gitBranchCacheEntry struct {
	branch  string
	expires time.Time
}

func getGitBranchCached(dir string) string {
	gitBranchCacheMu.Lock()
	if entry, ok := gitBranchCache[dir]; ok && time.Now().Before(entry.expires) {
		gitBranchCacheMu.Unlock()
		return entry.branch
	}
	gitBranchCacheMu.Unlock()

	branch := getGitBranch(dir)

	gitBranchCacheMu.Lock()
	gitBranchCache[dir] = gitBranchCacheEntry{
		branch:  branch,
		expires: time.Now().Add(10 * time.Second),
	}
	gitBranchCacheMu.Unlock()

	return branch
}

func DiscoverSessions() ([]ClaudeSession, error) {
	panes, err := tmux.ListAllPanes()
	if err != nil {
		return nil, err
	}

	activePaneIDs := make(map[string]bool)
	for _, p := range panes {
		activePaneIDs[p.PaneID] = true
	}
	CleanStale(activePaneIDs)

	// Single ps call replaces all per-pane pgrep+ps invocations
	procTree := buildProcessTree()

	var sessions []ClaudeSession
	for _, p := range panes {
		pid := findClaudeInTree(procTree, p.PanePID)
		if pid == 0 {
			status, err := ReadStatus(p.PaneID)
			if err != nil {
				continue
			}
			if status == StatusWorking {
				WriteStatus(p.PaneID, StatusDone)
				status = StatusDone
			}
			if hasNonShellChildInTree(procTree, p.PanePID) {
				RemoveStatus(p.PaneID)
				continue
			}
			if status == StatusDeferred {
				s := buildSession(p, 0, status)
				sessions = append(sessions, s)
			} else if status == StatusDone {
				// Claude process exited; clean up and don't show the session.
				RemoveStatus(p.PaneID)
			}
			continue
		}

		status, err := ReadStatus(p.PaneID)
		if err != nil {
			status = StatusDone
		}

		s := buildSession(p, pid, status)
		sessions = append(sessions, s)
	}

	return sessions, nil
}

func buildSession(p tmux.PaneInfo, pid int, status Status) ClaudeSession {
	s := ClaudeSession{
		PaneID:      p.PaneID,
		Status:      status,
		Project:     filepath.Base(p.CurrentPath),
		CWD:         p.CurrentPath,
		GitBranch:   getGitBranchCached(p.CurrentPath),
		TmuxSession: p.SessionName,
		TmuxWindow:  p.WindowIndex,
		TmuxPane:    p.PaneIndex,
		PID:         pid,
		LastChanged: getStatusModTime(p.PaneID),
		SessionID:   ReadSessionID(p.PaneID),
	}

	if status == StatusWorking {
		s.PermissionMode = ReadPermissionMode(p.PaneID)
	}

	if s.SessionID != "" {
		// Prefer hook-written cache (avoids transcript tail-scan missing old messages)
		if cached := ReadLastUserMessageCached(p.PaneID); cached != "" {
			s.LastUserMessage = cached
		} else {
			s.LastUserMessage = ReadLastUserMessage(s.SessionID)
		}
		if cached := ReadCachedSummary(s.SessionID); cached != nil && cached.Headline != "" {
			s.Headline = cached.Headline
		}
		s.CustomTitle = ReadCustomTitle(s.SessionID)
		s.FirstMessage = ReadFirstUserMessage(s.SessionID)
		s.LastActionCommit = ReadLastActionCommit(s.SessionID)
	}

	if status == StatusDeferred {
		s.DeferUntil, _ = ReadDeferUntil(p.PaneID)
		if !s.DeferUntil.IsZero() && time.Now().After(s.DeferUntil) {
			WriteStatus(p.PaneID, StatusDone)
			ClearDefer(p.PaneID)
			s.Status = StatusDone
			s.DeferUntil = time.Time{}
		}
	}

	return s
}

func getGitBranch(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getStatusModTime(paneID string) time.Time {
	info, err := os.Stat(statusFilePath(paneID))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
