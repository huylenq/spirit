package app

import (
	"strings"

	"github.com/huylenq/spirit/internal/ui"
)

// renderMacroPalette renders the macro palette overlay.
func (m Model) renderMacroPalette() string {
	var lines []string
	for _, macro := range m.macros {
		line := "  " + ui.MacroPaletteKeyStyle.Render(macro.Key) + "  " +
			ui.FooterDimStyle.Render(macro.Name)
		lines = append(lines, line)
	}
	// Always show the create option
	lines = append(lines, "  "+ui.MacroPaletteKeyStyle.Render("=")+"  "+
		ui.FooterDimStyle.Render("create new macro…"))

	body := strings.Join(lines, "\n")
	title := ui.MacroPaletteTitleStyle.Render("Macros")

	content := title + "\n\n" + body
	return ui.MacroPaletteOverlayStyle.Render(content)
}
