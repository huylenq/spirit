package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// TurnEventKind tags the content of a single TurnEvent.
type TurnEventKind string

const (
	TurnEventText      TurnEventKind = "text"
	TurnEventEdit      TurnEventKind = "edit"
	TurnEventWrite     TurnEventKind = "write"
	TurnEventMultiEdit TurnEventKind = "multi_edit"
)

// TurnEvent is one navigable step in the current turn — either an assistant
// text block or a file-touching tool call (Edit / Write / MultiEdit).
// Added/Removed are line-count deltas, same approximation as the old
// per-file stats: Write counts every line as added (no prior state in the
// JSONL). BlockCount records how many source content-blocks this event was
// merged from (>= 1) — used by the UI to walk past the right number of
// `⏺` markers in the captured pane when computing the nav target line.
type TurnEvent struct {
	Kind       TurnEventKind `json:"kind"`
	Text       string        `json:"text,omitempty"`     // populated for text events
	FilePath   string        `json:"filePath,omitempty"` // populated for tool events
	Added      int           `json:"added,omitempty"`
	Removed    int           `json:"removed,omitempty"`
	BlockCount int           `json:"blockCount,omitempty"`
}

// CurrentTurn is the chronological event stream since the last user message.
// Consecutive text events are merged: when two text blocks arrive with no
// file-touching tool call between them, they collapse into a single event.
type CurrentTurn struct {
	Events []TurnEvent `json:"events"`
}

type currentTurnCacheEntry struct {
	turn    CurrentTurn
	modTime time.Time
}

var (
	currentTurnCache   = make(map[string]currentTurnCacheEntry)
	currentTurnCacheMu sync.Mutex
)

// ReadCurrentTurn returns the chronological event stream for the current
// (in-progress or just-completed) turn — every assistant text block plus
// every Edit / Write / MultiEdit tool call after the last user message,
// in source order. Cached by mtime.
func ReadCurrentTurn(sessionID string) CurrentTurn {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return CurrentTurn{}
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return CurrentTurn{}
	}

	currentTurnCacheMu.Lock()
	if c, ok := currentTurnCache[sessionID]; ok && c.modTime.Equal(info.ModTime()) {
		currentTurnCacheMu.Unlock()
		return c.turn
	}
	currentTurnCacheMu.Unlock()

	// Read the whole file: the last user-typed message can sit arbitrarily far
	// behind the file end for long agent runs, so any fixed-size tail fails
	// for sessions with one user msg + hundreds of tool calls. mtime caching
	// keeps the cost off the hot path.
	data, err := os.ReadFile(path)
	if err != nil {
		return CurrentTurn{}
	}
	lines := bytes.Split(data, []byte{'\n'})

	lastUserIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i]) == 0 {
			continue
		}
		if extractUserText(lines[i]) != "" {
			lastUserIdx = i
			break
		}
	}

	var turn CurrentTurn
	if lastUserIdx < 0 {
		currentTurnCacheMu.Lock()
		currentTurnCache[sessionID] = currentTurnCacheEntry{turn: turn, modTime: info.ModTime()}
		currentTurnCacheMu.Unlock()
		return turn
	}

	isCodex := ReadSessionMeta(sessionID).Provider == ProviderCodex
	for i := lastUserIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if len(line) == 0 {
			continue
		}
		if isCodex {
			if text := extractAssistantText(line); text != "" {
				appendTextEvent(&turn.Events, text, 1)
			}
			continue
		}
		if !bytes.Contains(line, []byte(`"type":"assistant"`)) {
			continue
		}
		appendAssistantEvents(line, &turn.Events)
	}

	currentTurnCacheMu.Lock()
	currentTurnCache[sessionID] = currentTurnCacheEntry{turn: turn, modTime: info.ModTime()}
	currentTurnCacheMu.Unlock()
	return turn
}

func appendTextEvent(events *[]TurnEvent, text string, blockCount int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if n := len(*events); n > 0 && (*events)[n-1].Kind == TurnEventText {
		(*events)[n-1].Text += "\n\n" + text
		(*events)[n-1].BlockCount += blockCount
		return
	}
	*events = append(*events, TurnEvent{Kind: TurnEventText, Text: text, BlockCount: blockCount})
}

// turnBlock is a unified content-block decoder that handles both text and
// tool_use shapes from the JSONL.
type turnBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type multiEditEntry struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

type multiEditInput struct {
	FilePath string           `json:"file_path"`
	Edits    []multiEditEntry `json:"edits"`
}

