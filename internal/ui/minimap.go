package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/tmux"
)

// MinimapModel renders a spatial minimap of tmux pane layout.
type MinimapModel struct {
	panes          []minimapPane
	sessionName    string
	selectedPaneID string
	height         int
	windowCols     int    // columns per window in the grid (default 40)
	collapse       bool   // collapse single-pane windows to narrower columns
	spinnerView    string // current spinner animation frame (set externally)
	LastNavDebug   string // debug: last navigation attempt result
}

// PaneStatus constants for minimap rendering (UI concept, not tied to claude.Status).
const (
	PaneStatusNone      = 0 // not a Claude pane
	PaneStatusAgentTurn = 1
	PaneStatusUserTurn  = 2 // "user-turn"
	PaneStatusLater     = 3 // later pane
)

type minimapPane struct {
	PaneID      string
	SessionName string
	WindowIndex int
	WindowName  string
	PaneTitle   string
	PaneIndex   int
	// Absolute pixel coords within the window
	Left, Top, Width, Height  int
	WindowWidth, WindowHeight int
	Status                    int // PaneStatus* constant
	IsSelected                bool
	AvatarColorIdx            int // avatar color index (Claude panes only)
	AvatarAnimalIdx           int // avatar animal glyph index (Claude panes only)
}

// PaneAvatarInfo bundles avatar indices for a Claude pane.
type PaneAvatarInfo struct {
	ColorIdx  int
	AnimalIdx int
}

// MinimapPaneInfo carries the info needed to switch to a minimap pane.
type MinimapPaneInfo struct {
	PaneID      string
	SessionName string
	WindowIndex int
	PaneIndex   int
	PaneTitle   string
	IsClaude    bool
}

type windowGroup struct {
	Index int
	Name  string
	Panes []minimapPane
	// Max dimensions of the window
	Width, Height int
}

var (
	minimapBorderStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorBorder).
				PaddingLeft(1).
				PaddingRight(1)

	minimapSessionStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true)

	minimapTabStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	minimapPaneDimStyle = lipgloss.NewStyle().
				Foreground(ColorBorder)

	minimapPaneWorkingStyle = lipgloss.NewStyle().
				Foreground(ColorWorking)

	minimapPaneDoneStyle = lipgloss.NewStyle().
				Foreground(ColorDone)

	minimapPaneLaterStyle = lipgloss.NewStyle().
				Foreground(ColorLater)

	minimapPaneSelectedStyle = lipgloss.NewStyle().
					Foreground(ColorAccent).
					Bold(true)
)

const DefaultMinimapWindowCols = 40
const collapsedWindowCols = 8 // narrower column for single-pane windows
const collapsedGridH = 3      // minimal vertical rows for collapsed single-pane windows

func NewMinimapModel() MinimapModel {
	return MinimapModel{windowCols: DefaultMinimapWindowCols}
}

func (m *MinimapModel) SetSize(_, h int) {
	m.height = h
}

// SetCollapse enables/disables collapsing single-pane windows to narrower columns.
func (m *MinimapModel) SetCollapse(on bool) {
	m.collapse = on
}

// SetWindowCols sets the per-window column width for the minimap grid.
func (m *MinimapModel) SetWindowCols(cols int) {
	if cols < 15 {
		cols = 15
	}
	m.windowCols = cols
}

// SetData configures the minimap. paneStatuses maps paneID → PaneStatus* constant
// for Claude panes; panes not in the map are treated as non-Claude (PaneStatusNone).
// paneAvatars maps paneID → avatar info for identity coloring.
func (m *MinimapModel) SetData(geom []tmux.PaneGeometry, paneStatuses map[string]int, paneAvatars map[string]PaneAvatarInfo, selectedPaneID, sessionName string) {
	m.sessionName = sessionName
	m.selectedPaneID = selectedPaneID
	m.panes = make([]minimapPane, len(geom))
	for i, g := range geom {
		av := paneAvatars[g.PaneID]
		m.panes[i] = minimapPane{
			PaneID:          g.PaneID,
			SessionName:     g.SessionName,
			WindowIndex:     g.WindowIndex,
			WindowName:      g.WindowName,
			PaneTitle:       g.PaneTitle,
			PaneIndex:       g.PaneIndex,
			Left:            g.Left,
			Top:             g.Top,
			Width:           g.Width,
			Height:          g.Height,
			WindowWidth:     g.WindowWidth,
			WindowHeight:    g.WindowHeight,
			Status:          paneStatuses[g.PaneID],
			IsSelected:      g.PaneID == selectedPaneID,
			AvatarColorIdx:  av.ColorIdx,
			AvatarAnimalIdx: av.AnimalIdx,
		}
	}
}

func (m *MinimapModel) SetSpinnerView(s string) {
	m.spinnerView = s
}

func (m *MinimapModel) UpdateSelected(paneID string) {
	m.selectedPaneID = paneID
	for i := range m.panes {
		m.panes[i].IsSelected = m.panes[i].PaneID == paneID
	}
}

func (m MinimapModel) SelectedPaneID() string {
	return m.selectedPaneID
}

// SelectedPaneInfo returns full switch info for the currently selected pane.
func (m MinimapModel) SelectedPaneInfo() (MinimapPaneInfo, bool) {
	for _, p := range m.panes {
		if p.PaneID == m.selectedPaneID {
			return MinimapPaneInfo{
				PaneID:      p.PaneID,
				SessionName: p.SessionName,
				WindowIndex: p.WindowIndex,
				PaneIndex:   p.PaneIndex,
				PaneTitle:   p.PaneTitle,
				IsClaude:    p.Status != PaneStatusNone,
			}, true
		}
	}
	return MinimapPaneInfo{}, false
}

// DebugInfo returns a debug string showing grid rects used for navigation.
func (m MinimapModel) DebugInfo() string {
	rects := m.computeGridRects()
	if len(rects) == 0 {
		return fmt.Sprintf("sel=%s height=%d (no rects)", m.selectedPaneID, m.height)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("sel=%s height=%d", m.selectedPaneID, m.height))
	if m.LastNavDebug != "" {
		lines = append(lines, m.LastNavDebug)
	}
	for _, r := range rects {
		marker := " "
		if r.PaneID == m.selectedPaneID {
			marker = ">"
		}
		lines = append(lines, fmt.Sprintf("%s%s X[%d..%d] Y[%d..%d]",
			marker, r.PaneID, r.X1, r.X2, r.Y1, r.Y2))
	}
	return strings.Join(lines, "\n")
}

func (m *MinimapModel) UpdateStatus(paneStatuses map[string]int) {
	for i := range m.panes {
		m.panes[i].Status = paneStatuses[m.panes[i].PaneID]
	}
}

func (m MinimapModel) View() string {
	return m.view(0)
}

// ViewDocked renders the minimap with its border stretched to the given width.
// Content that overflows the border is truncated.
func (m MinimapModel) ViewDocked(outerWidth int) string {
	return m.view(outerWidth)
}
