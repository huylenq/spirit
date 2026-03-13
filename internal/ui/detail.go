package ui

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// Chat outline display modes (must match constants in internal/app/model.go).
const (
	chatOutlineOverlay = "overlay"
	chatOutlineDocked  = "docked"
	chatOutlineHidden  = "hidden"
)

// diffFileStat is a pre-sorted, per-file diff entry cached on SetDiffStats.
type diffFileStat struct {
	name      string
	added     int
	removed   int
	footprint int
}

type DetailModel struct {
	viewport               viewport.Model
	session                *claude.ClaudeSession
	content                string
	userMessages           []string
	msgOffsets             []int // line index in content for each userMessage; -1 if not found
	msgCursor              int   // which user message we last navigated to
	pendingMsgReset        bool  // set on session switch; reset msgCursor when messages arrive
	diffStats              map[string]claude.FileDiffStat
	diffFiles              []diffFileStat // cached sorted file entries
	summary                *claude.SessionSummary
	note                   string          // freeform note for this session (empty = no panel)
	noteEditor             NoteEditorModel // inline textarea for editing the note
	noteEditing            bool            // true while the note is being edited
	relayView              string          // when set, rendered inline after the ❯ prompt line
	hookEvents             []claude.HookEvent
	showHooks              bool
	chatOutlineMode            string // chatOutlineOverlay, chatOutlineDocked, chatOutlineHidden
	hookCursor             int
	hookExpanded           map[int]bool   // per-entry expansion (keyed by filtered index)
	hookExpandedJSON       map[int]string // lazy pretty-print cache
	hookScroll             int
	hookFilter             int                // 0=all, 1=handled only, 2=unhandled only
	hookFiltered           []claude.HookEvent // cached filtered+reversed slice
	transcriptEntries      []claude.TranscriptEntry
	transcriptCursor       int            // selected entry index
	transcriptScroll       int            // first visible entry index
	transcriptExpanded     map[int]bool   // which entries are expanded
	transcriptExpandedJSON map[int]string // lazy pretty-print cache
	transcriptMaxTypeW     int            // cached max width of Type column
	transcriptMaxCTypeW    int            // cached max width of ContentType column
	showRawTranscript      bool
	showDiffs              bool
	diffHunks              []claude.FileDiffHunk
	diffHunkFiles          []diffHunkFile
	diffScroll             int
	diffSimThreshold       float64 // similarity threshold for ~ vs separate -/+ lines
	width                  int
	height                 int
	ready                  bool
}

func NewDetailModel() DetailModel {
	return DetailModel{
		diffSimThreshold: defaultDiffSimThreshold,
		noteEditor:       NewNoteEditorModel(),
	}
}

