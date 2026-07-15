package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct{ Dir string }

func (s Store) path(id, suffix string) string { return filepath.Join(s.Dir, id+suffix) }

type SessionMeta struct {
	Provider       ProviderID `json:"provider"`
	SessionID      string     `json:"sessionID"`
	TurnID         string     `json:"turnID,omitempty"`
	TranscriptPath string     `json:"transcriptPath,omitempty"`
	CWD            string     `json:"cwd,omitempty"`
	Model          string     `json:"model,omitempty"`
}

func (s Store) ReadSessionMeta(sessionID string) SessionMeta {
	data, err := os.ReadFile(s.path(sessionID, ".meta.json"))
	if err != nil {
		return SessionMeta{Provider: ProviderClaude, SessionID: sessionID}
	}
	var meta SessionMeta
	if json.Unmarshal(data, &meta) != nil {
		return SessionMeta{Provider: ProviderClaude, SessionID: sessionID}
	}
	meta.Provider = ParseProviderID(string(meta.Provider))
	if meta.SessionID == "" {
		meta.SessionID = sessionID
	}
	return meta
}

func (s Store) WriteSessionMeta(meta SessionMeta) error {
	if meta.SessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	previous := s.ReadSessionMeta(meta.SessionID)
	if meta.Provider == "" {
		meta.Provider = previous.Provider
	}
	if meta.TurnID == "" {
		meta.TurnID = previous.TurnID
	}
	if meta.TranscriptPath == "" {
		meta.TranscriptPath = previous.TranscriptPath
	}
	if meta.CWD == "" {
		meta.CWD = previous.CWD
	}
	if meta.Model == "" {
		meta.Model = previous.Model
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path(meta.SessionID, ".meta.json"), data, 0o644)
}

// QueueItem is one queued message with a durable identity (W8). The ID is
// minted at enqueue and survives daemon restarts via the .queue file, so a
// queued instruction → its delivery → the turn it caused is one traceable
// chain. ActionID links the item back to the batch/MCP action that enqueued
// it, when there was one.
type QueueItem struct {
	ID         string    `json:"id"`
	Message    string    `json:"message"`
	ActionID   string    `json:"action_id,omitempty"`
	EnqueuedAt time.Time `json:"enqueued_at,omitempty"`
}

// NewQueueItemID mints a short, collision-resistant queue item id like
// "qi_9f3c1a2b7d4e".
func NewQueueItemID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Time-based fallback is still unique enough to correlate.
		return "qi_" + time.Now().UTC().Format("20060102150405.000000")
	}
	return "qi_" + hex.EncodeToString(b[:])
}

// ReadQueueItems reads a session's queue file. Three historical formats are
// accepted: the current object array, the legacy JSON string array, and the
// oldest single-message plain text. Legacy entries are upgraded in place —
// fresh ids are minted ONCE and the file is rewritten in the current format,
// so ids are stable from then on (deliberate one-way migration, W8).
func (s Store) ReadQueueItems(id string) []QueueItem {
	data, err := os.ReadFile(s.path(id, ".queue"))
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "[") {
		var items []QueueItem
		if err := json.Unmarshal(data, &items); err == nil {
			ok := true
			for _, it := range items {
				if it.ID == "" {
					ok = false
					break
				}
			}
			if ok {
				return items
			}
		}
		var messages []string
		if json.Unmarshal(data, &messages) != nil {
			return nil
		}
		items = queueItemsFromMessages(messages)
		s.WriteQueueItems(id, items) //nolint:errcheck // upgrade is best-effort; items are still valid in memory
		return items
	}
	items := queueItemsFromMessages([]string{text})
	s.WriteQueueItems(id, items) //nolint:errcheck
	return items
}

func queueItemsFromMessages(messages []string) []QueueItem {
	items := make([]QueueItem, 0, len(messages))
	for _, m := range messages {
		items = append(items, QueueItem{ID: NewQueueItemID(), Message: m})
	}
	return items
}

// WriteQueueItems persists a session's queue in the current object-array
// format; an empty queue removes the file.
func (s Store) WriteQueueItems(id string, items []QueueItem) error {
	if len(items) == 0 {
		return s.RemoveQueue(id)
	}
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(id, ".queue"), data, 0o644)
}

// ReadQueue returns the queued message texts (derived from ReadQueueItems;
// kept for surfaces that only display messages).
func (s Store) ReadQueue(id string) []string {
	items := s.ReadQueueItems(id)
	if len(items) == 0 {
		return nil
	}
	messages := make([]string, 0, len(items))
	for _, it := range items {
		messages = append(messages, it.Message)
	}
	return messages
}

// WriteQueue persists plain messages, minting fresh item ids (legacy-shaped
// helper; prefer WriteQueueItems).
func (s Store) WriteQueue(id string, messages []string) error {
	return s.WriteQueueItems(id, queueItemsFromMessages(messages))
}

func (s Store) RemoveQueue(id string) error { return removeIfPresent(s.path(id, ".queue")) }

func (s Store) ReadTags(id string) []string {
	data, err := os.ReadFile(s.path(id, ".tags"))
	if err != nil {
		return nil
	}
	var tags []string
	for _, tag := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func (s Store) WriteTags(id string, tags []string) error {
	if len(tags) == 0 {
		return removeIfPresent(s.path(id, ".tags"))
	}
	return os.WriteFile(s.path(id, ".tags"), []byte(strings.Join(tags, "\n")+"\n"), 0o644)
}

func (s Store) ReadNote(id string) string {
	data, err := os.ReadFile(s.path(id, ".note"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (s Store) WriteNote(id, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return s.RemoveNote(id)
	}
	return os.WriteFile(s.path(id, ".note"), []byte(note), 0o644)
}

func (s Store) RemoveNote(id string) error { return removeIfPresent(s.path(id, ".note")) }

func (s Store) laterDir() string { return filepath.Join(s.Dir, "later") }

func GenerateLaterID() string {
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func (s Store) WriteLater(record LaterRecord) error {
	if err := os.MkdirAll(s.laterDir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.laterDir(), record.ID+".json"), data, 0o644)
}

func (s Store) ReadLater(id string) (*LaterRecord, error) {
	data, err := os.ReadFile(filepath.Join(s.laterDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var record LaterRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.Provider == "" {
		record.Provider = ProviderClaude
	}
	return &record, nil
}

func (s Store) ReadAllLaters() ([]LaterRecord, error) {
	entries, err := os.ReadDir(s.laterDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	records := make([]LaterRecord, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.laterDir(), entry.Name()))
		if err != nil {
			continue
		}
		var record LaterRecord
		if json.Unmarshal(data, &record) != nil {
			continue
		}
		if record.Provider == "" {
			record.Provider = ProviderClaude
		}
		records = append(records, record)
	}
	return records, nil
}

func (s Store) RemoveLater(id string) error {
	return removeIfPresent(filepath.Join(s.laterDir(), id+".json"))
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
