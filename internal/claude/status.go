package claude

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huylenq/spirit/internal/agent"
)

func statusDir() string {
	return StatusDir()
}

// StatusDir returns the path to the spirit state directory (~/.spirit).
// Cached because it's hit per-session every poll tick.
func StatusDir() string {
	statusDirOnce.Do(func() {
		home, _ := os.UserHomeDir()
		cachedStatusDir = filepath.Join(home, ".spirit")
	})
	return cachedStatusDir
}

var (
	cachedStatusDir string
	statusDirOnce   sync.Once
)

// OverrideStatusDirForTest points StatusDir at dir and returns a restore func.
// Test seam only (cross-package tests need to redirect session-file IO away
// from the real ~/.spirit); never call it from production code.
func OverrideStatusDirForTest(dir string) (restore func()) {
	old := cachedStatusDir
	statusDirOnce.Do(func() {}) // ensure the Once is spent before we override
	cachedStatusDir = dir
	return func() { cachedStatusDir = old }
}

// DaemonSocketPath returns the path to the daemon Unix socket.
// If the binary lives inside a git repository, the socket is scoped to that
// repo root under /tmp (matching the daemon's WorkdirDaemonInfo logic).
// Otherwise falls back to StatusDir()/daemon.sock.
func DaemonSocketPath() string {
	daemonSocketOnce.Do(func() {
		if exe, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(exe); err == nil {
				cmd := exec.Command("git", "rev-parse", "--show-toplevel")
				cmd.Dir = filepath.Dir(resolved)
				if out, err := cmd.Output(); err == nil {
					root := strings.TrimSpace(string(out))
					h := sha256.Sum256([]byte(root))
					cachedDaemonSocket = fmt.Sprintf("/tmp/spirit-%x.sock", h[:6])
					return
				}
			}
		}
		cachedDaemonSocket = filepath.Join(StatusDir(), "daemon.sock")
	})
	return cachedDaemonSocket
}

var (
	cachedDaemonSocket string
	daemonSocketOnce   sync.Once
)

func statusFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".status")
}

func sessionFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".session")
}

type SessionMeta = agent.SessionMeta

func metaFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".meta.json")
}

func ReadSessionMeta(sessionID string) SessionMeta {
	return (agent.Store{Dir: statusDir()}).ReadSessionMeta(sessionID)
}

func WriteSessionMeta(meta SessionMeta) error {
	return (agent.Store{Dir: statusDir()}).WriteSessionMeta(meta)
}

func ReadSessionID(paneID string) string {
	data, err := os.ReadFile(sessionFilePath(paneID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func ReadStatus(sessionID string) (Status, error) {
	data, err := os.ReadFile(statusFilePath(sessionID))
	if err != nil {
		return StatusUserTurn, err
	}
	s := strings.TrimSpace(string(data))
	switch s {
	case "agent-turn", "working":
		return StatusAgentTurn, nil
	case "user-turn", "stopped", "done", "later", "deferred":
		return StatusUserTurn, nil
	default:
		return StatusUserTurn, fmt.Errorf("unknown status: %s", s)
	}
}

func WriteStatus(sessionID string, status Status) error {
	dir := statusDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(statusFilePath(sessionID), []byte(status.String()+"\n"), 0o644)
}

func hookFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".hooks")
}

func lastMsgFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".lastmsg")
}

