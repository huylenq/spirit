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

	// Load bookmarks once for the entire discover cycle
	bookmarks, _ := ReadAllLaterBookmarks()
	bookmarkByPane := make(map[string]string, len(bookmarks)) // paneID → bookmarkID
	for _, bm := range bookmarks {
		bookmarkByPane[bm.PaneID] = bm.ID
	}

	// Build active sets for CleanStale
	activePaneIDs := make(map[string]bool)
	activeSessionIDs := make(map[string]bool)
	for _, p := range panes {
		activePaneIDs[p.PaneID] = true
		if sid := ReadSessionID(p.PaneID); sid != "" {
			activeSessionIDs[sid] = true
		}
	}
	for _, bm := range bookmarks {
		if bm.SessionID != "" {
			activeSessionIDs[bm.SessionID] = true
		}
		activePaneIDs[bm.PaneID] = true
	}
	CleanStale(activeSessionIDs, activePaneIDs)

	// Single ps call replaces all per-pane pgrep+ps invocations
	procTree := buildProcessTree()

	var sessions []ClaudeSession
	for _, p := range panes {
		sessionID := ReadSessionID(p.PaneID)
		pid := findClaudeInTree(procTree, p.PanePID)
		if pid == 0 {
			if sessionID == "" {
				continue
			}
			status, err := ReadStatus(sessionID)
			if err != nil {
				continue
			}
			// Crash recovery: if process is gone but status says agent-turn, mark user-turn.
			// SessionEnd hook normally handles this, but crashes skip the hook.
			if status == StatusAgentTurn {
				WriteStatus(sessionID, StatusUserTurn)
				status = StatusUserTurn
			}
			if bookmarkByPane[p.PaneID] != "" {
				// Bookmarked: keep session visible regardless of status
				s := buildSession(p, 0, status, bookmarkByPane)
				sessions = append(sessions, s)
			} else {
				// No bookmark, no process: clean up
				RemoveSessionFiles(sessionID)
				RemovePaneMapping(p.PaneID)
			}
			continue
		}

		if sessionID == "" {
			continue // no session ID yet, skip
		}
		status, err := ReadStatus(sessionID)
		if err != nil {
			status = StatusUserTurn
		}

		s := buildSession(p, pid, status, bookmarkByPane)
		sessions = append(sessions, s)
	}

	// Merge phantom Later sessions from bookmarks (one per pane, newest wins)
	seenPaneIDs := make(map[string]bool)
	for _, s := range sessions {
		seenPaneIDs[s.PaneID] = true
	}
	for _, bm := range bookmarks {
		if seenPaneIDs[bm.PaneID] {
			continue // pane already represented (live session or earlier bookmark)
		}
		seenPaneIDs[bm.PaneID] = true
		sessions = append(sessions, ClaudeSession{
			PaneID:            bm.PaneID,
			Status:            StatusUserTurn,
			Project:           bm.Project,
			CWD:               bm.CWD,
			SynthesizedTitle: bm.SynthesizedTitle,
			CustomTitle:       bm.CustomTitle,
			FirstMessage:      bm.FirstMessage,
			SessionID:         bm.SessionID,
			IsPhantom:         true,
			LaterBookmarkID:   bm.ID,
			LastChanged:       bm.CreatedAt,
		})
	}

	return sessions, nil
}

// parseWorktreeCWD detects if cwd is inside a Claude Code worktree
// (i.e. contains /.claude/worktrees/<name>). Returns the root repo path,
// worktree name, and whether it matched.
func parseWorktreeCWD(cwd string) (rootPath, name string, ok bool) {
	const marker = "/.claude/worktrees/"
	idx := strings.Index(cwd, marker)
	if idx < 0 {
		return "", "", false
	}
	rootPath = cwd[:idx]
	rest := cwd[idx+len(marker):]
	// name is the first path segment after the marker
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		name = rest[:slash]
	} else {
		name = rest
	}
	if name == "" {
		return "", "", false
	}
	return rootPath, name, true
}

func buildSession(p tmux.PaneInfo, pid int, status Status, bookmarkByPane map[string]string) ClaudeSession {
	sessionID := ReadSessionID(p.PaneID)
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
		CreatedAt:   p.PaneCreated,
		SessionID:   sessionID,
	}

	// Detect worktree sessions and fix project grouping
	if rootPath, wtName, ok := parseWorktreeCWD(p.CurrentPath); ok {
		s.IsWorktree = true
		s.WorktreeName = wtName
		s.WorktreeRootProjectPath = rootPath
		s.Project = filepath.Base(rootPath)
	}

	if sessionID != "" {
		s.LastChanged = getStatusModTime(sessionID)

		if status == StatusAgentTurn {
			s.PermissionMode = ReadPermissionMode(sessionID)
		}

		// Prefer hook-written cache (avoids transcript tail-scan missing old messages)
		if cached := ReadLastUserMessageCached(sessionID); cached != "" {
			s.LastUserMessage = cached
		} else {
			s.LastUserMessage = ReadLastUserMessage(sessionID)
		}
		if cached := ReadCachedSummary(sessionID); cached != nil {
			s.SynthesizedTitle = cached.SynthesizedTitle
			if cached.ProblemType != "" {
				s.ProblemType = cached.ProblemType
			}
			// Title drift: synthesized title changed since last /rename apply
			s.TitleDrift = cached.SynthesizedTitle != "" &&
				cached.AppliedSynthesizedTitle != "" &&
				cached.SynthesizedTitle != cached.AppliedSynthesizedTitle
		}
		s.CustomTitle = ReadCustomTitle(sessionID)
		s.FirstMessage = ReadFirstUserMessage(sessionID)

		// Prefer hook-derived last action when present (faster than transcript scan)
		if action := ReadLastAction(sessionID); action != "" {
			s.LastActionCommit = action == "commit"
		} else {
			s.LastActionCommit = ReadLastActionCommit(sessionID)
		}

		// Hook-derived fields from status files
		s.StopReason = ReadStopReason(sessionID)
		s.SkillName = ReadSkillName(sessionID)
		s.IsWaiting = ReadWaiting(sessionID)
		s.CompactCount = ReadCompactCount(sessionID)
		s.Tags = ReadTags(sessionID)
		s.Note = ReadNote(sessionID)
	}

	s.LaterBookmarkID = bookmarkByPane[p.PaneID]

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

func getStatusModTime(sessionID string) time.Time {
	info, err := os.Stat(statusFilePath(sessionID))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
