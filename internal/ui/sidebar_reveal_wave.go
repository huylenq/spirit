package ui

// TUI-reveal wave overlay — applied after the row is rendered with the resting
// AvatarFillBg so the wave doesn't disturb the entry's base color. Sweeps a
// soft highlight band left→right across the row over landMaxFrames.

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// applyRevealWave overlays a left→right brightness wave on the row content.
// It only paints when the reveal animation is active on paneID.
// startCol is the column where the row's selection background begins (the
// vertical bar in sidebar mode, col 0 in card mode). Cells before startCol
// (slot/flag indicators) are left untouched.
func (m SidebarModel) applyRevealWave(content string, paneID string, avatarColorIdx, startCol, width int) string {
	if !m.isRevealing(paneID) || width <= startCol {
		return content
	}
	cols := m.revealWaveColumns(avatarColorIdx, startCol, width)
	return rewriteLineBg(content, startCol, width, cols)
}

// revealWaveColumns precomputes one RGB triple per column in [startCol, width).
// Returned slice is indexed by absolute column; entries before startCol are zero.
func (m SidebarModel) revealWaveColumns(avatarColorIdx, startCol, width int) [][3]uint8 {
	fillBg := AvatarFillBg(avatarColorIdx)
	avatarColor := AvatarColor(avatarColorIdx)

	const peakMix = 0.45
	baseHex := fillBg.Dark
	peakHex := blendHex(fillBg.Dark, avatarColor.Dark, peakMix)
	if !lipgloss.HasDarkBackground() {
		baseHex = fillBg.Light
		peakHex = blendHex(fillBg.Light, avatarColor.Light, peakMix)
	}
	br, bg, bb := parseHexRGB(baseHex)
	pr, pg, pb := parseHexRGB(peakHex)

	bgWidth := width - startCol
	halfW := bgWidth / 3
	if halfW < 4 {
		halfW = 4
	}
	denom := m.landMaxFrames - 1
	if denom < 1 {
		denom = 1
	}
	progress := float64(m.landFrame) / float64(denom)
	center := float64(startCol) - float64(halfW) + progress*float64(bgWidth+2*halfW)

	cols := make([][3]uint8, width)
	for col := startCol; col < width; col++ {
		d := math.Abs(float64(col) - center)
		t := 1.0 - d/float64(halfW)
		switch {
		case t <= 0:
			cols[col] = [3]uint8{br, bg, bb}
		case t >= 1:
			cols[col] = [3]uint8{pr, pg, pb}
		default:
			t = t * t * (3.0 - 2.0*t) // smoothstep
			cols[col] = [3]uint8{
				uint8(float64(br) + t*(float64(pr)-float64(br))),
				uint8(float64(bg) + t*(float64(pg)-float64(bg))),
				uint8(float64(bb) + t*(float64(pb)-float64(bb))),
			}
		}
	}
	return cols
}

// rewriteLineBg walks each line of content, inserting a 24-bit background SGR
// before every visible cell in [startCol, width) using cols[col]. ANSI SGR
// sequences in the input are passed through verbatim so foreground styling is
// preserved. Default-bg is restored on exit from the wave region and at the
// end of each line.
func rewriteLineBg(content string, startCol, width int, cols [][3]uint8) string {
	var out strings.Builder
	// SGR per cell is ~18 bytes; account for that across the wave region.
	out.Grow(len(content) + (width-startCol)*18)

	src := []byte(content)
	col := 0
	inside := false
	for i := 0; i < len(src); {
		c := src[i]
		if c == '\n' {
			if inside {
				out.WriteString("\x1b[49m")
				inside = false
			}
			out.WriteByte('\n')
			col = 0
			i++
			continue
		}
		// Pass ANSI escape sequences through untouched.
		if c == 0x1b && i+1 < len(src) && src[i+1] == '[' {
			j := i + 2
			for j < len(src) && !isSGRTerminator(src[j]) {
				j++
			}
			if j < len(src) {
				out.Write(src[i : j+1])
				i = j + 1
				continue
			}
			i++
			continue
		}
		r, size := utf8.DecodeRune(src[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		within := col >= startCol && col < width
		if within {
			rgb := cols[col]
			appendBgSGR(&out, rgb[0], rgb[1], rgb[2])
			inside = true
		} else if inside {
			out.WriteString("\x1b[49m")
			inside = false
		}
		out.WriteRune(r)
		col += runewidth.RuneWidth(r)
		i += size
	}
	if inside {
		out.WriteString("\x1b[49m")
	}
	return out.String()
}

// appendBgSGR writes `\x1b[48;2;R;G;Bm` without going through fmt.
func appendBgSGR(out *strings.Builder, r, g, b uint8) {
	out.WriteString("\x1b[48;2;")
	out.WriteString(strconv.Itoa(int(r)))
	out.WriteByte(';')
	out.WriteString(strconv.Itoa(int(g)))
	out.WriteByte(';')
	out.WriteString(strconv.Itoa(int(b)))
	out.WriteByte('m')
}

func isSGRTerminator(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