func (m *DetailModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	headerHeight := 3 // title + branch/diff + blank line
	footerHeight := 2 // metadata line + blank
	borderHeight := 2 // top + bottom border of content box
	contentHeight := h - headerHeight - footerHeight - borderHeight
	if contentHeight < 1 {
		contentHeight = 1
	}
	vpWidth := m.effectiveVPWidth(w)
	if !m.ready {
		m.viewport = viewport.New(vpWidth, contentHeight)
		m.ready = true
		if m.content != "" {
			m.viewport.SetContent(wrapLines(trimTrailingBlanks(m.content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
			m.viewport.GotoBottom() // content arrived before size was known
		}
	} else {
		m.viewport.Width = vpWidth
		m.viewport.Height = contentHeight
		if m.content != "" {
			m.viewport.SetContent(wrapLines(trimTrailingBlanks(m.content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
		}
	}
}

// calcPanelWidth returns the sidebar/overlay panel width for a given content width,
// clamped to [20, 50].
func calcPanelWidth(contentWidth int) int {
	w := contentWidth * 40 / 100
	if w < 20 {
		w = 20
	}
	if w > 50 {
		w = 50
	}
	return w
}

// hasSidebarContent reports whether any panel content (transcript, summary, note)
// exists that would be shown in the sidebar/overlay.
func (m *DetailModel) hasSidebarContent() bool {
	return len(m.userMessages) > 0 || m.summary != nil || m.note != "" || m.noteEditing
}

// effectiveVPWidth returns the viewport width accounting for transcript mode.
// In docked mode, the viewport is narrower to make room for the side panel.
func (m *DetailModel) effectiveVPWidth(w int) int {
	contentWidth := w - 4
	vpWidth := w - 6 // content box border (2) + outer padding (4)
	if vpWidth < 1 {
		vpWidth = 1
	}
	if m.chatOutlineMode == chatOutlineDocked && m.hasSidebarContent() {
		vpWidth = contentWidth - calcPanelWidth(contentWidth) - 3 // 1 gap + 2 for content border
		if vpWidth < 1 {
			vpWidth = 1
		}
	}
	return vpWidth
}

// effectiveDividerWidth returns the max width for reconstructing horizontal rule
// labels. In overlay mode the label must sit left of the overlay panel; in other
// modes it matches the viewport width.
func (m *DetailModel) effectiveDividerWidth(vpWidth int) int {
	if m.chatOutlineMode == chatOutlineOverlay && m.hasSidebarContent() {
		w := vpWidth - calcPanelWidth(m.width-4) - 1
		if w < 1 {
			return 1
		}
		return w
	}
	return vpWidth
}

// ViewportHeight returns the current height of the detail viewport in lines.
func (m *DetailModel) ViewportHeight() int {
	return m.viewport.Height
}

// ClearSession resets the preview to the empty "Select a session" state.
func (m *DetailModel) ClearSession() {
	m.session = nil
	m.content = ""
	m.userMessages = nil
	m.diffFiles = nil
	m.summary = nil
}

// SetNonClaudePane shows a raw terminal capture for a non-Claude pane.
func (m *DetailModel) SetNonClaudePane(paneID string, paneTitle string, content string) {
	title := paneTitle
	if title == "" {
		title = paneID
	}
	isNew := m.session == nil || m.session.PaneID != paneID
	m.session = &claude.ClaudeSession{PaneID: paneID, Project: title}
	if isNew {
		m.userMessages = nil
		m.diffFiles = nil
		m.diffStats = nil
		m.summary = nil
	}
	if m.content == content {
		return // skip re-render when content unchanged (daemon re-captures every ~1s)
	}
	m.content = content
	if m.ready {
		m.viewport.SetContent(wrapLines(trimTrailingBlanks(content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
		if isNew {
			m.viewport.GotoBottom()
		}
	}
}

func (m *DetailModel) SetSession(s *claude.ClaudeSession, content string) {
	isNewSession := m.session == nil || m.session.PaneID != s.PaneID
	if isNewSession {
		m.hookScroll = 0
		m.pendingMsgReset = true
	}
	m.session = s
	m.content = content
	m.recomputeOffsets()
	if m.ready {
		m.viewport.SetContent(wrapLines(trimTrailingBlanks(content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
		if isNewSession {
			m.viewport.GotoBottom()
		}
	}
}

func (m *DetailModel) SetDiffStats(stats map[string]claude.FileDiffStat) {
	m.diffStats = stats
	// Pre-sort file entries by footprint so View() doesn't re-sort every frame
	files := make([]diffFileStat, 0, len(stats))
	for f, ds := range stats {
		parts := strings.Split(f, "/")
		files = append(files, diffFileStat{
			name: parts[len(parts)-1], added: ds.Added, removed: ds.Removed,
			footprint: ds.Added + ds.Removed,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].footprint != files[j].footprint {
			return files[i].footprint > files[j].footprint
		}
		return files[i].name < files[j].name
	})
	m.diffFiles = files
}

func (m *DetailModel) SetSummary(s *claude.SessionSummary) {
	m.summary = s
}

func (m *DetailModel) SetNote(note string) {
	m.note = note
}

// StartNoteEdit activates inline editing of the note panel.
func (m *DetailModel) StartNoteEdit() {
	m.noteEditing = true
	m.noteEditor.Activate(m.note)
}

// StopNoteEdit deactivates the inline editor without saving.
func (m *DetailModel) StopNoteEdit() {
	m.noteEditing = false
	m.noteEditor.Deactivate()
}

// NoteEditing reports whether the note panel is in edit mode.
func (m *DetailModel) NoteEditing() bool { return m.noteEditing }

// NoteValue returns the current textarea content (only meaningful while editing).
func (m *DetailModel) NoteValue() string { return m.noteEditor.Value() }

// UpdateNoteEditor forwards a message to the textarea and returns any cmd.
func (m *DetailModel) UpdateNoteEditor(msg tea.Msg) tea.Cmd {
	return m.noteEditor.Update(msg)
}

func (m *DetailModel) SetChatOutlineMode(mode string) {
	m.chatOutlineMode = mode
	if m.ready {
		vpWidth := m.effectiveVPWidth(m.width)
		m.viewport.Width = vpWidth
		if m.content != "" {
			m.viewport.SetContent(wrapLines(trimTrailingBlanks(m.content), vpWidth, m.effectiveDividerWidth(vpWidth)))
		}
	}
}

func (m *DetailModel) SetRelayView(v string) {
	m.relayView = v
}

func (m *DetailModel) ToggleExpand() {
	if m.showRawTranscript {
		if m.transcriptExpanded == nil {
			m.transcriptExpanded = make(map[int]bool)
		}
		m.transcriptExpanded[m.transcriptCursor] = !m.transcriptExpanded[m.transcriptCursor]
		return
	}
	if !m.showHooks {
		return
	}
	if m.hookExpanded == nil {
		m.hookExpanded = make(map[int]bool)
	}
	m.hookExpanded[m.hookCursor] = !m.hookExpanded[m.hookCursor]
}

func (m *DetailModel) SetTranscriptEntries(entries []claude.TranscriptEntry) {
	m.transcriptEntries = entries
	m.transcriptExpandedJSON = make(map[int]string)
	// Precompute column widths (minimum = header label width)
	m.transcriptMaxTypeW = len("TYPE")
	m.transcriptMaxCTypeW = len("CONTENT")
	for _, e := range entries {
		if len(e.Type) > m.transcriptMaxTypeW {
			m.transcriptMaxTypeW = len(e.Type)
		}
		if len(e.ContentType) > m.transcriptMaxCTypeW {
			m.transcriptMaxCTypeW = len(e.ContentType)
		}
	}
	// Don't reset cursor/scroll/expanded — preserve navigation state on refresh
}

func (m *DetailModel) SetShowRawTranscript(show bool) {
	m.showRawTranscript = show
	if !show {
		m.transcriptEntries = nil
		m.transcriptCursor = 0
		m.transcriptScroll = 0
		m.transcriptExpanded = nil
		m.transcriptExpandedJSON = nil
	} else {
		m.transcriptExpanded = make(map[int]bool)
		m.transcriptExpandedJSON = make(map[int]string)
	}
}

// runeWidth returns the display width of a rune without allocating a string.
// CJK wide characters are 2 cells; everything else is 1.
func runeWidth(r rune) int {
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hiragana, r) {
		return 2
	}
	return 1
}

// wordWrapFirst splits s into a first line that fits within width (breaking at
// word boundaries) and the remaining text. If no word boundary is found within
// width, it falls back to a hard truncation.
func wordWrapFirst(s string, width int) (string, string) {
	if ansi.StringWidth(s) <= width {
		return s, ""
	}
	// Walk runes, track last space position
	w := 0
	lastSpace := -1
	lastSpaceByte := 0
	byteOff := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > width {
			break
		}
		if r == ' ' {
			lastSpace = w
			lastSpaceByte = byteOff
		}
		w += rw
		byteOff += utf8.RuneLen(r)
	}
	if lastSpace > 0 {
		return s[:lastSpaceByte], strings.TrimSpace(s[lastSpaceByte:])
	}
	// No space found — hard break
	return ansi.Truncate(s, width, ""), strings.TrimSpace(s[byteOff:])
}
