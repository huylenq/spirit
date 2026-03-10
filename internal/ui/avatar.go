package ui

import "github.com/charmbracelet/lipgloss"

// avatarAnimals is the ordered list of 23 Nerd Font animal glyphs.
var avatarAnimals = []string{
	"\uEEED", // nf-fa-cat
	"\uEEF7", // nf-fa-dog
	"\uEE41", // nf-fa-fish
	"\uEDF8", // nf-fa-frog
	"\uEF04", // nf-fa-horse
	"\uEDEA", // nf-fa-crow
	"\uED99", // nf-fa-dove
	"\uEEF8", // nf-fa-dragon
	"\uEF03", // nf-fa-hippo
	"\uEF0A", // nf-fa-otter
	"\uEF10", // nf-fa-spider
	"\uEDFF", // nf-fa-kiwi_bird
	"\uEC16", // nf-cod-snake
	"\U000F0B5F", // nf-md-bat
	"\U000F0FA1", // nf-md-bee
	"\U000F15C6", // nf-md-bird
	"\U000F01E5", // nf-md-duck
	"\U000F03D2", // nf-md-owl
	"\U000F0EC0", // nf-md-penguin
	"\U000F0401", // nf-md-pig
	"\U000F0907", // nf-md-rabbit
	"\U000F18BA", // nf-md-shark
	"\uEEF1", // nf-fa-cow
}

// avatarColors is the 8-color palette (avoids status colors: amber/blue/purple).
var avatarColors = []lipgloss.AdaptiveColor{
	{Light: "#c0284a", Dark: "#fb7185"}, // rose
	{Light: "#c2410c", Dark: "#fb923c"}, // orange
	{Light: "#a16207", Dark: "#fbbf24"}, // yellow
	{Light: "#4d7c0f", Dark: "#a3e635"}, // lime
	{Light: "#0e7490", Dark: "#22d3ee"}, // cyan
	{Light: "#4338ca", Dark: "#818cf8"}, // indigo
	{Light: "#be185d", Dark: "#f472b6"}, // pink
	{Light: "#0f766e", Dark: "#2dd4bf"}, // teal
}

// avatarFillBgs are very subtle tints of the avatar colors for selected-pane fill backgrounds.
var avatarFillBgs = []lipgloss.AdaptiveColor{
	{Light: "#fce7eb", Dark: "#2a1520"}, // rose
	{Light: "#ffedd5", Dark: "#2a1e10"}, // orange
	{Light: "#fef9c3", Dark: "#2a2510"}, // yellow
	{Light: "#ecfccb", Dark: "#1e2a15"}, // lime
	{Light: "#cffafe", Dark: "#102a2a"}, // cyan
	{Light: "#e0e7ff", Dark: "#1e1e2a"}, // indigo
	{Light: "#fce7f3", Dark: "#2a1525"}, // pink
	{Light: "#ccfbf1", Dark: "#102a25"}, // teal
}

// AvatarGlyph returns the animal glyph for the given index.
func AvatarGlyph(idx int) string {
	return avatarAnimals[idx%len(avatarAnimals)]
}

// AvatarColor returns the adaptive color for the given index.
func AvatarColor(idx int) lipgloss.AdaptiveColor {
	return avatarColors[idx%len(avatarColors)]
}

// AvatarFillBg returns the subtle background tint for the given avatar color index.
func AvatarFillBg(idx int) lipgloss.AdaptiveColor {
	return avatarFillBgs[idx%len(avatarFillBgs)]
}

// AvatarStyle returns a lipgloss style colored for the given index.
func AvatarStyle(idx int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(AvatarColor(idx))
}
