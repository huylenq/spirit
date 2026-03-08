package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func statusDir() string {
	return StatusDir()
}

// StatusDir returns the path to the cmc cache directory.
func StatusDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "cmc")
}

func statusFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".status")
}

func deferFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".defer")
}

func sessionFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".session")
}

func ReadSessionID(paneID string) string {
	data, err := os.ReadFile(sessionFilePath(paneID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func ReadStatus(paneID string) (Status, error) {
	data, err := os.ReadFile(statusFilePath(paneID))
	if err != nil {
		return StatusDone, err
	}
	switch strings.TrimSpace(string(data)) {
	case "working":
		return StatusWorking, nil
	case "stopped", "done":
		return StatusDone, nil
	case "deferred":
		return StatusDeferred, nil
	default:
		return StatusDone, fmt.Errorf("unknown status: %s", string(data))
	}
}

func WriteStatus(paneID string, status Status) error {
	dir := statusDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(statusFilePath(paneID), []byte(status.String()+"\n"), 0o644)
}

func ReadDeferUntil(paneID string) (time.Time, error) {
	data, err := os.ReadFile(deferFilePath(paneID))
	if err != nil {
		return time.Time{}, err
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(epoch, 0), nil
}

func WriteDeferUntil(paneID string, until time.Time) error {
	dir := statusDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	epoch := strconv.FormatInt(until.Unix(), 10)
	if err := WriteStatus(paneID, StatusDeferred); err != nil {
		return err
	}
	return os.WriteFile(deferFilePath(paneID), []byte(epoch+"\n"), 0o644)
}

func hookFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".hooks")
}

func lastMsgFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".lastmsg")
}

func ReadLastUserMessageCached(paneID string) string {
	data, err := os.ReadFile(lastMsgFilePath(paneID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteLastUserMessageCached(paneID, msg string) error {
	return os.WriteFile(lastMsgFilePath(paneID), []byte(msg), 0o644)
}

type HookEvent struct {
	Time     string
	HookType string
	Payload  string
}

func ReadHookEvents(paneID string) ([]HookEvent, error) {
	data, err := os.ReadFile(hookFilePath(paneID))
	if err != nil {
		return nil, err
	}
	var events []HookEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		// Format: "HH:MM:SS HookType\tPayload" (payload optional)
		tabParts := strings.SplitN(line, "\t", 2)
		timeAndType := tabParts[0]
		payload := ""
		if len(tabParts) == 2 {
			payload = tabParts[1]
		}
		tp := strings.SplitN(timeAndType, " ", 2)
		if len(tp) != 2 {
			continue
		}
		events = append(events, HookEvent{Time: tp[0], HookType: tp[1], Payload: payload})
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

func ReadPermissionMode(paneID string) string {
	path := hookFilePath(paneID)
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	// Check mtime cache — avoid re-reading if hooks file hasn't changed
	permModeCacheMu.Lock()
	if cached, ok := permModeCache[paneID]; ok && cached.modTime.Equal(info.ModTime()) {
		permModeCacheMu.Unlock()
		return cached.mode
	}
	permModeCacheMu.Unlock()

	events, err := ReadHookEvents(paneID)
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
	permModeCache[paneID] = permModeCacheEntry{mode: mode, modTime: info.ModTime()}
	permModeCacheMu.Unlock()

	return mode
}

func enqueueFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".enqueue")
}

func ReadEnqueueMessage(paneID string) string {
	data, err := os.ReadFile(enqueueFilePath(paneID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteEnqueueMessage(paneID, message string) error {
	dir := statusDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(enqueueFilePath(paneID), []byte(message), 0o644)
}

func RemoveEnqueueMessage(paneID string) {
	os.Remove(enqueueFilePath(paneID))
}

func RemoveStatus(paneID string) {
	os.Remove(statusFilePath(paneID))
	os.Remove(deferFilePath(paneID))
	os.Remove(sessionFilePath(paneID))
	os.Remove(hookFilePath(paneID))
	os.Remove(lastMsgFilePath(paneID))
	os.Remove(enqueueFilePath(paneID))
}

func ClearDefer(paneID string) {
	os.Remove(deferFilePath(paneID))
}

func Undefer(paneID string) {
	ClearDefer(paneID)
	WriteStatus(paneID, StatusDone)
}

func CleanStale(activePaneIDs map[string]bool) error {
	dir := statusDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".status") {
			continue
		}
		paneID := strings.TrimSuffix(e.Name(), ".status")
		if !activePaneIDs[paneID] {
			RemoveStatus(paneID)
		}
	}
	return nil
}
