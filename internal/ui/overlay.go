package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// OverlayCentered composites the overlay string onto the base, centered both vertically and horizontally.
func OverlayCentered(base, overlay string, baseWidth int) string {
	if overlay == "" {
		return base
	}

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Find max overlay line width
	maxOW := 0
	for _, oLine := range overlayLines {
		if w := ansi.StringWidth(oLine); w > maxOW {
			maxOW = w
		}
	}

	startRow := (len(baseLines) - len(overlayLines)) / 2
	if startRow < 0 {
		startRow = 0
	}
	startCol := (baseWidth - maxOW) / 2
	if startCol < 0 {
		startCol = 0
	}

	for i, oLine := range overlayLines {
		row := startRow + i
		if row >= len(baseLines) {
			break
		}

		oWidth := ansi.StringWidth(oLine)
		endCol := startCol + oWidth

		bLine := baseLines[row]
		bWidth := ansi.StringWidth(bLine)

		// Build: [left portion of base] + [overlay] + [right portion of base]
		var left, right string
		if startCol >= bWidth {
			left = bLine + strings.Repeat(" ", startCol-bWidth)
		} else {
			left = ansi.Truncate(bLine, startCol, "")
		}
		if endCol < bWidth {
			right = ansi.TruncateLeft(bLine, endCol, "")
		}
		baseLines[row] = left + oLine + right
	}

	return strings.Join(baseLines, "\n")
}

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
