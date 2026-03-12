package ui

import (
	"encoding/json"
	"strings"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

// Hook event management for DetailModel.

// Hook filter modes.
const (
	hookFilterAll       = 0
	hookFilterHandled   = 1
	hookFilterUnhandled = 2
	hookFilterCount     = 3 // for modular cycling
)

func (m *DetailModel) SetShowHooks(show bool) {
	m.showHooks = show
	m.hookScroll = 0
	m.hookCursor = 0
	m.hookFilter = 0
	m.hookFiltered = nil
	if show {
		m.hookExpanded = make(map[int]bool)
		m.hookExpandedJSON = make(map[int]string)
	} else {
		m.hookEvents = nil
		m.hookExpanded = nil
		m.hookExpandedJSON = nil
	}
}

// CycleHookFilter cycles through hook filter modes: all → handled → unhandled → all.
func (m *DetailModel) CycleHookFilter() {
	m.hookFilter = (m.hookFilter + 1) % 3
	m.hookCursor = 0
	m.hookScroll = 0
	m.hookExpanded = make(map[int]bool)
	m.hookExpandedJSON = make(map[int]string)
	m.rebuildHookFiltered()
}

// rebuildHookFiltered rebuilds the cached filtered+reversed hook event list.
func (m *DetailModel) rebuildHookFiltered() {
	filtered := make([]claude.HookEvent, 0, len(m.hookEvents))
	for i := len(m.hookEvents) - 1; i >= 0; i-- {
		ev := m.hookEvents[i]
		handled := hookIsHandled(ev)
		switch m.hookFilter {
		case 1: // handled only
			if !handled {
				continue
			}
		case 2: // unhandled only
			if handled {
				continue
			}
		}
		filtered = append(filtered, ev)
	}
	m.hookFiltered = filtered
}

func (m *DetailModel) SetHookEvents(events []claude.HookEvent) {
	m.hookEvents = events
	m.hookExpanded = make(map[int]bool)       // reset — filtered indices shift
	m.hookExpandedJSON = make(map[int]string) // invalidate cache
	m.rebuildHookFiltered()
}

// hookIsHandled returns true if the hook event had a meaningful effect (not passthrough, not legacy).
func hookIsHandled(ev claude.HookEvent) bool {
	return ev.Effect != "" && ev.Effect != claude.HookEffectNone
}

// hookVisLines returns the number of visible lines for the hook event overlay.
func (m *DetailModel) hookVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// ensureHookCursorVisible adjusts hookScroll so the cursor is in view,
// accounting for expanded entries consuming extra lines.
func (m *DetailModel) ensureHookCursorVisible() {
	avail := m.hookVisLines()
	if avail < 1 {
		return
	}
	if m.hookCursor < m.hookScroll {
		m.hookScroll = m.hookCursor
		return
	}
	usedLines := 0
	for i := m.hookScroll; i <= m.hookCursor && i < len(m.hookFiltered); i++ {
		usedLines++
		if m.hookExpanded[i] {
			usedLines += m.hookExpandedLineCount(i)
		}
	}
	for usedLines > avail && m.hookScroll < m.hookCursor {
		usedLines--
		if m.hookExpanded[m.hookScroll] {
			usedLines -= m.hookExpandedLineCount(m.hookScroll)
		}
		m.hookScroll++
	}
}

// hookExpandedLineCount returns the number of extra lines an expanded hook entry consumes.
func (m *DetailModel) hookExpandedLineCount(idx int) int {
	s := m.getHookExpandedJSON(idx)
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// getHookExpandedJSON returns the pretty-printed payload JSON for a hook entry, caching lazily.
func (m *DetailModel) getHookExpandedJSON(idx int) string {
	if idx < 0 || idx >= len(m.hookFiltered) {
		return ""
	}
	if cached, ok := m.hookExpandedJSON[idx]; ok {
		return cached
	}
	raw := m.hookFiltered[idx].Payload
	if raw == "" {
		m.hookExpandedJSON[idx] = ""
		return ""
	}
	var v interface{}
	if json.Unmarshal([]byte(raw), &v) != nil {
		m.hookExpandedJSON[idx] = raw
		return raw
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		m.hookExpandedJSON[idx] = raw
		return raw
	}
	result := string(b)
	m.hookExpandedJSON[idx] = result
	return result
}
