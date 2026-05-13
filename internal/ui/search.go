package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type SearchModel struct {
	input  textinput.Model
	active bool
	// candidates: project names available for ghost completion + Tab cycle +
	// popover. Caller orders them (typically MRU first, then alphabetical).
	candidates []string
	// popoverIdx: highlighted index within filteredCandidates().
	popoverIdx int
	// pendingPrefix: the user's most-recently-typed project-filter stub.
	// Preserved across Tab presses so navigation continues filtering against
	// the originally-typed text rather than against the most-recently-inserted
	// candidate (which would collapse the candidate list to length 1).
	pendingPrefix string
	// tabbedSinceTyping: true once the user has pressed Tab (committing to a
	// candidate), false again once they resume typing. Switches the active
	// entry's popover styling from "preview" (bold-only) to "selected" (rose).
	tabbedSinceTyping bool
	// filtered caches the candidates that match pendingPrefix (case-insensitive).
	// Recomputed by refreshFiltered() whenever candidates or pendingPrefix
	// changes, so render-path readers are O(1).
	filtered []string
}

func NewSearchModel() SearchModel {
	ti := textinput.New()
	ti.Placeholder = "search… (`p <name>` filters by project)"
	ti.Prompt = "/ "
	ti.PromptStyle = SearchPromptStyle
	ti.CharLimit = 96
	return SearchModel{input: ti, popoverIdx: 0}
}

func (m *SearchModel) Activate() {
	m.active = true
	m.input.Focus()
	m.input.SetValue("")
	m.popoverIdx = 0
	m.pendingPrefix = ""
	m.tabbedSinceTyping = false
	m.filtered = nil
}

// ActivateWith opens the search input pre-filled with the given value and
// places the cursor at the end. Used for directive shortcuts (e.g. `p` →
// pre-filled with "p:").
func (m *SearchModel) ActivateWith(value string) {
	m.active = true
	m.input.Focus()
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.popoverIdx = 0
	m.pendingPrefix = ""
	m.tabbedSinceTyping = false
	m.filtered = nil
}

func (m *SearchModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
	m.popoverIdx = 0
	m.pendingPrefix = ""
	m.tabbedSinceTyping = false
	m.filtered = nil
}

func (m *SearchModel) Confirm() string {
	val := m.input.Value()
	m.active = false
	m.input.Blur()
	m.popoverIdx = 0
	m.pendingPrefix = ""
	m.tabbedSinceTyping = false
	m.filtered = nil
	return val
}

func (m SearchModel) Active() bool {
	return m.active
}

func (m SearchModel) Value() string {
	return m.input.Value()
}

func (m *SearchModel) UpdateInput(msg interface{}) {
	// Type assert to tea.Msg would happen in the app layer
	// This is called from the app's Update function
}

