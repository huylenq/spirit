package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

func messageLogPath() string {
	return filepath.Join(claude.StatusDir(), "messagelog.json")
}

// messageLogEntryJSON is the on-disk representation (Time as Unix ms for compactness).
type messageLogEntryJSON struct {
	Text    string `json:"text"`
	IsError bool   `json:"isError,omitempty"`
	Time    int64  `json:"time"` // Unix milliseconds
}

func loadMessageLog() []MessageLogEntry {
	data, err := os.ReadFile(messageLogPath())
	if err != nil {
		return nil
	}
	var raw []messageLogEntryJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	entries := make([]MessageLogEntry, 0, len(raw))
	for _, r := range raw {
		entries = append(entries, MessageLogEntry{
			Text:    r.Text,
			IsError: r.IsError,
			Time:    time.UnixMilli(r.Time),
		})
	}
	return entries
}

func saveMessageLog(entries []MessageLogEntry) {
	raw := make([]messageLogEntryJSON, len(entries))
	for i, e := range entries {
		raw[i] = messageLogEntryJSON{
			Text:    e.Text,
			IsError: e.IsError,
			Time:    e.Time.UnixMilli(),
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	os.WriteFile(messageLogPath(), data, 0o644)
}
