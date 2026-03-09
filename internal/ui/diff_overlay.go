package ui

import (
	"fmt"
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

// SetShowDiffs toggles the diff hunks overlay.
func (m *PreviewModel) SetShowDiffs(show bool) {
	m.showDiffs = show
	m.diffFileCursor = 0
	m.diffScroll = 0
	m.diffExpanded = nil
	if !show {
		m.diffHunks = nil
		m.diffHunkFiles = nil
	}
}

// SetDiffHunks sets the diff hunks and groups them by file.
func (m *PreviewModel) SetDiffHunks(hunks []claude.FileDiffHunk) {
	m.diffHunks = hunks

	// Group by file path
	fileMap := make(map[string]*diffHunkFile)
	var order []string
	for _, h := range hunks {
		f, exists := fileMap[h.FilePath]
		if !exists {
			parts := strings.Split(h.FilePath, "/")
			f = &diffHunkFile{
				name:      parts[len(parts)-1],
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

	// Auto-expand first file
	m.diffExpanded = make(map[int]bool)
	if len(files) > 0 {
		m.diffExpanded[0] = true
	}
	m.diffFileCursor = 0
	m.diffScroll = 0
}

// ToggleDiffExpand toggles expansion of the file at the current cursor.
func (m *PreviewModel) ToggleDiffExpand() {
	if !m.showDiffs || len(m.diffHunkFiles) == 0 {
		return
	}
	if m.diffExpanded == nil {
		m.diffExpanded = make(map[int]bool)
	}
	m.diffExpanded[m.diffFileCursor] = !m.diffExpanded[m.diffFileCursor]
}

// diffVisLines returns the number of file rows visible in the diff overlay.
func (m *PreviewModel) diffVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// maxHunkDisplayLines caps how many output lines a single hunk can produce.
const maxHunkDisplayLines = 30

// renderInlineDiff computes a line-level diff first (using DiffLinesToChars to
// treat lines as atomic units), then for paired modified lines does a char-level
// pass to highlight word/segment changes on the same output line.
// maxWidth is the available visual width for truncation.
func renderInlineDiff(oldStr, newStr string, maxWidth int) []string {
	differ := dmp.New()

	// Phase 1: line-level diff using DiffLinesToChars (maps each unique line to
	// a single rune so DiffMain diffs lines, not characters).
	chars1, chars2, lineArray := differ.DiffLinesToChars(oldStr, newStr)
	lineDiffs := differ.DiffMain(chars1, chars2, false)
	lineDiffs = differ.DiffCharsToLines(lineDiffs, lineArray)
	lineDiffs = differ.DiffCleanupSemantic(lineDiffs)

	// Phase 2: collect delete/insert runs, pair them for inline rendering
	type linePair struct {
		old string // empty = pure insert
		new string // empty = pure delete
	}
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
		// DiffCharsToLines restores full lines including trailing \n
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

	// Phase 3: render changed pairs only (skip equal lines, show ··· for context gaps)
	var result []string
	lastWasContext := false
	for _, p := range pairs {
		switch {
		case p.old == p.new:
			// Equal line — skip but mark context gap
			if !lastWasContext && len(result) > 0 {
				result = append(result, PreviewMetaStyle.Render("  ···"))
				lastWasContext = true
			}
			continue
		case p.old == "":
			if strings.TrimSpace(p.new) == "" {
				continue // skip blank inserts
			}
			result = append(result, ansi.Truncate(DiffAddBg.Render("+ "+p.new), maxWidth, "…"))
		case p.new == "":
			if strings.TrimSpace(p.old) == "" {
				continue // skip blank deletes
			}
			result = append(result, ansi.Truncate(DiffDelBg.Render("- "+p.old), maxWidth, "…"))
		default:
			if strings.TrimSpace(p.old) == "" && strings.TrimSpace(p.new) == "" {
				continue
			}
			charDiffs := differ.DiffMain(p.old, p.new, false)
			charDiffs = differ.DiffCleanupSemantic(charDiffs)
			// Similarity: ratio of equal chars to total
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
			if sim < 0.05 {
				if strings.TrimSpace(p.old) != "" {
					result = append(result, ansi.Truncate(DiffDelBg.Render("- "+p.old), maxWidth, "…"))
				}
				if strings.TrimSpace(p.new) != "" {
					result = append(result, ansi.Truncate(DiffAddBg.Render("+ "+p.new), maxWidth, "…"))
				}
			} else {
				var buf strings.Builder
				buf.WriteString("~ ")
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
				result = append(result, ansi.Truncate(buf.String(), maxWidth, "…"))
			}
		}
		lastWasContext = false

		if len(result) >= maxHunkDisplayLines {
			remaining := len(pairs) - len(result)
			if remaining > 0 {
				result = append(result, PreviewMetaStyle.Render(fmt.Sprintf("  … (%d more changes)", remaining)))
			}
			break
		}
	}
	return result
}

func (m PreviewModel) renderDiffOverlay(width, height int) string {
	fileCount := len(m.diffHunkFiles)
	titleLine := DiffTitleStyle.Render(fmt.Sprintf(" File Changes (%d files)", fileCount))

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	if fileCount == 0 {
		lines = append(lines, PreviewMetaStyle.Render("No file changes"))
	} else {
		innerWidth := width - 6 // border(2) + padding(2) + cursor(2)
		clipStyle := lipgloss.NewStyle().MaxWidth(innerWidth)

		// Build all rendered lines (files + expanded hunks)
		type renderedLine struct {
			text    string
			fileIdx int // which file this line belongs to (-1 for separators)
		}
		var allLines []renderedLine

		for i, f := range m.diffHunkFiles {
			// Cursor indicator
			cursor := "  "
			if i == m.diffFileCursor {
				cursor = "> "
			}

			// Expand/collapse indicator
			expandIcon := "▸ "
			if m.diffExpanded[i] {
				expandIcon = "▾ "
			}

			// File type icon
			icon := IconModified
			if f.isNewFile {
				icon = IconNewFile
			}

			// Stats
			addStr := DiffAddedStyle.Render(fmt.Sprintf("+%d", f.added))
			rmStr := StatWorkingStyle.Render(fmt.Sprintf("-%d", f.removed))

			line := fmt.Sprintf("%s%s%s %s  %s %s", cursor, expandIcon, icon, f.name, addStr, rmStr)
			allLines = append(allLines, renderedLine{text: clipStyle.Render(line), fileIdx: i})

			// Expanded hunks with inline diffs
			if m.diffExpanded[i] {
				for hi, h := range f.hunks {
					if h.IsWrite {
						// Write (new file): just show + lines
						for _, dl := range strings.Split(h.NewString, "\n") {
							if strings.TrimSpace(dl) == "" {
								continue
							}
							styled := ansi.Truncate(DiffAddBg.Render("+ "+dl), innerWidth-4, "…")
							allLines = append(allLines, renderedLine{text: "    " + styled, fileIdx: i})
						}
					} else {
						// Edit: inline diff with word-level highlighting
						for _, dl := range renderInlineDiff(h.OldString, h.NewString, innerWidth-4) {
							allLines = append(allLines, renderedLine{text: "    " + dl, fileIdx: i})
						}
					}
					// Separator between hunks (skip after last)
					if hi < len(f.hunks)-1 {
						sep := PreviewMetaStyle.Render("    " + strings.Repeat("·", min(innerWidth-8, 30)))
						allLines = append(allLines, renderedLine{text: sep, fileIdx: i})
					}
				}
			}
		}

		// Apply scroll: find the first line of the scroll-start file
		scrollLineIdx := 0
		if m.diffScroll > 0 {
			for idx, rl := range allLines {
				if rl.fileIdx >= m.diffScroll {
					scrollLineIdx = idx
					break
				}
			}
		}

		visLines := m.diffVisLines()
		end := scrollLineIdx + visLines
		if end > len(allLines) {
			end = len(allLines)
		}

		for _, rl := range allLines[scrollLineIdx:end] {
			lines = append(lines, rl.text)
		}

		// Scroll indicator
		if len(allLines) > visLines {
			pct := 0
			if len(allLines)-visLines > 0 {
				pct = (scrollLineIdx * 100) / (len(allLines) - visLines)
			}
			indicator := PreviewMetaStyle.Render(fmt.Sprintf("── %d/%d files (%d%%) ──", min(m.diffFileCursor+1, fileCount), fileCount, pct))
			lines = append(lines, indicator)
		}
	}

	content := strings.Join(lines, "\n")
	return DiffOverlayStyle.
		Width(width).
		Height(height).
		Render(content)
}
