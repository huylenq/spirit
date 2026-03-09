package claude

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	case "later", "deferred":
		return StatusLater, nil
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

func queueFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".queue")
}

func ReadQueueMessage(paneID string) string {
	data, err := os.ReadFile(queueFilePath(paneID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteQueueMessage(paneID, message string) error {
	return os.WriteFile(queueFilePath(paneID), []byte(message), 0o644)
}

func RemoveQueueMessage(paneID string) {
	os.Remove(queueFilePath(paneID))
}

func stopReasonFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".stopreason")
}

func waitingFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".waiting")
}

func compactCountFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".compactcount")
}

func lastActionFilePath(paneID string) string {
	return filepath.Join(statusDir(), paneID+".lastaction")
}

func ReadStopReason(paneID string) string {
	data, err := os.ReadFile(stopReasonFilePath(paneID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteStopReason(paneID, reason string) {
	os.WriteFile(stopReasonFilePath(paneID), []byte(reason), 0o644)
}

func ReadWaiting(paneID string) bool {
	_, err := os.Stat(waitingFilePath(paneID))
	return err == nil
}

func WriteWaiting(paneID, notifType string) {
	os.WriteFile(waitingFilePath(paneID), []byte(notifType), 0o644)
}

func RemoveWaiting(paneID string) {
	os.Remove(waitingFilePath(paneID))
}

func ReadCompactCount(paneID string) int {
	data, err := os.ReadFile(compactCountFilePath(paneID))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

func WriteCompactCount(paneID string, n int) {
	os.WriteFile(compactCountFilePath(paneID), []byte(strconv.Itoa(n)), 0o644)
}

func ReadLastAction(paneID string) string {
	data, err := os.ReadFile(lastActionFilePath(paneID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func WriteLastAction(paneID, action string) {
	os.WriteFile(lastActionFilePath(paneID), []byte(action), 0o644)
}

func RemoveStatus(paneID string) {
	os.Remove(statusFilePath(paneID))
	os.Remove(sessionFilePath(paneID))
	os.Remove(hookFilePath(paneID))
	os.Remove(lastMsgFilePath(paneID))
	os.Remove(queueFilePath(paneID))
	os.Remove(stopReasonFilePath(paneID))
	os.Remove(waitingFilePath(paneID))
	os.Remove(compactCountFilePath(paneID))
	os.Remove(lastActionFilePath(paneID))
}

func CleanStale(activePaneIDs, laterPaneIDs map[string]bool) error {
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
		if !activePaneIDs[paneID] && !laterPaneIDs[paneID] {
			RemoveStatus(paneID)
		}
	}
	return nil
}

// --- Later bookmark CRUD ---

func laterDir() string {
	return filepath.Join(StatusDir(), "later")
}

func GenerateBookmarkID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func WriteLaterBookmark(bm LaterBookmark) error {
	dir := laterDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(bm)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, bm.ID+".json"), data, 0o644)
}

func ReadLaterBookmark(id string) (*LaterBookmark, error) {
	data, err := os.ReadFile(filepath.Join(laterDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var bm LaterBookmark
	if err := json.Unmarshal(data, &bm); err != nil {
		return nil, err
	}
	return &bm, nil
}

func ReadAllLaterBookmarks() ([]LaterBookmark, error) {
	dir := laterDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var bookmarks []LaterBookmark
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var bm LaterBookmark
		if err := json.Unmarshal(data, &bm); err != nil {
			continue
		}
		bookmarks = append(bookmarks, bm)
	}
	return bookmarks, nil
}

func RemoveLaterBookmark(id string) {
	os.Remove(filepath.Join(laterDir(), id+".json"))
}

// FindBookmarkIDByPane scans bookmarks to find one matching the given pane ID.
func FindBookmarkIDByPane(paneID string) string {
	bookmarks, _ := ReadAllLaterBookmarks()
	for _, bm := range bookmarks {
		if bm.PaneID == paneID {
			return bm.ID
		}
	}
	return ""
}

func WriteLaterStatus(paneID string) error {
	return WriteStatus(paneID, StatusLater)
}




