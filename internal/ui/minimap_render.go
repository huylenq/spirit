package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Rendering methods for MinimapModel.

type gridCell struct {
	char  string
	style lipgloss.Style
}

func (m MinimapModel) view(dockWidth int) string {
	if len(m.panes) == 0 || m.height < 5 {
		return ""
	}

	windows, winCols, winRows, innerW, gridH := m.computeLayout()
	if len(windows) == 0 || innerW < 8 || gridH < 1 {
		return ""
	}

	visibleWindows := windows
	hiddenBefore, hiddenAfter := 0, 0

	// Find which window contains the selected pane and pre-build its label style
	selectedWindowIdx := -1
	selectedLabelStyle := lipgloss.NewStyle()
	for _, p := range m.panes {
		if p.PaneID == m.selectedPaneID {
			selectedWindowIdx = p.WindowIndex
			fg := lipgloss.AdaptiveColor{Light: "#374151", Dark: "#e5e7eb"}
			if p.Status != PaneStatusNone {
				fg = AvatarColor(p.AvatarColorIdx)
			}
			selectedLabelStyle = lipgloss.NewStyle().Foreground(fg).Bold(true)
			break
		}
	}

	// Render per-window columns: centered label + grid
	var windowColumns []string
	for i, w := range visibleWindows {
		cols := winCols[i]
		rows := winRows[i]

		// Centered window index label — highlight if it contains the selected pane
		labelText := truncateStr(fmt.Sprintf("%d:%s", w.Index, w.Name), cols)
		labelStyle := minimapTabStyle
		if w.Index == selectedWindowIdx {
			labelStyle = selectedLabelStyle
		}
		label := labelStyle.Render(labelText)
		labelWidth := ansi.StringWidth(label)
		pad := (cols - labelWidth) / 2
		if pad < 0 {
			pad = 0
		}
		centeredLabel := strings.Repeat(" ", pad) + label

		grid := renderWindowGrid(w, cols, rows, m.spinnerView)
		// Vertically center collapsed windows within the full grid height
		if rows < gridH {
			topPad := (gridH - rows) / 2
			botPad := gridH - rows - topPad
			blank := strings.Repeat(" ", cols)
			var padded []string
			for j := 0; j < topPad; j++ {
				padded = append(padded, blank)
			}
			padded = append(padded, strings.Split(grid, "\n")...)
			for j := 0; j < botPad; j++ {
				padded = append(padded, blank)
			}
			grid = strings.Join(padded, "\n")
		}
		windowColumns = append(windowColumns, centeredLabel+"\n"+grid)
	}

	// Hidden window indicators
	prefix := ""
	suffix := ""
	if hiddenBefore > 0 {
		prefix = minimapTabStyle.Render(fmt.Sprintf("+%d", hiddenBefore))
	}
	if hiddenAfter > 0 {
		suffix = minimapTabStyle.Render(fmt.Sprintf("+%d", hiddenAfter))
	}

	// Build vertical separator between windows: 3-char centered stub
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#e5e7eb", Dark: "#2d3341"})
	sepVisible := 2
	if m.collapse {
		sepVisible = collapsedGridH // match collapsed box height for alignment
	}
	if sepVisible > gridH {
		sepVisible = gridH
	}
	topPadSep := (gridH - sepVisible) / 2
	var sepLines []string
	sepLines = append(sepLines, "   ") // label row blank
	for r := 0; r < gridH; r++ {
		if r >= topPadSep && r < topPadSep+sepVisible {
			sepLines = append(sepLines, " "+sepStyle.Render("│")+" ")
		} else {
			sepLines = append(sepLines, "   ")
		}
	}
	sep := strings.Join(sepLines, "\n")

	parts := interleave(windowColumns, sep)
	if prefix != "" {
		// Pad prefix to grid height (+1 label row)
		prefixCol := prefix + strings.Repeat("\n ", gridH)
		parts = append([]string{prefixCol, sep}, parts...)
	}
	if suffix != "" {
		suffixCol := suffix + strings.Repeat("\n ", gridH)
		parts = append(parts, sep, suffixCol)
	}

	gridStr := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	// Session name, centered
	sessionLabel := minimapSessionStyle.
		Width(innerW).
		Align(lipgloss.Center).
		Render(truncateStr(m.sessionName, innerW))

	// Compose inner content
	inner := lipgloss.JoinVertical(lipgloss.Left,
		sessionLabel,
		gridStr,
	)

	// In docked mode, stretch border to fill container width;
	// truncate inner content that overflows the budget.
	borderW := innerW + 2 // +2 so padding doesn't eat into content
	if dockWidth > 0 {
		borderW = dockWidth - 2 // subtract left+right border chars
		contentW := borderW - 2 // subtract left+right padding
		if innerW > contentW {
			lines := strings.Split(inner, "\n")
			for i, line := range lines {
				if ansi.StringWidth(line) > contentW {
					lines[i] = ansi.Truncate(line, contentW, "")
				}
			}
			inner = strings.Join(lines, "\n")
		}
	}

	return minimapBorderStyle.
		Width(borderW).
		Render(inner)
}