func ReadLastUserMessageCached(sessionID string) string {
	data, err := os.ReadFile(lastMsgFilePath(sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteLastUserMessageCached(sessionID, msg string) error {
	return os.WriteFile(lastMsgFilePath(sessionID), []byte(msg), 0o644)
}

// HookEffectNone is the sentinel value written when a hook triggers no state changes.
const HookEffectNone = "-"

// HookEffectDedupSuffix is appended to effect strings when the daemon reports
// that the nudge was redundant (no state change, no subscriber notification).
const HookEffectDedupSuffix = " [=]"

type HookEvent struct {
	Time     string
	HookType string
	Payload  string
	Effect   string // what spirit did with this hook (empty = legacy/no data)
}

// GlobalHookEffect is a handled hook event tagged with its source session's avatar.
type GlobalHookEffect struct {
	Time      string `json:"time"`
	HookType  string `json:"hookType"`
	Effect    string `json:"effect"`
	AnimalIdx int    `json:"animalIdx"`
	ColorIdx  int    `json:"colorIdx"`
	Count     int    `json:"count,omitempty"` // >1 when consecutive identical effects are merged
}

func ReadHookEvents(sessionID string) ([]HookEvent, error) {
	data, err := os.ReadFile(hookFilePath(sessionID))
	if err != nil {
		return nil, err
	}
	var events []HookEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		// Format: "HH:MM:SS HookType\tPayload\tEffect" (payload and effect optional)
		tabParts := strings.SplitN(line, "\t", 3)
		timeAndType := tabParts[0]
		payload := ""
		effect := ""
		if len(tabParts) >= 2 {
			payload = tabParts[1]
		}
		if len(tabParts) >= 3 {
			effect = tabParts[2]
		}
		tp := strings.SplitN(timeAndType, " ", 2)
		if len(tp) != 2 {
			continue
		}
		events = append(events, HookEvent{Time: tp[0], HookType: tp[1], Payload: payload, Effect: effect})
	}
	return events, nil
}

// --- Permission mode cache (mtime-based to avoid re-reading 50KB hooks file every poll) ---

type permModeCacheEntry struct {
	mode    string
	modTime time.Time
}

var (
	permModeCache   = make(map[string]permModeCacheEntry)
	permModeCacheMu sync.Mutex
)

func ReadPermissionMode(sessionID string) string {
	path := hookFilePath(sessionID)
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	// Check mtime cache — avoid re-reading if hooks file hasn't changed
	permModeCacheMu.Lock()
	if cached, ok := permModeCache[sessionID]; ok && cached.modTime.Equal(info.ModTime()) {
		permModeCacheMu.Unlock()
		return cached.mode
	}
	permModeCacheMu.Unlock()

	events, err := ReadHookEvents(sessionID)
	if err != nil {
		return ""
	}
	var mode string
	for i := len(events) - 1; i >= 0; i-- {
		p := events[i].Payload
		if idx := strings.Index(p, `"permission_mode"`); idx >= 0 {
			after := strings.TrimLeft(p[idx+len(`"permission_mode"`):], " :")
			if strings.HasPrefix(after, `"`) {
				if end := strings.Index(after[1:], `"`); end >= 0 {
					mode = after[1 : end+1]
					break
				}
			}
		}
	}

	permModeCacheMu.Lock()
	permModeCache[sessionID] = permModeCacheEntry{mode: mode, modTime: info.ModTime()}
	permModeCacheMu.Unlock()

	return mode
}

func queueFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".queue")
}

func ReadQueueMessages(sessionID string) []string {
	return (agent.Store{Dir: statusDir()}).ReadQueue(sessionID)
}

func WriteQueueMessages(sessionID string, messages []string) error {
	return (agent.Store{Dir: statusDir()}).WriteQueue(sessionID, messages)
}

func RemoveQueueMessage(sessionID string) {
	_ = (agent.Store{Dir: statusDir()}).RemoveQueue(sessionID)
}

func tagsFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".tags")
}

func ReadTags(sessionID string) []string {
	return (agent.Store{Dir: statusDir()}).ReadTags(sessionID)
}

func WriteTags(sessionID string, tags []string) error {
	return (agent.Store{Dir: statusDir()}).WriteTags(sessionID, tags)
}

func noteFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".note")
}

func ReadNote(sessionID string) string {
	return (agent.Store{Dir: statusDir()}).ReadNote(sessionID)
}

func WriteNote(sessionID, text string) error {
	return (agent.Store{Dir: statusDir()}).WriteNote(sessionID, text)
}

func RemoveNote(sessionID string) {
	_ = (agent.Store{Dir: statusDir()}).RemoveNote(sessionID)
}

func stopReasonFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".stopreason")
}

func waitingFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".waiting")
}

func compactCountFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".compactcount")
}

func lastActionFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".lastaction")
}

func skillFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".skill")
}

