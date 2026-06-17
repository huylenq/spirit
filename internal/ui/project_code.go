package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/claude"
)

// Project-code prefix styling. The brackets are fainter than the code itself so
// the 3-char tag reads as a quiet badge ahead of the session title. The prefix
// is always rendered as a SEPARATE styled segment — never folded into the title
// string — because titles flow through ANSI-hostile paths (shimmer, fuzzy-match
// highlighting, selection backgrounds, ItemDetailStyle wrapping) that would
// corrupt or strip embedded SGR codes.
var (
	projectCodeBracketStyle = lipgloss.NewStyle().Foreground(ColorFaint)
	projectCodeTextStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
)

// projectCodeWidth is the display width of the "[CODE] " prefix (0 when unset).
func projectCodeWidth(s claude.ClaudeSession) int {
	if s.ProjectCode == "" {
		return 0
	}
	return len(s.ProjectCode) + 3 // '[' + code + ']' + ' '
}

// renderProjectCode returns the colored "[CODE] " prefix, or "" when the
// session's project has no assigned code.
func renderProjectCode(s claude.ClaudeSession) string {
	if s.ProjectCode == "" {
		return ""
	}
	return projectCodeBracketStyle.Render("[") +
		projectCodeTextStyle.Render(s.ProjectCode) +
		projectCodeBracketStyle.Render("] ")
}

// renderProjectCodeBg is the selection-row variant: same two-tone prefix with a
// selection background painted behind it.
func renderProjectCodeBg(s claude.ClaudeSession, bg lipgloss.TerminalColor) string {
	if s.ProjectCode == "" {
		return ""
	}
	return projectCodeBracketStyle.Background(bg).Render("[") +
		projectCodeTextStyle.Background(bg).Render(s.ProjectCode) +
		projectCodeBracketStyle.Background(bg).Render("] ")
}
