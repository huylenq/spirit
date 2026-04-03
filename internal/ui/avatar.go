package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/spirit"
)

// avatarAnimalDef pairs each animal glyph with its human-readable name.
type avatarAnimalDef struct {
	Glyph string
	Name  string
}

// avatarAnimals is the ordered list of 23 animals.
var avatarAnimals = []avatarAnimalDef{
	{"\uEEED", "Cat"},
	{"\uEEF7", "Dog"},
	{"\uEE41", "Fish"},
	{"\uEDF8", "Frog"},
	{"\uEF04", "Horse"},
	{"\uEDEA", "Crow"},
	{"\uED99", "Dove"},
	{"\uEEF8", "Dragon"},
	{"\uEF03", "Hippo"},
	{"\uEF0A", "Otter"},
	{"\uEF10", "Spider"},
	{"\uEDFF", "Kiwi"},
	{"\uEC16", "Snake"},
	{"\U000F0B5F", "Bat"},
	{"\U000F0FA1", "Bee"},
	{"\U000F15C6", "Bird"},
	{"\U000F01E5", "Duck"},
	{"\U000F03D2", "Owl"},
	{"\U000F0EC0", "Penguin"},
	{"\U000F0401", "Pig"},
	{"\U000F0907", "Rabbit"},
	{"\U000F18BA", "Shark"},
	{"\uEEF1", "Cow"},
}

// avatarColorDef is the single source of truth for each avatar color.
type avatarColorDef struct {
	Fg        lipgloss.AdaptiveColor // primary foreground
	BadgeBgDk string                 // badge pill background (dark mode only; light uses Fg.Light)
	FillBg    lipgloss.AdaptiveColor // subtle selected-pane fill background
}

// avatarColorDefs is the 8-color palette (avoids status colors: amber/blue/purple).
var avatarColorDefs = []avatarColorDef{
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#c0284a", Dark: "#fb7185"},
		BadgeBgDk: "#3b1525",
		FillBg:    lipgloss.AdaptiveColor{Light: "#fce7eb", Dark: "#2a1520"},
	},
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#c2410c", Dark: "#fb923c"},
		BadgeBgDk: "#3b2010",
		FillBg:    lipgloss.AdaptiveColor{Light: "#ffedd5", Dark: "#2a1e10"},
	},
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#a16207", Dark: "#fbbf24"},
		BadgeBgDk: "#3b2e10",
		FillBg:    lipgloss.AdaptiveColor{Light: "#fef9c3", Dark: "#2a2510"},
	},
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#4d7c0f", Dark: "#a3e635"},
		BadgeBgDk: "#253b15",
		FillBg:    lipgloss.AdaptiveColor{Light: "#ecfccb", Dark: "#1e2a15"},
	},
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#0e7490", Dark: "#22d3ee"},
		BadgeBgDk: "#153035",
		FillBg:    lipgloss.AdaptiveColor{Light: "#cffafe", Dark: "#102a2a"},
	},
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#4338ca", Dark: "#818cf8"},
		BadgeBgDk: "#252535",
		FillBg:    lipgloss.AdaptiveColor{Light: "#e0e7ff", Dark: "#1e1e2a"},
	},
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#be185d", Dark: "#f472b6"},
		BadgeBgDk: "#351525",
		FillBg:    lipgloss.AdaptiveColor{Light: "#fce7f3", Dark: "#2a1525"},
	},
	{
		Fg:        lipgloss.AdaptiveColor{Light: "#0f766e", Dark: "#2dd4bf"},
		BadgeBgDk: "#153530",
		FillBg:    lipgloss.AdaptiveColor{Light: "#ccfbf1", Dark: "#102a25"},
	},
}

// avatarAdjectives references the shared spirit.Adjectives table.
var avatarAdjectives = spirit.Adjectives

const avatarBadgeFgLight = "#ffffff"

func init() {
	if len(avatarColorDefs) != len(avatarAdjectives) {
		panic("avatarAdjectives rows must match avatarColorDefs length")
	}
	if len(avatarAnimals) != len(avatarAdjectives[0]) {
		panic("avatarAdjectives columns must match avatarAnimals length")
	}
}

func animalDef(idx int) avatarAnimalDef {
	return avatarAnimals[idx%len(avatarAnimals)]
}

func colorDef(idx int) avatarColorDef {
	return avatarColorDefs[idx%len(avatarColorDefs)]
}

// AvatarGlyph returns the animal glyph for the given index.
func AvatarGlyph(idx int) string {
	return animalDef(idx).Glyph
}

// AvatarColor returns the adaptive color for the given index.
func AvatarColor(idx int) lipgloss.AdaptiveColor {
	return colorDef(idx).Fg
}

// AvatarFillBg returns the subtle background tint for the given avatar color index.
func AvatarFillBg(idx int) lipgloss.AdaptiveColor {
	return colorDef(idx).FillBg
}

// AvatarStyle returns a lipgloss style colored for the given index.
func AvatarStyle(idx int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(AvatarColor(idx))
}

// AvatarMnemonicName returns the unique mnemonic name for an avatar, e.g. "Ember Cat".
func AvatarMnemonicName(animalIdx, colorIdx int) string {
	ci := colorIdx % len(avatarAdjectives)
	ai := animalIdx % len(avatarAdjectives[0])
	return avatarAdjectives[ci][ai] + " " + animalDef(animalIdx).Name
}

// AvatarMnemonicBadge renders a colored pill badge with the mnemonic name.
func AvatarMnemonicBadge(animalIdx, colorIdx int) string {
	def := colorDef(colorIdx)
	name := AvatarMnemonicName(animalIdx, colorIdx)
	fg := lipgloss.AdaptiveColor{Light: avatarBadgeFgLight, Dark: def.Fg.Dark}
	bg := lipgloss.AdaptiveColor{Light: def.Fg.Light, Dark: def.BadgeBgDk}
	return lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Render(" " + name + " ")
}
