package destroyer

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Fragment is a positioned rune extracted from rendered TUI output.
type Fragment struct {
	X, Y      int
	Rune      rune
	AnsiStyle string // accumulated ANSI SGR prefix active when this rune was rendered
}

// DecomposeStyled walks each line of rendered TUI output, tracking the active
// ANSI SGR state. Each visible non-space rune is captured with its color/style
// prefix so particles can be rendered with original colors preserved.
func DecomposeStyled(rendered string, width, height int) []Fragment {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}

	var frags []Fragment
	for y, line := range lines {
		col := 0
		var stylePrefix string
		bytes := []byte(line)
		i := 0

		for i < len(bytes) {
			// ANSI escape sequence: \x1b[ ... <terminator letter>
			if bytes[i] == 0x1b && i+1 < len(bytes) && bytes[i+1] == '[' {
				j := i + 2
				for j < len(bytes) && !isAnsiTerminator(bytes[j]) {
					j++
				}
				if j < len(bytes) {
					seq := string(bytes[i : j+1])
					if bytes[j] == 'm' {
						// SGR sequence — update style state
						if seq == "\x1b[0m" || seq == "\x1b[m" {
							stylePrefix = "" // reset clears all
						} else {
							stylePrefix += seq
						}
					}
					i = j + 1
					continue
				}
				// Malformed sequence — skip the ESC byte
				i++
				continue
			}

			// Decode UTF-8 rune
			r, size := utf8.DecodeRune(bytes[i:])
			if r == utf8.RuneError && size <= 1 {
				i++
				continue
			}
			if col >= width {
				break
			}
			if r != ' ' && r != '\t' {
				frags = append(frags, Fragment{
					X:         col,
					Y:         y,
					Rune:      r,
					AnsiStyle: stylePrefix,
				})
			}
			col += runewidth.RuneWidth(r)
			i += size
		}
	}
	return frags
}

// isAnsiTerminator returns true for bytes that end an ANSI escape sequence.
func isAnsiTerminator(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
