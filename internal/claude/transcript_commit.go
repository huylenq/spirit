package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// commitSuccessRe matches Claude's commit success output like "Commit 0141d9c created successfully."
var commitSuccessRe = regexp.MustCompile(`(?i)\bcommit\s+[0-9a-f]{7,}.*\bcreated\b`)

// --- Last action commit cache (mtime-based) ---

type lastActionCommitCacheEntry struct {
	isCommit bool
	modTime  time.Time
}

var (
	lastActionCommitCache   = make(map[string]lastActionCommitCacheEntry)
	lastActionCommitCacheMu sync.Mutex
)

// ReadLastActionCommit returns true if the session's last action was a git commit.
// Two heuristics (checked within the last 3 assistant messages each):
//   - Assistant text matches commit success pattern (e.g. "Commit abc1234 created successfully.")
//   - Bash tool_use input contains a git commit command
func ReadLastActionCommit(sessionID string) bool {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return false
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return false
	}

	lastActionCommitCacheMu.Lock()
	if cached, ok := lastActionCommitCache[sessionID]; ok && cached.modTime.Equal(info.ModTime()) {
		lastActionCommitCacheMu.Unlock()
		return cached.isCommit
	}
	lastActionCommitCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	const tailSize = 8 * 1024
	offset := info.Size() - tailSize
	if offset < 0 {
		offset = 0
	}

	buf := make([]byte, info.Size()-offset)
	n, _ := f.ReadAt(buf, offset)
	if n == 0 {
		return false
	}

	result := scanLastActionCommit(buf[:n])

	lastActionCommitCacheMu.Lock()
	lastActionCommitCache[sessionID] = lastActionCommitCacheEntry{isCommit: result, modTime: info.ModTime()}
	lastActionCommitCacheMu.Unlock()

	return result
}

// scanLastActionCommit walks lines backwards looking for commit evidence.
// Returns true if either:
//   - The last 3 assistant text messages contain a commit success pattern
//     (e.g. "Commit 0141d9c created successfully.")
//   - The last 3 Bash tool_use calls contain a git commit command
func scanLastActionCommit(data []byte) bool {
	lines := bytes.Split(data, []byte("\n"))
	toolUseCount := 0
	textCount := 0
	const maxChecks = 3

	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if len(line) == 0 || !bytes.Contains(line, []byte(`"assistant"`)) {
			continue
		}

		// Check assistant text for commit success pattern
		if textCount < maxChecks {
			text := extractAssistantText(line)
			if text != "" {
				textCount++
				if commitSuccessRe.MatchString(text) {
					return true
				}
			}
		}

		// Check Bash tool_use blocks for git commit command
		if toolUseCount < maxChecks && bytes.Contains(line, []byte(`"Bash"`)) {
			found, n := checkLineForGitCommit(line)
			if found {
				return true
			}
			toolUseCount += n
		}

		if toolUseCount >= maxChecks && textCount >= maxChecks {
			break
		}
	}

	return false
}

// checkLineForGitCommit parses an assistant line for Bash tool_use blocks
// containing a git commit command. Returns (found, bashBlockCount).
func checkLineForGitCommit(line []byte) (bool, int) {
	var tl transcriptLine
	if json.Unmarshal(line, &tl) != nil || tl.Type != "assistant" {
		return false, 0
	}

	var msg messageContent
	if json.Unmarshal(tl.Message, &msg) != nil {
		return false, 0
	}

	var blocks []toolUseBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return false, 0
	}

	count := 0
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name != "Bash" {
			continue
		}
		count++

		var inp struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(b.Input, &inp) != nil {
			continue
		}
		if isGitCommitCommand(inp.Command) {
			return true, count
		}
	}
	return false, count
}

// isGitCommitCommand checks if a shell command contains a git commit invocation.
// Matches "git commit" as a complete command or followed by a space/flag.
func isGitCommitCommand(cmd string) bool {
	for _, sep := range []string{"&&", ";"} {
		for _, part := range strings.Split(cmd, sep) {
			p := strings.TrimSpace(part)
			if p == "git commit" || strings.HasPrefix(p, "git commit ") {
				return true
			}
		}
	}
	// Single command (no separators)
	cmd = strings.TrimSpace(cmd)
	return cmd == "git commit" || strings.HasPrefix(cmd, "git commit ")
}