func ReadSkillName(sessionID string) string {
	data, err := os.ReadFile(skillFilePath(sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteSkillName(sessionID, name string) {
	os.WriteFile(skillFilePath(sessionID), []byte(name), 0o644)
}

func RemoveSkillName(sessionID string) {
	os.Remove(skillFilePath(sessionID))
}

func ReadStopReason(sessionID string) string {
	data, err := os.ReadFile(stopReasonFilePath(sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteStopReason(sessionID, reason string) {
	os.WriteFile(stopReasonFilePath(sessionID), []byte(reason), 0o644)
}

func ReadWaiting(sessionID string) bool {
	_, err := os.Stat(waitingFilePath(sessionID))
	return err == nil
}

// ReadWaitingInfo returns the waiting notification type (permission_prompt /
// elicitation_dialog) and the waiting file's mtime, or ("" , zero) when the
// session is not waiting. The mtime identifies the waiting episode for the
// perception ledger's rising-edge anchor.
func ReadWaitingInfo(sessionID string) (kind string, at time.Time) {
	path := waitingFilePath(sessionID)
	info, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}
	}
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data)), info.ModTime()
}

// StatusModTime exposes the status file's mtime (the last hook write); the
// perception ledger uses it as a last-resort turn discriminator.
func StatusModTime(sessionID string) time.Time {
	return getStatusModTime(sessionID)
}

func WriteWaiting(sessionID, notifType string) {
	os.WriteFile(waitingFilePath(sessionID), []byte(notifType), 0o644)
}

func RemoveWaiting(sessionID string) {
	os.Remove(waitingFilePath(sessionID))
}

func ReadCompactCount(sessionID string) int {
	data, err := os.ReadFile(compactCountFilePath(sessionID))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

func WriteCompactCount(sessionID string, n int) {
	os.WriteFile(compactCountFilePath(sessionID), []byte(strconv.Itoa(n)), 0o644)
}

func ReadLastAction(sessionID string) string {
	data, err := os.ReadFile(lastActionFilePath(sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteLastAction(sessionID, action string) {
	os.WriteFile(lastActionFilePath(sessionID), []byte(action), 0o644)
}

// RemoveSessionFiles removes all session-keyed status files for a session.
func RemoveSessionFiles(sessionID string) {
	os.Remove(statusFilePath(sessionID))
	os.Remove(hookFilePath(sessionID))
	os.Remove(lastMsgFilePath(sessionID))
	os.Remove(queueFilePath(sessionID))
	os.Remove(stopReasonFilePath(sessionID))
	os.Remove(waitingFilePath(sessionID))
	os.Remove(compactCountFilePath(sessionID))
	os.Remove(lastActionFilePath(sessionID))
	os.Remove(skillFilePath(sessionID))
	os.Remove(tagsFilePath(sessionID))
	os.Remove(noteFilePath(sessionID))
	os.Remove(metaFilePath(sessionID))
}

// RemovePaneMapping removes the pane→session reverse mapping file.
func RemovePaneMapping(paneID string) {
	os.Remove(sessionFilePath(paneID))
}

// MigrateToSessionKey renames old pane-keyed files to session-keyed.
// Idempotent: skips if old file doesn't exist, removes old if new already exists.
func MigrateToSessionKey(paneID, sessionID string) {
	if paneID == sessionID {
		return
	}
	dir := statusDir()
	exts := []string{".status", ".hooks", ".lastmsg", ".queue", ".stopreason", ".waiting", ".compactcount", ".lastaction", ".skill", ".tags", ".note", ".meta.json"}
	for _, ext := range exts {
		oldPath := filepath.Join(dir, paneID+ext)
		newPath := filepath.Join(dir, sessionID+ext)
		if _, err := os.Stat(oldPath); err != nil {
			continue // old file doesn't exist
		}
		if _, err := os.Stat(newPath); err == nil {
			os.Remove(oldPath) // new already exists, just clean up old
			continue
		}
		os.Rename(oldPath, newPath)
	}
}

// CleanStale removes status files for sessions and pane mappings that are no longer active.
func CleanStale(activeSessionIDs map[string]bool, activePaneIDs map[string]bool) error {
	dir := statusDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".status") {
			sessionID := strings.TrimSuffix(name, ".status")
			if !activeSessionIDs[sessionID] {
				RemoveSessionFiles(sessionID)
			}
		} else if strings.HasSuffix(name, ".session") {
			paneID := strings.TrimSuffix(name, ".session")
			if !activePaneIDs[paneID] {
				RemovePaneMapping(paneID)
			}
		}
	}
	return nil
}

// --- Later record CRUD ---

func laterDir() string {
	return filepath.Join(StatusDir(), "later")
}

func GenerateLaterID() string {
	return agent.GenerateLaterID()
}

func WriteLaterRecord(bm LaterRecord) error {
	return (agent.Store{Dir: statusDir()}).WriteLater(bm)
}

func ReadLaterRecord(id string) (*LaterRecord, error) {
	return (agent.Store{Dir: statusDir()}).ReadLater(id)
}

func ReadAllLaterRecords() ([]LaterRecord, error) {
	return (agent.Store{Dir: statusDir()}).ReadAllLaters()
}

func RemoveLaterRecord(id string) {
	_ = (agent.Store{Dir: statusDir()}).RemoveLater(id)
}

// FindLaterIDByPane scans Later records to find one matching the given pane ID.
func FindLaterIDByPane(paneID string) string {
	records, _ := ReadAllLaterRecords()
	for _, bm := range records {
		if bm.PaneID == paneID {
			return bm.ID
		}
	}
	return ""
}