func renderWindowGrid(w windowGroup, cols, rows int, spinnerView string) string {
	if w.Width == 0 || w.Height == 0 || cols < 3 || rows < 1 {
		return strings.Repeat("\n", rows)
	}

	// Create a 2D grid of cells
	grid := make([][]gridCell, rows)
	for r := range grid {
		grid[r] = make([]gridCell, cols)
	}

	// Track the selected pane to render as a lipgloss box overlay
	type selPaneInfo struct {
		pane            minimapPane
		x1, y1, x2, y2 int
	}
	var selPane *selPaneInfo

	for _, p := range w.Panes {
		// Scale pane coordinates to grid (scaleEdge adjusts for tmux separators)
		x1 := scaleEdge(p.Left, w.Width, cols)
		y1 := scaleEdge(p.Top, w.Height, rows)
		x2 := int(math.Round(float64(p.Left+p.Width) / float64(w.Width) * float64(cols)))
		y2 := int(math.Round(float64(p.Top+p.Height) / float64(w.Height) * float64(rows)))

		// Enforce minimum size
		if x2-x1 < 3 {
			x2 = x1 + 3
		}
		if y2-y1 < 1 {
			y2 = y1 + 1
		}

		// Clamp to grid
		if x1 < 0 {
			x1 = 0
		}
		if y1 < 0 {
			y1 = 0
		}
		if x2 > cols {
			x2 = cols
		}
		if y2 > rows {
			y2 = rows
		}

		if p.IsSelected {
			// Defer selected pane to lipgloss box renderer — Background() fills
			// only the content area (not border chars), so no fill extrusion.
			pCopy := p
			selPane = &selPaneInfo{pane: pCopy, x1: x1, y1: y1, x2: x2, y2: y2}
			continue
		}

		ss := stylesForStatus(p.Status)
		isClaude := p.Status != PaneStatusNone

		tl, tr, bl, br, hz, vt := "┌", "┐", "└", "┘", "─", "│"

		// For Claude panes, use avatar color for borders and avatar glyph as center icon
		var avatarSt lipgloss.Style
		if isClaude {
			avatarSt = lipgloss.NewStyle().Foreground(AvatarColor(p.AvatarColorIdx))
		}

		// Interior dimensions (excluding border)
		paneW := x2 - x1
		paneH := y2 - y1

		// Center position for avatar glyph
		centerR := (y1 + y2 - 1) / 2
		centerC := (x1 + x2 - 1) / 2

		// Spinner placement for agent-turn Claude panes
		hasSpinner := isClaude && p.Status == PaneStatusAgentTurn && spinnerView != ""
		spinR, spinC := -1, -1
		if hasSpinner {
			innerW := paneW - 2 // excluding left+right border
			innerH := paneH - 2 // excluding top+bottom border
			if innerW >= 3 {
				// Horizontal: spinner one cell right of avatar
				spinR = centerR
				spinC = centerC + 2
			} else if innerH >= 2 {
				// Vertical: spinner one row below avatar
				spinR = centerR + 1
				spinC = centerC
			}
			// else: no room, skip spinner
		}

		// Draw box characters
		for r := y1; r < y2; r++ {
			for c := x1; c < x2; c++ {
				if c >= cols || r >= rows {
					continue
				}
				var ch string
				cellStyle := ss.Style
				isTop := r == y1
				isBot := r == y2-1
				isLeft := c == x1
				isRight := c == x2-1
				isCenter := r == centerR && c == centerC
				isSpinner := r == spinR && c == spinC

				switch {
				case isTop && isLeft:
					ch = tl
				case isTop && isRight:
					ch = tr
				case isBot && isLeft:
					ch = bl
				case isBot && isRight:
					ch = br
				case isTop || isBot:
					ch = hz
				case isLeft || isRight:
					ch = vt
				case isClaude && isCenter:
					ch = AvatarGlyph(p.AvatarAnimalIdx)
				case hasSpinner && isSpinner:
					ch = spinnerView
				default:
					ch = " "
				}

				// Claude panes: use avatar color for borders, icon, and spinner
				if isClaude && (isTop || isBot || isLeft || isRight || isCenter || isSpinner) {
					cellStyle = avatarSt
				}

				grid[r][c] = gridCell{char: ch, style: cellStyle}
			}
		}
	}

	// Render non-selected panes to string
	var lines []string
	for _, row := range grid {
		var line strings.Builder
		for _, cell := range row {
			if cell.char == "" {
				line.WriteString(" ")
			} else {
				line.WriteString(cell.style.Render(cell.char))
			}
		}
		lines = append(lines, line.String())
	}
	gridStr := strings.Join(lines, "\n")

	// Overlay the selected pane as a proper lipgloss box.
	// lipgloss.Background() fills content area only — border chars stay on
	// terminal default background, giving a clean "fill inside the box" look.
	if selPane != nil {
		// innerW excludes border (2) + 1-col side gaps (2) so padding on the box
		// creates terminal-default-bg gaps between border and fill.
		innerW := (selPane.x2 - selPane.x1) - 4
		innerH := (selPane.y2 - selPane.y1) - 2
		if innerW < 1 {
			innerW = 1
		}
		if innerH < 1 {
			innerH = 1
		}

		ss := stylesForStatus(selPane.pane.Status)
		isClaude := selPane.pane.Status != PaneStatusNone

		borderColor := ss.BorderColor
		fillBg := ss.FillBg
		iconStr := ""
		hAlign := lipgloss.Center
		if isClaude {
			avatarColor := AvatarColor(selPane.pane.AvatarColorIdx)
			borderColor = avatarColor
			fillBg = AvatarFillBg(selPane.pane.AvatarColorIdx)
			glyph := AvatarGlyph(selPane.pane.AvatarAnimalIdx)
			glyphW := ansi.StringWidth(glyph)

			hasSpinner := selPane.pane.Status == PaneStatusAgentTurn && spinnerView != ""
			if hasSpinner {
				spinW := ansi.StringWidth(spinnerView)
				glyphPad := max((innerW-glyphW)/2, 0)
				if glyphPad+glyphW+1+spinW <= innerW {
					hAlign = lipgloss.Left
					iconStr = strings.Repeat(" ", glyphPad) + glyph + " " + spinnerView
				} else if innerH >= 2 {
					iconStr = glyph + "\n" + spinnerView
				} else {
					iconStr = glyph
				}
			} else {
				iconStr = glyph
			}
		}

		// Interior style carries fg+bg together so there are no ANSI reset
		// gaps between content segments (glyph, space, spinner).
		interiorSt := lipgloss.NewStyle().
			Width(innerW).
			Height(innerH).
			Align(hAlign).
			AlignVertical(lipgloss.Center).
			Background(fillBg)
		if isClaude {
			interiorSt = interiorSt.Foreground(AvatarColor(selPane.pane.AvatarColorIdx))
		}
		interior := interiorSt.Render(iconStr)
		box := lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(borderColor).
			PaddingLeft(1).
			PaddingRight(1).
			Render(interior)

		gridStr = overlayAt(gridStr, box, selPane.x1, selPane.y1)
	}

	return gridStr
}

