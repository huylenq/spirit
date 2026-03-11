package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
	dmp "github.com/sergi/go-diff/diffmatchpatch"
)

// diffHunkFile groups hunks by file for the diff overlay.
type diffHunkFile struct {
	name      string // basename
	added     int
	removed   int
	footprint int
	isNewFile bool // all hunks are Write (no Edit)
	hunks     []claude.FileDiffHunk
}

const defaultDiffSimThreshold = 0.3

// SetShowDiffs toggles the diff hunks overlay.
func (m *DetailModel) SetShowDiffs(show bool) {
	m.showDiffs = show
	m.diffScroll = 0
	if !show {
		m.diffHunks = nil
		m.diffHunkFiles = nil
	}
}

// AdjustDiffSimThreshold nudges the similarity threshold by delta, clamped to [0, 1].
func (m *DetailModel) AdjustDiffSimThreshold(delta float64) {
	t := m.diffSimThreshold + delta
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	m.diffSimThreshold = t
}

// SetDiffHunks sets the diff hunks and groups them by file.
// cwd is the session's working directory, used to compute relative display paths.
func (m *DetailModel) SetDiffHunks(hunks []claude.FileDiffHunk, cwd string) {
	m.diffHunks = hunks

	// Group by file path
	fileMap := make(map[string]*diffHunkFile)
	var order []string
	for _, h := range hunks {
		f, exists := fileMap[h.FilePath]
		if !exists {
			displayPath := h.FilePath
			if cwd != "" {
				if rel, err := filepath.Rel(cwd, h.FilePath); err == nil {
					displayPath = rel
				}
			}
			f = &diffHunkFile{
				name:      displayPath,
				isNewFile: true, // assume new until we see an Edit
			}
			fileMap[h.FilePath] = f
			order = append(order, h.FilePath)
		}
		f.hunks = append(f.hunks, h)
		added := strings.Count(h.NewString, "\n")
		removed := strings.Count(h.OldString, "\n")
		f.added += added
		f.removed += removed
		f.footprint += added + removed
		if !h.IsWrite {
			f.isNewFile = false
		}
	}

	files := make([]diffHunkFile, 0, len(fileMap))
	for _, p := range order {
		files = append(files, *fileMap[p])
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].footprint != files[j].footprint {
			return files[i].footprint > files[j].footprint
		}
		return files[i].name < files[j].name
	})
	m.diffHunkFiles = files
	m.diffScroll = 0
}

// ToggleDiffExpand is a no-op — the flat view is always fully expanded.
func (m *DetailModel) ToggleDiffExpand() {}

// diffVisLines returns the number of file rows visible in the diff overlay.
func (m *DetailModel) diffVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// maxHunkDisplayLines caps how many output lines a single hunk can produce.
const maxHunkDisplayLines = 30

// diffLine is a typed diff output line.
// kind '+'/'-' get full-width background in the caller; '~' uses char-level highlights only.
type diffLine struct {
	text    string // body text (no symbol prefix for +/-; pre-rendered for ~)
	kind    byte
	lineNum int // line number to show in gutter (old for -/~, new for +)
}

