package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// OverlayBottomRight composites the overlay string onto the base at bottom-right.
func OverlayBottomRight(base, overlay string, baseWidth int) string {
	if overlay == "" {
		return base
	}

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	startRow := len(baseLines) - len(overlayLines)
	if startRow < 0 {
		startRow = 0
	}

	for i, oLine := range overlayLines {
		row := startRow + i
		if row >= len(baseLines) {
			break
		}

		oWidth := ansi.StringWidth(oLine)
		startCol := baseWidth - oWidth
		if startCol < 0 {
			startCol = 0
		}

		bLine := baseLines[row]
		bWidth := ansi.StringWidth(bLine)

		if startCol >= bWidth {
			// Pad with spaces to reach startCol, then append overlay
			baseLines[row] = bLine + strings.Repeat(" ", startCol-bWidth) + oLine
		} else {
			// Truncate base at startCol, then append overlay
			left := ansi.Truncate(bLine, startCol, "")
			baseLines[row] = left + oLine
		}
	}

	return strings.Join(baseLines, "\n")
}
