package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func (k KeyMap) ShortHelp() []key.Binding {
	bindings := []key.Binding{
		k.Up, k.NavLeft, k.Enter, k.NewSession, k.PromptRelay, k.Queue, k.Search, k.Later, k.LaterKill, k.LaterToggle,
		k.Refresh, k.GroupMode, k.GoBottom, k.Synthesize, k.SynthesizeAll, k.Macro,
		k.Rename, k.ChatOutline, k.Minimap, k.ListShrink, k.Fullscreen, k.Kill, k.Commit, k.CommitAndDone,
		k.JumpBack, k.Note,
	}
	bindings = append(bindings, chordBindings()...)
	bindings = append(bindings, k.Quit)
	return bindings
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Enter, k.Search, k.Refresh, k.Quit},
		{k.Later, k.LaterKill, k.GroupMode, k.Minimap},
		{k.Synthesize, k.SynthesizeAll, k.Rename, k.ChatOutline},
		{k.ScrollDown, k.MsgNext, k.ListShrink, k.SpatialUp},
		{k.Note},
	}
}

type KeyMap struct {
	Up         key.Binding
	Down       key.Binding
	Enter      key.Binding
	CtrlEnter  key.Binding
	Search     key.Binding
	Later       key.Binding
	LaterKill   key.Binding
	LaterToggle    key.Binding
	BacklogToggle  key.Binding
	ClaudingToggle key.Binding
	Refresh    key.Binding
	ChatOutline key.Binding
	Quit       key.Binding
	Escape     key.Binding

	Minimap       key.Binding
	MinimapMode   key.Binding
	GroupMode     key.Binding
	GoBottom      key.Binding
	Synthesize    key.Binding
	SynthesizeAll key.Binding
	Rename        key.Binding

	// Spatial navigation (minimap)
	SpatialUp    key.Binding
	SpatialDown  key.Binding
	SpatialLeft  key.Binding
	SpatialRight key.Binding

	// Preview half-page scroll (ctrl+d / ctrl+u)
	ScrollDown key.Binding
	ScrollUp   key.Binding

	// Preview single-line scroll (ctrl+e / ctrl+y)
	LineDown key.Binding
	LineUp   key.Binding

	// Preview full-page scroll (ctrl+f / ctrl+b)
	PageDown key.Binding
	PageUp   key.Binding

	// Conversation message navigation
	MsgNext key.Binding
	MsgPrev key.Binding

	// Sidebar panel resize
	ListShrink key.Binding
	ListGrow   key.Binding

	// Popup fullscreen toggle
	Fullscreen key.Binding

	// Prompt relay (send message to Claude session)
	PromptRelay key.Binding

	// Queue message for delivery when session becomes Done
	Queue key.Binding

	// Kill session + close pane
	Kill key.Binding

	// Commit only (send /commit, wait, no kill)
	Commit key.Binding

	// Commit and done (send /commit, wait, verify, kill)
	CommitAndDone key.Binding

	// Debug overlay toggle
	Debug key.Binding

	// Help overlay toggle
	Help key.Binding

	// Command palette
	Palette key.Binding

	// Tree navigation (h/l for project/session level)
	NavLeft  key.Binding
	NavRight key.Binding

	// New session (project level)
	NewSession key.Binding

	// Jump trail navigation (like Vim's jumplist)
	JumpBack    key.Binding
	JumpForward key.Binding

	// Message log overlay
	MessageLog key.Binding

	// Preferences editor
	Prefs key.Binding

	// Macro palette
	Macro key.Binding

	// Tag relay (toggle session tags)
	PromptTag key.Binding

	// Session note editor
	Note key.Binding
}

// chordBindings returns one key.Binding per unique chord starter key for the help bar.
// e.g. chords "ys", "yp" → single entry "y copy…"
func chordBindings() []key.Binding {
	type group struct {
		helps []string
	}
	groups := make(map[string]*group)
	var order []string
	for _, c := range Chords {
		starter := c.Keys[:1]
		if g, ok := groups[starter]; ok {
			g.helps = append(g.helps, c.Help)
		} else {
			groups[starter] = &group{helps: []string{c.Help}}
			order = append(order, starter)
		}
	}
	var out []key.Binding
	for _, starter := range order {
		g := groups[starter]
		label := g.helps[0]
		if len(g.helps) > 1 {
			label += "…"
		}
		out = append(out, key.NewBinding(
			key.WithKeys(starter),
			key.WithHelp(starter, label),
		))
	}
	return out
}

// Chord defines a multi-key sequence binding.
type Chord struct {
	Keys    string                          // full key sequence, e.g. "ys"
	Help    string                          // description shown in footer
	Execute func(m *Model) (Model, tea.Cmd) // action to run when chord completes
}