// renderInlineDiff computes a line-level diff, then for paired modified lines
// does a char-level pass to produce '~' inline-highlight lines.
// simThreshold is the minimum similarity ratio [0,1] to render as '~' rather than separate -/+.
func renderInlineDiff(oldStr, newStr string, maxWidth int, simThreshold float64) []diffLine {
	differ := dmp.New()

	chars1, chars2, lineArray := differ.DiffLinesToChars(oldStr, newStr)
	lineDiffs := differ.DiffMain(chars1, chars2, false)
	lineDiffs = differ.DiffCharsToLines(lineDiffs, lineArray)
	lineDiffs = differ.DiffCleanupSemantic(lineDiffs)

	type linePair struct{ old, new string }
	var pairs []linePair
	var delBuf, insBuf []string

	flushPairs := func() {
		n := max(len(delBuf), len(insBuf))
		for i := range n {
			var p linePair
			if i < len(delBuf) {
				p.old = delBuf[i]
			}
			if i < len(insBuf) {
				p.new = insBuf[i]
			}
			pairs = append(pairs, p)
		}
		delBuf = nil
		insBuf = nil
	}

	for _, d := range lineDiffs {
		text := strings.TrimRight(d.Text, "\n")
		for _, l := range strings.Split(text, "\n") {
			switch d.Type {
			case dmp.DiffEqual:
				flushPairs()
				pairs = append(pairs, linePair{old: l, new: l})
			case dmp.DiffDelete:
				delBuf = append(delBuf, l)
			case dmp.DiffInsert:
				insBuf = append(insBuf, l)
			}
		}
	}
	flushPairs()

	var result []diffLine
	oldLine, newLine := 1, 1
	for _, p := range pairs {
		if p.old == p.new {
			// Equal line — skip but advance both counters
			oldLine++
			newLine++
			continue
		}
		// Both sides non-empty: check similarity before using inline '~'
		if strings.TrimSpace(p.old) != "" && strings.TrimSpace(p.new) != "" {
			charDiffs := differ.DiffMain(p.old, p.new, false)
			charDiffs = differ.DiffCleanupSemantic(charDiffs)
			var eqC, totC int
			for _, cd := range charDiffs {
				n := utf8.RuneCountInString(cd.Text)
				totC += n
				if cd.Type == dmp.DiffEqual {
					eqC += n
				}
			}
			sim := 0.0
			if totC > 0 {
				sim = float64(eqC) / float64(totC)
			}
			if sim >= simThreshold {
				var buf strings.Builder
				buf.WriteString(DiffModSymbol.Render("~") + " ")
				for _, cd := range charDiffs {
					switch cd.Type {
					case dmp.DiffEqual:
						buf.WriteString(cd.Text)
					case dmp.DiffDelete:
						buf.WriteString(DiffInlineDelBg.Render(cd.Text))
					case dmp.DiffInsert:
						buf.WriteString(DiffInlineAddBg.Render(cd.Text))
					}
				}
				result = append(result, diffLine{text: ansi.Truncate(buf.String(), maxWidth, "…"), kind: '~', lineNum: oldLine})
				oldLine++
				newLine++
				continue
			}
			// Too different — fall through to separate -/+ lines
		}
		if strings.TrimSpace(p.old) != "" {
			result = append(result, diffLine{text: p.old, kind: '-', lineNum: oldLine})
			oldLine++
		}
		if strings.TrimSpace(p.new) != "" {
			result = append(result, diffLine{text: p.new, kind: '+', lineNum: newLine})
			newLine++
		}

		if len(result) >= maxHunkDisplayLines {
			remaining := len(pairs) - len(result)
			if remaining > 0 {
				result = append(result, diffLine{text: fmt.Sprintf("  … (%d more changes)", remaining), kind: '~'})
			}
			break
		}
	}
	return result
}