// appendFileEvent appends a tool event, merging into the previous event when
// it touched the same file. Stats sum, BlockCount sums — so navigation can
// still skip the right number of `⏺` markers in the captured pane.
func appendFileEvent(events *[]TurnEvent, ev TurnEvent) {
	if n := len(*events); n > 0 {
		last := &(*events)[n-1]
		if last.FilePath != "" && last.FilePath == ev.FilePath {
			last.Added += ev.Added
			last.Removed += ev.Removed
			last.BlockCount += ev.BlockCount
			return
		}
	}
	*events = append(*events, ev)
}

// isFileToolEvent reports whether ev is a file-mutating tool call.
func isFileToolEvent(ev TurnEvent) bool {
	switch ev.Kind {
	case TurnEventEdit, TurnEventWrite, TurnEventMultiEdit:
		return true
	}
	return false
}

// FilteredEvents returns a presentation slice for the chat outline.
//
// When hideInterleaved is false: returns the input slice with identity sources
// (sources[i] = [i]) and hidden=0.
//
// When hideInterleaved is true: drops "interleaved" text events — text events
// that have a file-tool event both before and after them — then re-runs the
// same-file merge over the filtered slice (using appendFileEvent's rule). Texts
// at the beginning or end of the turn are preserved; pure-text turns are
// untouched.
//
// `sources[v]` is the list of raw event indices that compose visibleEvents[v].
// For non-merged events it has length 1; for merged events its length matches
// the number of source events combined. Used to map cursor positions across
// the toggle.
func FilteredEvents(events []TurnEvent, hideInterleaved bool) (visible []TurnEvent, sources [][]int, hidden int) {
	if !hideInterleaved {
		sources = make([][]int, len(events))
		for i := range events {
			sources[i] = []int{i}
		}
		return events, sources, 0
	}
	n := len(events)
	hasToolBefore := make([]bool, n)
	seen := false
	for i := 0; i < n; i++ {
		hasToolBefore[i] = seen
		if isFileToolEvent(events[i]) {
			seen = true
		}
	}
	hasToolAfter := make([]bool, n)
	seen = false
	for i := n - 1; i >= 0; i-- {
		hasToolAfter[i] = seen
		if isFileToolEvent(events[i]) {
			seen = true
		}
	}

	for i, ev := range events {
		if ev.Kind == TurnEventText && hasToolBefore[i] && hasToolAfter[i] {
			hidden++
			continue
		}
		preLen := len(visible)
		appendFileEvent(&visible, ev)
		if len(visible) == preLen {
			sources[len(sources)-1] = append(sources[len(sources)-1], i)
		} else {
			sources = append(sources, []int{i})
		}
	}
	return visible, sources, hidden
}

func appendAssistantEvents(line []byte, events *[]TurnEvent) {
	var tl transcriptLine
	if json.Unmarshal(line, &tl) != nil || tl.Type != "assistant" {
		return
	}
	var msg messageContent
	if json.Unmarshal(tl.Message, &msg) != nil {
		return
	}
	var blocks []turnBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			text := strings.TrimSpace(b.Text)
			if text == "" {
				continue
			}
			// Merge consecutive text events (no file-touching tool between them).
			if n := len(*events); n > 0 && (*events)[n-1].Kind == TurnEventText {
				(*events)[n-1].Text += "\n\n" + text
				(*events)[n-1].BlockCount++
				continue
			}
			*events = append(*events, TurnEvent{Kind: TurnEventText, Text: text, BlockCount: 1})

		case "tool_use":
			switch b.Name {
			case "Edit":
				var inp editInput
				if json.Unmarshal(b.Input, &inp) != nil || inp.FilePath == "" {
					continue
				}
				appendFileEvent(events, TurnEvent{
					Kind:       TurnEventEdit,
					FilePath:   inp.FilePath,
					Added:      strings.Count(inp.NewString, "\n"),
					Removed:    strings.Count(inp.OldString, "\n"),
					BlockCount: 1,
				})
			case "Write":
				var inp writeInput
				if json.Unmarshal(b.Input, &inp) != nil || inp.FilePath == "" {
					continue
				}
				appendFileEvent(events, TurnEvent{
					Kind:       TurnEventWrite,
					FilePath:   inp.FilePath,
					Added:      strings.Count(inp.Content, "\n"),
					BlockCount: 1,
				})
			case "MultiEdit":
				var inp multiEditInput
				if json.Unmarshal(b.Input, &inp) != nil || inp.FilePath == "" {
					continue
				}
				var added, removed int
				for _, e := range inp.Edits {
					added += strings.Count(e.NewString, "\n")
					removed += strings.Count(e.OldString, "\n")
				}
				appendFileEvent(events, TurnEvent{
					Kind:       TurnEventMultiEdit,
					FilePath:   inp.FilePath,
					Added:      added,
					Removed:    removed,
					BlockCount: 1,
				})
			}
		}
	}
}
