package claude

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// hookInput is the JSON payload Claude Code sends to hooks on stdin.
type hookInput struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
}

// HandleHook processes a Claude Code hook event. This replaces claude-status.sh.
// It resolves the current tmux pane, reads stdin JSON, and writes status files.
func HandleHook(hookType string) {
	if os.Getenv("TMUX") == "" {
		return // not in tmux, nothing to do
	}

	paneID := resolveCurrentPane()
	if paneID == "" {
		return
	}

	dir := StatusDir()
	os.MkdirAll(dir, 0o755)

	// Read stdin (Claude Code pipes JSON with session_id, prompt, etc.)
	var input hookInput
	var rawJSON string
	if stat, _ := os.Stdin.Stat(); stat.Mode()&os.ModeCharDevice == 0 {
		data, err := os.ReadFile("/dev/stdin")
		if err == nil && len(data) > 0 {
			rawJSON = string(data)
			json.Unmarshal(data, &input)
		}
	}

	// Persist session ID
	if input.SessionID != "" {
		os.WriteFile(sessionFilePath(paneID), []byte(input.SessionID+"\n"), 0o644)
	}

	// Append to hook log (compact JSON on one line)
	compactJSON := compactJSONString(rawJSON)
	entry := fmt.Sprintf("%s %s\t%s\n", time.Now().Format("15:04:05"), hookType, compactJSON)
	hooksPath := hookFilePath(paneID)
	f, err := os.OpenFile(hooksPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		f.WriteString(entry)
		f.Close()
		// Trim when file exceeds ~60KB
		if info, err := os.Stat(hooksPath); err == nil && info.Size() > 61440 {
			trimHookFile(hooksPath)
		}
	}

	// Write status and optional data, then nudge daemon with the change
	var newStatus string
	var lastMsg string
	switch hookType {
	case "UserPromptSubmit", "PreToolUse":
		newStatus = "working"
		os.WriteFile(statusFilePath(paneID), []byte("working\n"), 0o644)
		os.Remove(deferFilePath(paneID)) // auto-cancel defer
		if hookType == "UserPromptSubmit" && input.Prompt != "" {
			os.WriteFile(lastMsgFilePath(paneID), []byte(input.Prompt), 0o644)
			lastMsg = input.Prompt
		}
	case "Stop":
		newStatus = "stopped"
		os.WriteFile(statusFilePath(paneID), []byte("stopped\n"), 0o644)
	}

	if newStatus != "" {
		nudgeDaemon(paneID, newStatus, lastMsg)
	}
}

// resolveCurrentPane walks the process tree upward to find which tmux pane
// owns the current process. This is more reliable than TMUX_PANE which can
// be stale in worktrees.
func resolveCurrentPane() string {
	// Get all tmux pane PIDs
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{pane_id}").Output()
	if err != nil {
		return ""
	}

	paneMap := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		paneMap[pid] = parts[1]
	}

	// Walk up the process tree from our PID
	pid := os.Getpid()
	for pid > 1 {
		if paneID, ok := paneMap[pid]; ok {
			return paneID
		}
		ppid, err := getParentPID(pid)
		if err != nil {
			break
		}
		pid = ppid
	}
	return ""
}

func getParentPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func compactJSONString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var v json.RawMessage
	if json.Unmarshal([]byte(raw), &v) != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(b)
}

type nudgeRequest struct {
	Type string    `json:"type"`
	Data nudgeData `json:"data"`
}

type nudgeData struct {
	PaneID          string `json:"paneID"`
	Status          string `json:"status"`
	LastUserMessage string `json:"lastUserMessage,omitempty"`
}

// nudgeDaemon sends a fire-and-forget "nudge" RPC to the daemon with the
// status change data so it can patch the session in-place without re-polling.
func nudgeDaemon(paneID, status, lastUserMessage string) {
	sock := filepath.Join(StatusDir(), "daemon.sock")
	conn, err := net.DialTimeout("unix", sock, 50*time.Millisecond)
	if err != nil {
		return // daemon not running, no big deal
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))

	json.NewEncoder(conn).Encode(nudgeRequest{
		Type: "nudge",
		Data: nudgeData{PaneID: paneID, Status: status, LastUserMessage: lastUserMessage},
	})
}

func trimHookFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