func (m SearchModel) View() string {
	if !m.active {
		return ""
	}
	// Empty value: defer to bubbles for placeholder + cursor handling.
	if m.input.Value() == "" {
		return lipgloss.NewStyle().Padding(0, 1).Render(m.input.View())
	}
	body := m.input.PromptStyle.Render(m.input.Prompt) + renderHighlightedSearchValue(m.input)
	if ghost := m.ghostCompletion(); ghost != "" {
		body += SearchGhostStyle.Render(ghost)
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(body)
}

func (m *SearchModel) TextInput() *textinput.Model {
	return &m.input
}

// SetCandidates updates the project name list used for ghost completion,
// popover, and Tab cycling. The caller controls order (e.g. MRU first).
// Clamps popoverIdx if the new candidate set shrank below the previous index.
func (m *SearchModel) SetCandidates(names []string) {
	m.candidates = names
	m.refreshFiltered()
	if len(m.filtered) == 0 {
		m.popoverIdx = 0
		return
	}
	if m.popoverIdx >= len(m.filtered) {
		m.popoverIdx = len(m.filtered) - 1
	}
	if m.popoverIdx < 0 {
		m.popoverIdx = 0
	}
}

// projectPrefix returns the project-filter value already typed (the chars
// after `p:` or after the leading `p ` shorthand), along with whether the
// cursor is positioned at the end of that value — only then is ghost
// completion / Tab / popover meaningful.
func (m SearchModel) projectPrefix() (prefix string, ok bool) {
	val := m.input.Value()
	if m.input.Position() != len([]rune(val)) {
		return "", false
	}
	lastSpace := strings.LastIndex(val, " ")
	tok := val[lastSpace+1:]
	switch {
	case strings.HasPrefix(tok, "p:"):
		return tok[2:], true
	case lastSpace == -1 && val == "p":
		// Bare leading `p` — about to become the shorthand. No prefix yet.
		return "", true
	case lastSpace >= 0:
		if lastSpace == 1 && strings.HasPrefix(val, "p ") {
			return tok, true
		}
	}
	return "", false
}

// NoteTyped captures the user's current project-filter token as the anchor
// for popover filtering. Call after the textinput consumes a keystroke so
// the filter narrows with typing — but stays stable across Tab presses.
// Also clears tabbedSinceTyping so the active popover entry reverts from
// "selected" (red) back to "preview" (bold-only).
func (m *SearchModel) NoteTyped() {
	m.tabbedSinceTyping = false
	if _, ok := m.projectPrefix(); !ok {
		m.pendingPrefix = ""
	} else {
		m.pendingPrefix = m.currentToken()
	}
	m.refreshFiltered()
}

// refreshFiltered rebuilds m.filtered from candidates + pendingPrefix.
// Empty when the directive isn't active. Called whenever either input changes.
func (m *SearchModel) refreshFiltered() {
	if _, ok := m.projectPrefix(); !ok {
		m.filtered = nil
		return
	}
	if m.pendingPrefix == "" {
		m.filtered = m.candidates
		return
	}
	lower := strings.ToLower(m.pendingPrefix)
	out := make([]string, 0, len(m.candidates))
	for _, n := range m.candidates {
		if strings.HasPrefix(strings.ToLower(n), lower) {
			out = append(out, n)
		}
	}
	m.filtered = out
}

// selectedCandidate returns the currently-highlighted candidate (the entry
// that Tab would insert), or "" if no candidates apply.
func (m SearchModel) selectedCandidate() string {
	if len(m.filtered) == 0 {
		return ""
	}
	idx := m.popoverIdx
	if idx < 0 || idx >= len(m.filtered) {
		idx = 0
	}
	return m.filtered[idx]
}

// ghostCompletion returns the suffix that completes the typed prefix into
// the currently-highlighted candidate. Empty when there's no completion to
// show (no candidates, or highlight already matches the typed value).
func (m SearchModel) ghostCompletion() string {
	prefix, ok := m.projectPrefix()
	if !ok {
		return ""
	}
	sel := m.selectedCandidate()
	if sel == "" {
		return ""
	}
	if strings.EqualFold(sel, prefix) {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(sel), strings.ToLower(prefix)) {
		return ""
	}
	return sel[len(prefix):]
}

// PopoverAdvance moves the popover highlight by delta and replaces the
// project-filter token in the input with the new highlighted candidate.
// When the filter has narrowed to a single candidate (i.e. Tab is actually
// "completing" rather than cycling), an additional trailing space is
// appended — fish/zsh-style — to signal that the directive is closed.
func (m *SearchModel) PopoverAdvance(delta int) bool {
	filtered := m.filtered
	if len(filtered) == 0 {
		return false
	}
	current := m.currentToken()
	matchedIdx := -1
	for i, n := range filtered {
		if strings.EqualFold(n, current) {
			matchedIdx = i
			break
		}
	}
	if matchedIdx >= 0 {
		m.popoverIdx = (matchedIdx + delta + len(filtered)) % len(filtered)
	} else {
		if delta >= 0 {
			m.popoverIdx = 0
		} else {
			m.popoverIdx = len(filtered) - 1
		}
	}
	m.replaceToken(filtered[m.popoverIdx])
	m.tabbedSinceTyping = true
	if len(filtered) == 1 {
		m.input.SetValue(m.input.Value() + " ")
		m.input.CursorEnd()
		m.refreshFiltered() // trailing space exits the directive — filtered should clear
	}
	return true
}

// currentToken extracts the project-filter value-portion currently in the
// input (the text after `p:` or the post-`p ` shorthand token). Returns ""
// when the directive isn't active.
func (m SearchModel) currentToken() string {
	val := m.input.Value()
	lastSpace := strings.LastIndex(val, " ")
	tokStart := lastSpace + 1
	if strings.HasPrefix(val[tokStart:], "p:") {
		tokStart += 2
	}
	return val[tokStart:]
}

// replaceToken swaps the project-filter value-portion in the input with
// `name`, preserving the `p:` (or `p `) directive prefix, and moves the
// cursor to the end.
func (m *SearchModel) replaceToken(name string) {
	val := m.input.Value()
	lastSpace := strings.LastIndex(val, " ")
	tokStart := lastSpace + 1
	if strings.HasPrefix(val[tokStart:], "p:") {
		tokStart += 2
	}
	m.input.SetValue(val[:tokStart] + name)
	m.input.CursorEnd()
}

// MaxQueryWidth returns the widest the search field could ever render
// while the popover is active — `/  p:` plus the longest candidate revealed
// as ghost text, plus padding. renderSearchBar uses this to pad the field
// to a constant width so the popover's start column doesn't slide as the
// user types. Returns 0 when the popover isn't active.
func (m SearchModel) MaxQueryWidth() int {
	if _, ok := m.projectPrefix(); !ok {
		return 0
	}
	if len(m.candidates) == 0 {
		return 0
	}
	maxName := 0
	for _, n := range m.candidates {
		if w := lipgloss.Width(n); w > maxName {
			maxName = w
		}
	}
	promptW := lipgloss.Width(m.input.PromptStyle.Render(m.input.Prompt))
	// promptW + 2 ("p:" directive) + maxName + 1 (cursor block always emitted
	// at end-of-value by renderHighlightedSearchValue) + 2 (Padding(0,1) wrapper
	// in View()).
	return promptW + 2 + maxName + 1 + 2
}

// PopoverView renders the horizontal completion strip. Each candidate's
// matched-prefix characters are tinted with the directive color; the rest
// is muted (or full rose+bold on the active candidate). No background fill,
// no per-cell padding — relies on color alone so it sits cleanly on the
// existing label row. Overflow on either side collapses to `…`.
func (m SearchModel) PopoverView(maxWidth int) (view string, width int, ok bool) {
	filtered := m.filtered
	if len(filtered) == 0 {
		return "", 0, false
	}
	if len(filtered) == 1 && strings.EqualFold(filtered[0], m.currentToken()) {
		return "", 0, false
	}
	if maxWidth < 8 {
		maxWidth = 8
	}
	idx := m.popoverIdx
	if idx < 0 || idx >= len(filtered) {
		idx = 0
	}

	prefixLen := len([]rune(m.pendingPrefix))

	// Matched chars are tinted rose for every entry; the rest is muted. The
	// active entry is "preview" (bold-only) until the user actually presses
	// Tab — after that it goes full rose + bold + underline to mark a real
	// selection. Typing any character flips back to preview.
	var matchedActive, restActive lipgloss.Style
	if m.tabbedSinceTyping {
		matchedActive = lipgloss.NewStyle().Foreground(ColorPulse).Bold(true).Underline(true)
		restActive = lipgloss.NewStyle().Foreground(ColorPulse).Bold(true)
	} else {
		matchedActive = lipgloss.NewStyle().Foreground(ColorPulse).Bold(true)
		restActive = lipgloss.NewStyle().Foreground(ColorMuted).Bold(true)
	}
	matchedInactive := lipgloss.NewStyle().Foreground(ColorPulse)
	restInactive := lipgloss.NewStyle().Foreground(ColorMuted)

	const sep = "  "
	sepW := lipgloss.Width(sep)

	cells := make([]string, len(filtered))
	cellW := make([]int, len(filtered))
	for i, name := range filtered {
		runes := []rune(name)
		split := prefixLen
		if split > len(runes) {
			split = len(runes)
		}
		matched := string(runes[:split])
		rest := string(runes[split:])
		var cell string
		if i == idx {
			cell = matchedActive.Render(matched) + restActive.Render(rest)
		} else {
			cell = matchedInactive.Render(matched) + restInactive.Render(rest)
		}
		cells[i] = cell
		cellW[i] = lipgloss.Width(name)
	}

	leftHint := SearchPopoverDimStyle.Render("…")
	rightHint := SearchPopoverDimStyle.Render("…")
	hintW := lipgloss.Width(leftHint)

	start, end := idx, idx+1
	used := cellW[idx]
	for {
		grew := false
		if end < len(filtered) {
			extra := cellW[end] + sepW
			rightCost := 0
			if end+1 < len(filtered) {
				rightCost = hintW + sepW
			}
			leftCost := 0
			if start > 0 {
				leftCost = hintW + sepW
			}
			if used+extra+leftCost+rightCost <= maxWidth {
				used += extra
				end++
				grew = true
			}
		}
		if start > 0 {
			extra := cellW[start-1] + sepW
			leftCost := 0
			if start-1 > 0 {
				leftCost = hintW + sepW
			}
			rightCost := 0
			if end < len(filtered) {
				rightCost = hintW + sepW
			}
			if used+extra+leftCost+rightCost <= maxWidth {
				used += extra
				start--
				grew = true
			}
		}
		if !grew {
			break
		}
	}

	parts := make([]string, 0, len(filtered)+2)
	if start > 0 {
		parts = append(parts, leftHint, sep)
	}
	for i := start; i < end; i++ {
		if i > start {
			parts = append(parts, sep)
		}
		parts = append(parts, cells[i])
	}
	if end < len(filtered) {
		parts = append(parts, sep, rightHint)
	}
	body := strings.Join(parts, "")
	return body, lipgloss.Width(body), true
}

// renderHighlightedSearchValue renders the input's value with directive
// syntax highlighting: `p:` (or leading `p ` shorthand) is styled as a
// directive key, the value after the colon as a directive value, and a
// reverse-styled cursor is overlaid at the textinput's cursor position.
func renderHighlightedSearchValue(in textinput.Model) string {
	runes := []rune(in.Value())
	pos := in.Position()
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}

	const (
		kindText = iota
		kindKey
		kindVal
	)
	styleFor := func(k int) lipgloss.Style {
		switch k {
		case kindKey:
			return SearchDirectiveKeyStyle
		case kindVal:
			return SearchDirectiveValStyle
		default:
			return in.TextStyle
		}
	}
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	// Classify every rune, walking token-by-token. Recognizes both `p:foo`
	// and the leading `p foo` shorthand (mirrors parseSearchQuery's rewrite of
	// "p X" → "p:X" — only the token immediately after `p ` is the value).
	kinds := make([]int, len(runes))
	i := 0
	if len(runes) >= 2 && runes[0] == 'p' && runes[1] == ' ' {
		kinds[0] = kindKey
		kinds[1] = kindText
		i = 2
		for i < len(runes) && runes[i] != ' ' {
			kinds[i] = kindVal
			i++
		}
	}
	for i < len(runes) {
		if runes[i] == ' ' {
			kinds[i] = kindText
			i++
			continue
		}
		j := i
		for j < len(runes) && runes[j] != ' ' {
			j++
		}
		token := string(runes[i:j])
		if strings.HasPrefix(token, "p:") {
			kinds[i] = kindKey   // 'p'
			kinds[i+1] = kindKey // ':'
			for k := i + 2; k < j; k++ {
				kinds[k] = kindVal
			}
		} else {
			for k := i; k < j; k++ {
				kinds[k] = kindText
			}
		}
		i = j
	}

	// Emit runes coalesced by adjacent style runs; insert the cursor at `pos`.
	// `kindVal` runs are additionally wrapped with raw CSI 4:4 m (dashed
	// underline) — an extended SGR not exposed by lipgloss. Falls back to plain
	// underline on terminals/multiplexers that don't pass it through.
	var b strings.Builder
	flush := func(start, end int) {
		if start >= end {
			return
		}
		rendered := styleFor(kinds[start]).Inline(true).Render(string(runes[start:end]))
		if kinds[start] == kindVal {
			rendered = "\x1b[4:4m" + rendered + "\x1b[4:0m"
		}
		b.WriteString(rendered)
	}
	runStart := 0
	for k := 0; k <= len(runes); k++ {
		if k == pos {
			flush(runStart, k)
			if k < len(runes) {
				b.WriteString(cursorStyle.Render(string(runes[k])))
				runStart = k + 1
			} else {
				b.WriteString(cursorStyle.Render(" "))
				runStart = k
			}
			continue
		}
		if k == len(runes) {
			flush(runStart, k)
			break
		}
		if k > runStart && kinds[k] != kinds[k-1] {
			flush(runStart, k)
			runStart = k
		}
	}
	return b.String()
}
