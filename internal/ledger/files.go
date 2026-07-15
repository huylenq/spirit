package ledger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const segmentPrefix = "signals-"

func (l *Ledger) segmentPath(day time.Time) string {
	return filepath.Join(l.dir, segmentPrefix+day.Format("2006-01-02")+".ndjson")
}

func (l *Ledger) attentionPath() string { return filepath.Join(l.dir, "attention.json") }
func (l *Ledger) cursorsPath() string   { return filepath.Join(l.dir, "cursors.json") }

// appendSignal appends one signal to today's day segment.
func (l *Ledger) appendSignal(sig Signal) error {
	data, err := json.Marshal(sig)
	if err != nil {
		return fmt.Errorf("marshal signal: %w", err)
	}
	f, err := os.OpenFile(l.segmentPath(sig.ObservedAt), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

// loadSegments reads every day segment within the window into the in-memory
// index. Corrupt lines are skipped with a log line — a torn write or a manual
// edit must never wedge the daemon.
func (l *Ledger) loadSegments() {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		log.Printf("ledger: read dir: %v", err)
		return
	}
	oldest := l.now().Add(-l.window).Format("2006-01-02")
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), ".ndjson")
		if day < oldest {
			continue // outside the window
		}
		names = append(names, name)
	}
	sort.Strings(names) // day order = time order

	for _, name := range names {
		path := filepath.Join(l.dir, name)
		f, err := os.Open(path)
		if err != nil {
			log.Printf("ledger: open %s: %v", name, err)
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var sig Signal
			if err := json.Unmarshal(line, &sig); err != nil || sig.ID == "" || sig.Kind == "" {
				log.Printf("ledger: %s:%d: skipping corrupt line: %v", name, lineNo, err)
				continue
			}
			l.signals = append(l.signals, sig)
			l.byKey[dedupKey(sig.Kind, sig.Anchor)] = sig.ID
		}
		if err := scanner.Err(); err != nil {
			log.Printf("ledger: scan %s: %v", name, err)
		}
		f.Close()
	}
	// Re-point byID after all appends (append may have reallocated l.signals).
	l.byID = make(map[string]*Signal, len(l.signals))
	for i := range l.signals {
		l.byID[l.signals[i].ID] = &l.signals[i]
	}
}

func (l *Ledger) loadAttention() {
	data, err := os.ReadFile(l.attentionPath())
	if err != nil {
		return // fresh ledger
	}
	var items []*AttentionItem
	if err := json.Unmarshal(data, &items); err != nil {
		log.Printf("ledger: attention.json corrupt, starting empty: %v", err)
		return
	}
	l.items = items
}

// saveAttention atomically rewrites attention.json, pruning resolved/expired
// items older than the window so the file stays small.
func (l *Ledger) saveAttention() {
	cutoff := l.now().Add(-l.window)
	keep := make([]*AttentionItem, 0, len(l.items))
	for _, it := range l.items {
		if (it.Status == StatusResolved || it.Status == StatusExpired) && it.UpdatedAt.Before(cutoff) {
			continue
		}
		keep = append(keep, it)
	}
	l.items = keep
	if err := atomicWriteJSON(l.attentionPath(), keep); err != nil {
		log.Printf("ledger: save attention: %v", err)
	}
}

func (l *Ledger) loadCursors() {
	data, err := os.ReadFile(l.cursorsPath())
	if err != nil {
		return
	}
	var cursors map[string]Cursor
	if err := json.Unmarshal(data, &cursors); err != nil {
		log.Printf("ledger: cursors.json corrupt, starting empty: %v", err)
		return
	}
	l.cursors = cursors
}

// saveCursors atomically rewrites cursors.json, pruning the oldest cursors
// past maxCursors (stale Hermes sessions from long-gone /new generations).
func (l *Ledger) saveCursors() {
	if len(l.cursors) > maxCursors {
		type aged struct {
			owner string
			at    time.Time
		}
		all := make([]aged, 0, len(l.cursors))
		for owner, c := range l.cursors {
			all = append(all, aged{owner, c.UpdatedAt})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
		for _, a := range all[:len(all)-maxCursors] {
			delete(l.cursors, a.owner)
		}
	}
	if err := atomicWriteJSON(l.cursorsPath(), l.cursors); err != nil {
		log.Printf("ledger: save cursors: %v", err)
	}
}

// atomicWriteJSON writes v as JSON via a temp file + rename so a crash cannot
// leave a torn file.
func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
