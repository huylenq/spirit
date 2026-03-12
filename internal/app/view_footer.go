package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

// hint formats a single key hint for the footer bar.
func hint(k, desc string) string {
	return ui.FooterKeyStyle.Render(k) + " " + desc
}

// renderNormalFooterHints builds a context-sensitive footer based on the selected session.
func (m Model) renderNormalFooterHints() string {
	var parts []string

	// Backlog-specific footer
	if m.sidebar.IsBacklogSelected() {
		parts = append(parts, hint("j/k", "nav"))
		parts = append(parts, hint("enter", "submit"), hint("b", "edit"), hint("e", "$EDITOR"), hint("d", "delete"))
		parts = append(parts, hint("?", "help"), hint("q", "quit"))
		return strings.Join(parts, "  ")
	}

	// Project-level footer
	if m.sidebar.SelectionLevel() == ui.LevelProject {
		if _, ok := m.sidebar.SelectedProject(); ok {
			parts = append(parts, hint("j/k", "nav"), hint("b", "new backlog"), hint("l", "enter"))
			parts = append(parts, hint("?", "help"), hint("q", "quit"))
			return strings.Join(parts, "  ")
		}
	}

	s, hasSelection := m.sidebar.SelectedItem()

	// Always show nav
	parts = append(parts, hint("j/k", "nav"))

	if !hasSelection {
		parts = append(parts, hint("/", "search"), hint("g", "group"), hint("m", "minimap"), hint("?", "help"), hint("q", "quit"))
		return strings.Join(parts, "  ")
	}

	parts = append(parts, hint("enter", "switch"), hint(">", "send"), hint("<", "queue"))

	if s.LaterBookmarkID != "" {
		parts = append(parts, hint("w", "unlater"))
	} else {
		switch s.Status {
		case claude.StatusUserTurn:
			if !s.CommitDonePending {
				parts = append(parts, hint("c", "commit"), hint("C", "commit+done"))
			}
			parts = append(parts, hint("w", "later"), hint("W", "later+kill"))
		case claude.StatusAgentTurn:
			parts = append(parts, hint("w", "later"), hint("W", "later+kill"))
		}
	}

	parts = append(parts, hint("d", "kill"), hint("b", "new backlog"))

	if m.showMinimap {
		parts = append(parts, hint("H/J/K/L", "spatial"))
	}

	parts = append(parts, hint("?", "help"), hint("q", "quit"))
	return strings.Join(parts, "  ")
}

// renderHelpOverlay returns a styled help overlay showing all keybindings.
func (m Model) renderHelpOverlay() string {
	title := ui.HelpTitleStyle.Render("Keybindings")
	nav := ui.HelpGroupStyle.Render("Navigation")
	actions := ui.HelpGroupStyle.Render("Actions")
	toggles := ui.HelpGroupStyle.Render("Toggles & Copy")

	col1 := strings.Join([]string{
		nav,
		hint("j/k", "up/down"),
		hint("enter", "switch to pane"),
		hint("/", "search"),
		hint("ctrl+d/u", "scroll preview"),
		hint("ctrl+j/k", "next/prev message"),
		hint("alt+h/l", "resize list"),
	}, "\n")

	col2 := strings.Join([]string{
		actions,
		hint(">", "send to session"),
		hint("<", "queue message"),
		hint("w", "later"),
		hint("W", "later + kill"),
		hint("d", "kill + close pane"),
		hint("s", "synthesize"),
		hint("S", "synthesize all"),
		hint("R", "rename window"),
		hint("c", "commit"),
		hint("C", "commit + done"),
		hint("r", "refresh preview"),
	}, "\n")

	chordHints := make([]string, 0, len(Chords))
	for _, c := range Chords {
		// Format "ys" as "y s" for readability
		keys := strings.Join(strings.Split(c.Keys, ""), " ")
		chordHints = append(chordHints, hint(keys, c.Help))
	}

	col3Parts := []string{
		toggles,
		hint("m", "minimap"),
		hint("M", "minimap settings"),
		hint("P", "preferences"),
		hint("g", "group by project"),
		hint("t", "toggle transcript"),
		hint("z", "fullscreen toggle"),
		hint("!", "message log"),
	}
	col3Parts = append(col3Parts, chordHints...)
	col3Parts = append(col3Parts, "", ui.FooterDimStyle.Render("press ? or esc to close"))
	col3 := strings.Join(col3Parts, "\n")

	columns := lipgloss.JoinHorizontal(lipgloss.Top, col1, "    ", col2, "    ", col3)
	body := title + "\n\n" + columns
	return ui.HelpOverlayStyle.Render(body)
}

// renderChordHints shows the pending chord prefix and available continuations.
func (m Model) renderChordHints() string {
	prefix := ui.FooterKeyStyle.Render(m.pendingChord + "-")
	continuations := ChordsWithPrefix(m.pendingChord)
	var parts []string
	for _, c := range continuations {
		// Show the remaining keys after the prefix
		remaining := c.Keys[len(m.pendingChord):]
		parts = append(parts, ui.FooterKeyStyle.Render(remaining)+" "+c.Help)
	}
	return prefix + "  " + strings.Join(parts, "  ")
}