func (m DetailModel) renderDiffOverlay(width, height int) string {
	fileCount := len(m.diffHunkFiles)
	simThreshold := m.diffSimThreshold
	thresholdHint := lipgloss.NewStyle().Foreground(ColorMuted).Render(fmt.Sprintf("  ~/≥%.0f%%  [/]", simThreshold*100))
	titleLine := DiffTitleStyle.Render(fmt.Sprintf(" File Changes (%d files)", fileCount)) + thresholdHint

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	if fileCount == 0 {
		lines = append(lines, DetailMetaStyle.Render("No file changes"))
	} else {
		// innerWidth = total visual width of each file box line
		innerWidth := width - 6    // outer border(2) + outer padding(2) + reserved(2)
		contentW := innerWidth - 4 // │ _ content _ │

		borderSt := lipgloss.NewStyle().Foreground(ColorBorder)
		hunkSepSt := lipgloss.NewStyle().Foreground(ColorBorder)
		rowSt := lipgloss.NewStyle().Width(contentW)

		// Dashed separator: exactly contentW chars so the line matches innerWidth.
		hunkSep := strings.Repeat("- ", contentW/2) + strings.Repeat("-", contentW%2)
		hunkSepLine := borderSt.Render("│") + " " + hunkSepSt.Render(hunkSep) + " " + borderSt.Render("│")

		bottomBorder := borderSt.Render("╰" + strings.Repeat("─", innerWidth-2) + "╯")

		var allLines []string

		for _, f := range m.diffHunkFiles {
			icon := IconModified
			if f.isNewFile {
				icon = IconNewFile
			}
			addStr := DiffAddedStyle.Render(fmt.Sprintf("+%d", f.added))
			rmStr := StatWorkingStyle.Render(fmt.Sprintf("-%d", f.removed))

			// Top border with embedded filename + stats
			titleRaw := fmt.Sprintf("%s %s  %s %s", icon, f.name, addStr, rmStr)
			titleVisLen := ansi.StringWidth(titleRaw)
			fill := innerWidth - titleVisLen - 5
			if fill < 0 {
				fill = 0
			}
			topBorder := borderSt.Render("╭─") + " " + titleRaw + " " + borderSt.Render(strings.Repeat("─", fill)+"╮")
			allLines = append(allLines, topBorder)

			gutterW := 4 // "123 " = 3 digits + 1 space
			gutterSt := lipgloss.NewStyle().Foreground(ColorMuted)

			wrapLine := func(dl string) string {
				return borderSt.Render("│") + " " + rowSt.Render(dl) + " " + borderSt.Render("│")
			}
			// Symbols rendered with bg+fg combined so the full line bg doesn't get
			// killed by the symbol's ANSI reset mid-line.
			addSym := DiffAddBg.Inherit(DiffAddSymbol).Render("+ ")
			delSym := DiffDelBg.Inherit(DiffDelSymbol).Render("- ")
			bodyW := contentW - 2 - gutterW // symbol(2) + gutter

			wrapTyped := func(dl diffLine) string {
				gutter := gutterSt.Render(fmt.Sprintf("%3d ", dl.lineNum))
				switch dl.kind {
				case '+':
					body := DiffAddBg.Width(bodyW).Render(ansi.Truncate(dl.text, bodyW, "…"))
					return borderSt.Render("│") + " " + gutter + addSym + body + " " + borderSt.Render("│")
				case '-':
					body := DiffDelBg.Width(bodyW).Render(ansi.Truncate(dl.text, bodyW, "…"))
					return borderSt.Render("│") + " " + gutter + delSym + body + " " + borderSt.Render("│")
				default:
					gutter = gutterSt.Render(fmt.Sprintf("%3d ", dl.lineNum))
					return wrapLine(gutter + dl.text)
				}
			}

			for hi, h := range f.hunks {
				if hi > 0 {
					allLines = append(allLines, hunkSepLine)
				}
				if h.IsWrite {
					lineNum := 1
					for _, dl := range strings.Split(h.NewString, "\n") {
						if strings.TrimSpace(dl) == "" {
							lineNum++
							continue
						}
						allLines = append(allLines, wrapTyped(diffLine{text: dl, kind: '+', lineNum: lineNum}))
						lineNum++
					}
				} else {
					for _, dl := range renderInlineDiff(h.OldString, h.NewString, contentW-gutterW, simThreshold) {
						allLines = append(allLines, wrapTyped(dl))
					}
				}
			}

			allLines = append(allLines, bottomBorder)
			allLines = append(allLines, "")
		}

		// Line-based scroll
		scrollIdx := m.diffScroll
		if scrollIdx >= len(allLines) {
			scrollIdx = max(0, len(allLines)-1)
		}
		visLines := m.diffVisLines()
		end := scrollIdx + visLines
		if end > len(allLines) {
			end = len(allLines)
		}
		lines = append(lines, allLines[scrollIdx:end]...)

		// Scroll indicator
		total := len(allLines)
		if total > visLines {
			pct := 0
			if total-visLines > 0 {
				pct = (scrollIdx * 100) / (total - visLines)
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(ColorMuted).Render(fmt.Sprintf("── %d%% ──", pct)))
		}
	}

	content := strings.Join(lines, "\n")
	return DiffOverlayStyle.
		Width(width).
		Height(height).
		Render(content)
}
