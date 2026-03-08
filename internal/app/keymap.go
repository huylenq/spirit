package app

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

func (k KeyMap) ShortHelp() []key.Binding {
	bindings := []key.Binding{
		k.Up, k.Enter, k.PromptRelay, k.Filter, k.Defer, k.Undefer,
		k.Refresh, k.GroupMode, k.Summarize, k.SummarizeAll,
		k.Rename, k.Hooks, k.Minimap, k.ListShrink, k.Fullscreen, k.Kill, k.CommitAndDone,
	}
	bindings = append(bindings, chordBindings()...)
	bindings = append(bindings, k.Quit)
	return bindings
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Enter, k.Filter, k.Refresh, k.Quit},
		{k.Defer, k.Undefer, k.GroupMode, k.Minimap},
		{k.Summarize, k.SummarizeAll, k.Rename, k.Hooks},
		{k.ScrollDown, k.MsgNext, k.ListShrink, k.SpatialUp},
	}
}

type KeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Enter   key.Binding
	Filter  key.Binding
	Defer   key.Binding
	Undefer key.Binding
	Refresh key.Binding
	Hooks   key.Binding
	Quit    key.Binding
	Escape  key.Binding

	Minimap      key.Binding
	GroupMode    key.Binding
	Summarize    key.Binding
	SummarizeAll key.Binding
	Rename       key.Binding

	// Spatial navigation (minimap)
	SpatialUp    key.Binding
	SpatialDown  key.Binding
	SpatialLeft  key.Binding
	SpatialRight key.Binding

	// Preview free-scrolling (line-by-line)
	ScrollDown key.Binding
	ScrollUp   key.Binding

	// Conversation message navigation
	MsgNext key.Binding
	MsgPrev key.Binding

	// List panel resize
	ListShrink key.Binding
	ListGrow   key.Binding

	// Popup fullscreen toggle
	Fullscreen key.Binding

	// Prompt relay (send message to Claude session)
	PromptRelay key.Binding

	// Kill session + close pane
	Kill key.Binding

	// Commit and done (send /commit, wait, verify, kill)
	CommitAndDone key.Binding

	// Debug overlay toggle
	Debug key.Binding
}

// chordBindings returns one key.Binding per unique chord starter key for the help bar.
// e.g. chords "ys", "yp" → single entry "y copy…"
func chordBindings() []key.Binding {
	seen := make(map[string]bool)
	var out []key.Binding
	for _, c := range Chords {
		starter := c.Keys[:1]
		if seen[starter] {
			continue
		}
		seen[starter] = true
		out = append(out, key.NewBinding(
			key.WithKeys(starter),
			key.WithHelp(starter, "copy…"),
		))
	}
	return out
}

// Chord defines a multi-key sequence binding.
type Chord struct {
	Keys string // full key sequence, e.g. "ys"
	Help string // description shown in footer
}

// Chords is the global list of chord bindings.
var Chords = []Chord{
	{Keys: "ys", Help: "copy session id"},
}

// ChordsWithPrefix returns chords whose key sequence starts with prefix.
func ChordsWithPrefix(prefix string) []Chord {
	var out []Chord
	for _, c := range Chords {
		if strings.HasPrefix(c.Keys, prefix) && len(c.Keys) > len(prefix) {
			out = append(out, c)
		}
	}
	return out
}

// ChordExact returns the chord that exactly matches the given key sequence, if any.
func ChordExact(seq string) (Chord, bool) {
	for _, c := range Chords {
		if c.Keys == seq {
			return c, true
		}
	}
	return Chord{}, false
}

var Keys = KeyMap{
	Up: key.NewBinding(
		key.WithKeys("k", "up"),
		key.WithHelp("j/k", "up/down"),
	),
	Down: key.NewBinding(
		key.WithKeys("j", "down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "switch"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Defer: key.NewBinding(
		key.WithKeys("w"),
		key.WithHelp("w", "defer"),
	),
	Undefer: key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "undefer"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Hooks: key.NewBinding(
		key.WithKeys("h"),
		key.WithHelp("h", "hooks"),
	),
	Minimap: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "minimap"),
	),
	GroupMode: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("g", "group"),
	),
	Summarize: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "summarize"),
	),
	SummarizeAll: key.NewBinding(
		key.WithKeys("S"),
		key.WithHelp("S", "summarize all"),
	),
	Rename: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "rename window"),
	),
	SpatialUp: key.NewBinding(
		key.WithKeys("K", "shift+up"),
		key.WithHelp("H/J/K/L", "spatial nav"),
	),
	SpatialDown: key.NewBinding(
		key.WithKeys("J", "shift+down"),
	),
	SpatialLeft: key.NewBinding(
		key.WithKeys("H", "shift+left"),
	),
	SpatialRight: key.NewBinding(
		key.WithKeys("L", "shift+right"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
	),
	ScrollDown: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d/u", "scroll preview"),
	),
	ScrollUp: key.NewBinding(
		key.WithKeys("ctrl+u"),
	),
	MsgNext: key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j/k", "next/prev message"),
	),
	MsgPrev: key.NewBinding(
		key.WithKeys("ctrl+k"),
	),
	ListShrink: key.NewBinding(
		key.WithKeys("alt+h"),
		key.WithHelp("alt+h/l", "resize list"),
	),
	ListGrow: key.NewBinding(
		key.WithKeys("alt+l"),
	),
	Fullscreen: key.NewBinding(
		key.WithKeys("z"),
		key.WithHelp("z", "fullscreen"),
	),
	PromptRelay: key.NewBinding(
		key.WithKeys(">"),
		key.WithHelp(">", "reply"),
	),
	Kill: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "kill+close"),
	),
	CommitAndDone: key.NewBinding(
		key.WithKeys("C"),
		key.WithHelp("C", "commit+done"),
	),
	Debug: key.NewBinding(
		key.WithKeys("D"),
	),
}
