package ui

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	// "github.com/charmbracelet/lipgloss"
)

// SyntaxTheme is the chroma style name used for diff syntax highlighting.
// The diff overlay always renders on a dark background, so dark styles work best.
var SyntaxTheme = "monokai"

var (
	resolvedThemeName   string
	resolvedChromaStyle *chroma.Style
)

func getChromaStyle() *chroma.Style {
	if resolvedChromaStyle == nil || resolvedThemeName != SyntaxTheme {
		// NOTE: We intentionally avoid lipgloss.HasDarkBackground() here.
		// Suspicion: inside bubbletea's alternate screen mode, the OSC terminal
		// query it sends can return false (light) even on a dark terminal, which
		// would cause us to pick a light chroma style (e.g. "friendly") that
		// looks washed-out against the dark diff backgrounds. The diff overlay
		// is always rendered on a dark box regardless of terminal theme, so we
		// just hardcode a dark style.
		//
		// Original adaptive logic, kept for reference:
		// name := "tokyonight-night"
		// if !lipgloss.HasDarkBackground() {
		// 	name = "friendly"
		// }
		resolvedChromaStyle = styles.Get(SyntaxTheme)
		if resolvedChromaStyle == nil {
			resolvedChromaStyle = styles.Fallback
		}
		resolvedThemeName = SyntaxTheme
	}
	return resolvedChromaStyle
}

// highlightLines syntax-highlights content for the given filename and returns
// a slice of ANSI-colored lines, one per newline in content. Only foreground
// attributes are applied — no background ANSI codes — so that diff background
// colors (applied by lipgloss callers) remain consistent across the full line.
func highlightLines(content, filename string) []string {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	it, err := lexer.Tokenise(nil, content)
	if err != nil {
		return strings.Split(content, "\n")
	}

	style := getChromaStyle()
	var lines []string
	var cur strings.Builder

	for _, tok := range it.Tokens() {
		entry := style.Get(tok.Type)
		hasFg := entry.Colour.IsSet()
		hasBold := entry.Bold == chroma.Yes
		hasItalic := entry.Italic == chroma.Yes

		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				lines = append(lines, cur.String())
				cur.Reset()
			}
			if part == "" {
				continue
			}
			if hasFg || hasBold || hasItalic {
				if hasFg {
					fmt.Fprintf(&cur, "\x1b[38;2;%d;%d;%dm",
						entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue())
				}
				if hasBold {
					cur.WriteString("\x1b[1m")
				}
				if hasItalic {
					cur.WriteString("\x1b[3m")
				}
				cur.WriteString(part)
				// Reset fg, bold, italic only — never background, so the
				// caller's diff bg color stays intact.
				cur.WriteString("\x1b[39;22;23m")
			} else {
				cur.WriteString(part)
			}
		}
	}
	lines = append(lines, cur.String())
	return lines
}
