package ui

import (
	"embed"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

//go:embed spirits/*.txt
var spiritFS embed.FS

// SpiritArt loads the ASCII art for a given animal+color combination from the embedded FS.
// Returns empty string if not found.
func SpiritArt(animalIdx, colorIdx int) string {
	adj := avatarAdjectives[colorIdx%len(avatarAdjectives)][animalIdx%len(avatarAdjectives[0])]
	animal := animalDef(animalIdx).Name
	filename := fmt.Sprintf("spirits/%s_%s.txt", strings.ToLower(adj), strings.ToLower(animal))
	data, err := spiritFS.ReadFile(filename)
	if err != nil {
		return ""
	}
	return string(data)
}

// RenderSpiritOverlay renders the spirit animal ASCII art in a centered lipgloss box
// colored with the avatar's foreground color, with a title line showing glyph + mnemonic name.
// Falls back to a compact badge when terminal is too small (< 124x44).
func RenderSpiritOverlay(animalIdx, colorIdx, termW, termH int) string {
	glyph := AvatarGlyph(animalIdx)
	name := AvatarMnemonicName(animalIdx, colorIdx)
	fg := AvatarColor(colorIdx)

	// Compact fallback for small terminals
	if termW < 124 || termH < 44 {
		badge := lipgloss.NewStyle().
			Bold(true).
			Foreground(fg).
			Render(glyph + " " + name)
		return lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(fg).
			Padding(1, 3).
			Render(badge)
	}

	art := SpiritArt(animalIdx, colorIdx)
	if art == "" {
		// No art file — show name badge as fallback
		badge := lipgloss.NewStyle().
			Bold(true).
			Foreground(fg).
			Render(glyph + " " + name)
		return lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(fg).
			Padding(1, 3).
			Render(badge)
	}

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(fg).
		Render(glyph + "  " + name)

	artStyled := lipgloss.NewStyle().
		Foreground(fg).
		Render(art)

	body := title + "\n\n" + artStyled

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(fg).
		Padding(0, 1).
		Render(body)
}
