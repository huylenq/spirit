package app

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/ui"
)

// hint formats a single key hint for the footer bar.
func hint(k, desc string) string {
	return ui.FooterKeyStyle.Render(k) + " " + desc
}

// bhint renders a hint sourced from a keymap binding, so footer text always
// matches the active key + label.
func bhint(b key.Binding) string {
	h := b.Help()
	return hint(h.Key, h.Desc)
}

// renderNormalFooterHints builds a context-sensitive footer based on the selected session.
func (m Model) renderNormalFooterHints() string {
	var parts []string

	// Backlog-specific footer
	if m.sidebar.IsBacklogSelected() {
		parts = append(parts, bhint(Keys.Up))
		parts = append(parts, hint("enter", "edit"), hint("a", "submit"), hint("b", "new"), hint("e", "$EDITOR"), hint("d", "delete"))
		parts = append(parts, bhint(Keys.Help), bhint(Keys.Quit))
		return strings.Join(parts, "  ")
	}

	// Project-level footer
	if m.sidebar.SelectionLevel() == ui.LevelProject {
		if _, ok := m.sidebar.SelectedProject(); ok {
			parts = append(parts, bhint(Keys.Up), hint("b", "new backlog"), hint("l", "enter"))
			parts = append(parts, bhint(Keys.Help), bhint(Keys.Quit))
			return strings.Join(parts, "  ")
		}
	}

	s, hasSelection := m.sidebar.SelectedItem()

	// Always show nav
	parts = append(parts, bhint(Keys.Up))

	if !hasSelection {
		parts = append(parts, bhint(Keys.Search), bhint(Keys.GroupMode), bhint(Keys.Minimap), bhint(Keys.Help), bhint(Keys.Quit))
		return strings.Join(parts, "  ")
	}

	parts = append(parts, bhint(Keys.Enter), bhint(Keys.PromptRelay), bhint(Keys.Queue))

	if s.LaterID != "" {
		parts = append(parts, hint("w", "unlater"))
	} else {
		switch s.Status {
		case claude.StatusUserTurn:
			if !s.CommitDonePending {
				parts = append(parts, bhint(Keys.Commit), bhint(Keys.CommitAndDone), bhint(Keys.CommitSimplifyAndDone))
			}
			parts = append(parts, bhint(Keys.Later), bhint(Keys.LaterKill))
		case claude.StatusAgentTurn:
			parts = append(parts, bhint(Keys.Later), bhint(Keys.LaterKill))
		}
	}

	parts = append(parts, bhint(Keys.Kill), hint("b", "new backlog"))

	if m.showMinimap {
		parts = append(parts, bhint(Keys.SpatialUp))
	}

	parts = append(parts, bhint(Keys.Help), bhint(Keys.Quit))
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
		bhint(Keys.Up),
		bhint(Keys.Enter),
		bhint(Keys.Search),
		bhint(Keys.ScrollDown),
		bhint(Keys.MsgNext),
		bhint(Keys.ListShrink),
	}, "\n")

	col2 := strings.Join([]string{
		actions,
		bhint(Keys.PromptRelay),
		bhint(Keys.Queue),
		bhint(Keys.Later),
		bhint(Keys.LaterKill),
		bhint(Keys.Kill),
		bhint(Keys.Synthesize),
		bhint(Keys.SynthesizeAll),
		bhint(Keys.Rename),
		bhint(Keys.Commit),
		bhint(Keys.CommitAndDone),
		bhint(Keys.ApplyTitle),
	}, "\n")

	chordHints := make([]string, 0, len(Chords))
	for _, c := range Chords {
		// Format "ys" as "y s" for readability
		keys := strings.Join(strings.Split(c.Keys, ""), " ")
		chordHints = append(chordHints, hint(keys, c.Help))
	}

	col3Parts := []string{
		toggles,
		bhint(Keys.Minimap),
		bhint(Keys.MinimapMode),
		bhint(Keys.Prefs),
		bhint(Keys.GroupMode),
		bhint(Keys.ChatOutline),
		bhint(Keys.Fullscreen),
		bhint(Keys.MessageLog),
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