// Chords is the global list of chord bindings.
// Execute fields are populated in init() to avoid an initialization cycle
// (Chords → exec methods → View → ChordsWithPrefix → Chords).
var Chords = []Chord{
	{Keys: "ys", Help: "copy session id"},
	{Keys: "yc", Help: "capture view"},
	{Keys: "gd", Help: "diffs"},
	{Keys: "gh", Help: "hooks"},
	{Keys: "gt", Help: "transcript json"},
	{Keys: "gg", Help: "top"},
	{Keys: "gs", Help: "spirit animal"},
}

func init() {
	executors := map[string]func(m *Model) (Model, tea.Cmd){
		"ys": func(m *Model) (Model, tea.Cmd) { return m.execCopySessionID() },
		"yc": func(m *Model) (Model, tea.Cmd) { return m.execCaptureView() },
		"gd": func(m *Model) (Model, tea.Cmd) { return m.execToggleDiffs() },
		"gh": func(m *Model) (Model, tea.Cmd) { return m.execToggleHooks() },
		"gt": func(m *Model) (Model, tea.Cmd) { return m.execToggleRawTranscript() },
		"gg": func(m *Model) (Model, tea.Cmd) { return m.execGoTop() },
		"gs": func(m *Model) (Model, tea.Cmd) { return m.execShowSpiritAnimal() },
	}
	for i := range Chords {
		Chords[i].Execute = executors[Chords[i].Keys]
		if Chords[i].Execute == nil {
			panic(fmt.Sprintf("chord %q has no executor wired in init()", Chords[i].Keys))
		}
	}
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
	CtrlEnter: key.NewBinding(
		key.WithKeys("ctrl+enter"),
		key.WithHelp("ctrl+enter", "submit"),
	),
	Search: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search"),
	),
	Later: key.NewBinding(
		key.WithKeys("w"),
		key.WithHelp("w", "later"),
	),
	LaterKill: key.NewBinding(
		key.WithKeys("W"),
		key.WithHelp("W", "later+kill"),
	),
	LaterToggle: key.NewBinding(
		key.WithKeys("alt+w"),
		key.WithHelp("alt+w", "toggle later"),
	),
	BacklogToggle: key.NewBinding(
		key.WithKeys("alt+b"),
		key.WithHelp("alt+b", "toggle backlog"),
	),
	ClaudingToggle: key.NewBinding(
		key.WithKeys("alt+c"),
		key.WithHelp("alt+c", "toggle clauding"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	ChatOutline: key.NewBinding(
		key.WithKeys("t"),
		key.WithHelp("t", "chat outline"),
	),
	Minimap: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "minimap"),
	),
	MinimapMode: key.NewBinding(
		key.WithKeys("M"),
		key.WithHelp("M", "minimap mode"),
	),
	GroupMode: key.NewBinding(
		key.WithKeys("o"),
		key.WithHelp("o", "group"),
	),
	GoBottom: key.NewBinding(
		key.WithKeys("G"),
		key.WithHelp("G", "bottom"),
	),
	Synthesize: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "synthesize"),
	),
	SynthesizeAll: key.NewBinding(
		key.WithKeys("S"),
		key.WithHelp("S", "synthesize all"),
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
		key.WithHelp("ctrl+d/u", "½page"),
	),
	ScrollUp: key.NewBinding(
		key.WithKeys("ctrl+u"),
	),
	LineDown: key.NewBinding(
		key.WithKeys("ctrl+e"),
		key.WithHelp("ctrl+e/y", "line"),
	),
	LineUp: key.NewBinding(
		key.WithKeys("ctrl+y"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f/b", "page"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("ctrl+b"),
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
		key.WithHelp(">", "send"),
	),
	Queue: key.NewBinding(
		key.WithKeys("<"),
		key.WithHelp("<", "queue"),
	),
	Kill: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "kill+close"),
	),
	Commit: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "commit"),
	),
	CommitAndDone: key.NewBinding(
		key.WithKeys("C"),
		key.WithHelp("C", "commit+done"),
	),
	Debug: key.NewBinding(
		key.WithKeys("D"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Palette: key.NewBinding(
		key.WithKeys(";"),
		key.WithHelp(";", "commands"),
	),
	NavLeft: key.NewBinding(
		key.WithKeys("h"),
		key.WithHelp("h/l", "project/session"),
	),
	NavRight: key.NewBinding(
		key.WithKeys("l"),
	),
	NewSession: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "new session"),
	),
	JumpBack: key.NewBinding(
		key.WithKeys("ctrl+o", "shift+tab"),
		key.WithHelp("ctrl+o/i", "jump back/fwd"),
	),
	JumpForward: key.NewBinding(
		key.WithKeys("tab"), // ctrl+i and tab are the same byte (0x09) in terminals
	),
	MessageLog: key.NewBinding(
		key.WithKeys("!"),
		key.WithHelp("!", "messages"),
	),
	Prefs: key.NewBinding(
		key.WithKeys("P"),
		key.WithHelp("P", "preferences"),
	),
	Macro: key.NewBinding(
		key.WithKeys("."),
		key.WithHelp(".", "macros"),
	),
	PromptTag: key.NewBinding(
		key.WithKeys("#"),
		key.WithHelp("#", "tag"),
	),
	Note: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "note"),
	),
}