// overlayAt composites overlay onto base starting at (col, row) in visible cell coordinates.
func overlayAt(base, overlay string, col, row int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	for i, oLine := range overlayLines {
		r := row + i
		if r < 0 || r >= len(baseLines) {
			continue
		}
		oWidth := ansi.StringWidth(oLine)
		baseLine := baseLines[r]
		baseWidth := ansi.StringWidth(baseLine)

		var prefix string
		if col > 0 {
			if col <= baseWidth {
				prefix = ansi.Truncate(baseLine, col, "")
			} else {
				prefix = baseLine + strings.Repeat(" ", col-baseWidth)
			}
		}

		suffix := ""
		if afterCol := col + oWidth; afterCol < baseWidth {
			suffix = ansi.TruncateLeft(baseLine, afterCol, "")
		}

		baseLines[r] = prefix + oLine + suffix
	}

	return strings.Join(baseLines, "\n")
}

func interleave(items []string, sep string) []string {
	if len(items) <= 1 {
		return items
	}
	result := make([]string, 0, len(items)*2-1)
	for i, item := range items {
		if i > 0 {
			result = append(result, sep)
		}
		result = append(result, item)
	}
	return result
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// OverlayBottomLeft composites the overlay string onto the base at bottom-left.
func OverlayBottomLeft(base, overlay string) string {
	if overlay == "" {
		return base
	}

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Start overlay at bottom of base
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
		baseWidth := ansi.StringWidth(baseLines[row])

		if oWidth >= baseWidth {
			baseLines[row] = oLine
		} else {
			// Replace the left portion of the base line with the overlay line
			// We need to truncate base from the right side after the overlay
			remainder := ansi.TruncateLeft(baseLines[row], oWidth, "")
			baseLines[row] = oLine + remainder
		}
	}

	return strings.Join(baseLines, "\n")
}
