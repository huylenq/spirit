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
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	CWD            string          `json:"cwd"`
	PermissionMode string          `json:"permission_mode"`
	HookEventName  string          `json:"hook_event_name"`
	Prompt         string          `json:"prompt,omitempty"`            // UserPromptSubmit
	ToolName       string          `json:"tool_name,omitempty"`         // PostToolUse
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`        // PostToolUse
	ToolResult     string          `json:"tool_result,omitempty"`       // PostToolUse (NOT persisted)
	Message        string          `json:"message,omitempty"`           // Notification
	Title          string          `json:"title,omitempty"`             // Notification
	NotifType      string          `json:"notification_type,omitempty"` // Notification
	StopReason     string          `json:"reason,omitempty"`            // Stop
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

	// Bail early if no session ID — can't write session-keyed files without it
	sessionID := input.SessionID
	if sessionID == "" {
		return
	}

	// Persist pane→session mapping (pane-keyed reverse lookup)
	os.WriteFile(sessionFilePath(paneID), []byte(sessionID+"\n"), 0o644)

	// Migrate any old pane-keyed files to session-keyed (idempotent)
	MigrateToSessionKey(paneID, sessionID)

	// Write status and optional data, then nudge daemon with the change.
	// Build effect string alongside each action so it's always truthful.
	nd := nudgeData{PaneID: paneID, SessionID: sessionID, PermissionMode: input.PermissionMode}
	var effects []string
	switch hookType {
	case "UserPromptSubmit":
		nd.Status = StatusAgentTurn.String()
		WriteStatus(sessionID, StatusAgentTurn)
		effects = append(effects, "status → agent-turn")
		if input.Prompt != "" {
			os.WriteFile(lastMsgFilePath(sessionID), []byte(input.Prompt), 0o644)
			nd.LastUserMessage = input.Prompt
			effects = append(effects, "captured prompt")
		}
		// Clear transient states — user has responded, session is active again
		RemoveWaiting(sessionID)
		os.Remove(stopReasonFilePath(sessionID))
		nd.IsWaiting = boolPtr(false)

	case "PreToolUse":
		nd.Status = StatusAgentTurn.String()
		WriteStatus(sessionID, StatusAgentTurn)
		effects = append(effects, "status → agent-turn")
		// Clear transient states — tool use means Claude is proceeding
		RemoveWaiting(sessionID)
		os.Remove(stopReasonFilePath(sessionID))
		nd.IsWaiting = boolPtr(false)

	case "PostToolUse":
		// Detect git commit via Bash tool
		if input.ToolName == "Bash" {
			cmd := extractBashCommand(input.ToolInput)
			if isGitCommitCommand(cmd) {
				WriteLastAction(sessionID, "commit")
				nd.IsGitCommit = boolPtr(true)
				effects = append(effects, "git commit detected")
			}
		}
		// Edit/Write clears committed state
		if input.ToolName == "Edit" || input.ToolName == "Write" {
			WriteLastAction(sessionID, "edit")
			nd.IsFileEdit = boolPtr(true)
			effects = append(effects, "file edit; cleared commit")
		}

	case "Stop":
		nd.Status = StatusUserTurn.String()
		WriteStatus(sessionID, StatusUserTurn)
		effects = append(effects, "status → user-turn")
		if input.StopReason != "" {
			WriteStopReason(sessionID, input.StopReason)
			nd.StopReason = input.StopReason
			effects = append(effects, "reason:"+input.StopReason)
		}

	case "Notification":
		if input.NotifType == "permission_prompt" || input.NotifType == "elicitation_dialog" {
			nd.Status = StatusUserTurn.String()
			WriteStatus(sessionID, StatusUserTurn)
			WriteWaiting(sessionID, input.NotifType)
			nd.IsWaiting = boolPtr(true)
			effects = append(effects, "waiting:"+input.NotifType)
		}

	case "SessionStart":
		nd.Status = StatusUserTurn.String()
		WriteStatus(sessionID, StatusUserTurn)
		os.Remove(stopReasonFilePath(sessionID))
		effects = append(effects, "status → user-turn; session init")

	case "SessionEnd":
		RemoveSessionFiles(sessionID)
		RemovePaneMapping(paneID)
		nd.Remove = true
		effects = append(effects, "session cleanup; files removed")

	case "PreCompact":
		count := ReadCompactCount(sessionID)
		count++
		WriteCompactCount(sessionID, count)
		nd.Compacted = true
		effects = append(effects, fmt.Sprintf("compact #%d", count))
	}

	var effect string
	if len(effects) == 0 {
		effect = HookEffectNone
	} else {
		effect = strings.Join(effects, "; ")
	}

	// Nudge daemon first so we can annotate the log with dedup status
	shouldNudge := nd.Status != "" || nd.StopReason != "" || nd.IsWaiting != nil ||
		nd.IsGitCommit != nil || nd.IsFileEdit != nil || nd.Compacted || nd.Remove
	if shouldNudge {
		if nudgeDaemon(nd) {
			effect += HookEffectDedupSuffix
		}
	}

	// Append to hook log (compact JSON on one line, with effect annotation)
	compactJSON := compactJSONString(rawJSON)
	entry := fmt.Sprintf("%s %s\t%s\t%s\n", time.Now().Format("15:04:05"), hookType, compactJSON, effect)
	hooksPath := hookFilePath(sessionID)
	f, err := os.OpenFile(hooksPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		f.WriteString(entry)
		f.Close()
		// Trim when file exceeds ~60KB
		if info, err := os.Stat(hooksPath); err == nil && info.Size() > 61440 {
			trimHookFile(hooksPath)
		}
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
	SessionID       string `json:"sessionID,omitempty"`
	Status          string `json:"status"`
	LastUserMessage string `json:"lastUserMessage,omitempty"`
	StopReason      string `json:"stopReason,omitempty"`
	PermissionMode  string `json:"permissionMode,omitempty"`
	IsWaiting       *bool  `json:"isWaiting,omitempty"`
	IsGitCommit     *bool  `json:"isGitCommit,omitempty"`
	IsFileEdit      *bool  `json:"isFileEdit,omitempty"`
	Compacted       bool   `json:"compacted,omitempty"`
	Remove          bool   `json:"remove,omitempty"`
}

func boolPtr(v bool) *bool { return &v }

// nudgeDaemon sends a "nudge" RPC to the daemon with the status change data
// so it can patch the session in-place without re-polling.
// Returns true if the daemon reported the nudge was deduped (no state change).
func nudgeDaemon(nd nudgeData) bool {
	sock := filepath.Join(StatusDir(), "daemon.sock")
	conn, err := net.DialTimeout("unix", sock, 50*time.Millisecond)
	if err != nil {
		return false // daemon not running, no big deal
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))

	if err := json.NewEncoder(conn).Encode(nudgeRequest{
		Type: "nudge",
		Data: nd,
	}); err != nil {
		return false
	}

	// Read back response to check dedup status
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	var resp struct {
		Deduped bool `json:"deduped"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return false
	}
	return resp.Deduped
}

// extractBashCommand extracts the "command" field from PostToolUse tool_input JSON.
func extractBashCommand(toolInput json.RawMessage) string {
	var inp struct {
		Command string `json:"command"`
	}
	json.Unmarshal(toolInput, &inp)
	return inp.Command
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
