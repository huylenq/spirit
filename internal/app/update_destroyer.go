package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/ui/destroyer"
)

// DestroyerTickMsg advances the destroyer animation.
type DestroyerTickMsg struct{}

// DestroyerAutoStartMsg is fired after AllQuiet has been active long enough.
type DestroyerAutoStartMsg struct{}

const destroyerAutoDelay = 20 * time.Second

func scheduleDestroyerAutoStart() tea.Cmd {
	return tea.Tick(destroyerAutoDelay, func(time.Time) tea.Msg {
		return DestroyerAutoStartMsg{}
	})
}

func tickDestroyer() tea.Cmd {
	return tea.Tick(destroyer.Interval, func(time.Time) tea.Msg {
		return DestroyerTickMsg{}
	})
}

// execDestroyer activates the session destroyer easter egg.
func (m *Model) execDestroyer() (Model, tea.Cmd) {
	if m.state == StateDestroyer {
		return *m, nil // already active
	}

	// Capture current TUI output as the snapshot to decompose
	snapshot := m.viewInner()
	innerWidth := m.innerWidth()
	// Subtract 1 for the divider line between content and footer
	destroyerH := m.contentHeight() - 1

	d := destroyer.New(snapshot, innerWidth, destroyerH)
	m.destroyer = &d
	m.state = StateDestroyer
	return *m, tickDestroyer()
}

// handleKeyDestroyer processes keys while destroyer is active.
func (m Model) handleKeyDestroyer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.destroyer == nil {
		m.state = StateNormal
		return m, nil
	}

	switch msg.String() {
	case "esc":
		if m.destroyer.Phase == destroyer.PhaseRebuild {
			// Already rebuilding — exit immediately
			m.destroyer = nil
			m.state = StateNormal
			return m, nil
		}
		// Start rebuild animation
		m.destroyer.StartRebuild()
		return m, tickDestroyer()

	case "tab":
		m.destroyer.CycleTool()
		return m, nil

	case "1":
		m.destroyer.SetTool(destroyer.ToolHammer)
		return m, nil
	case "2":
		m.destroyer.SetTool(destroyer.ToolBomb)
		return m, nil
	case "3":
		m.destroyer.SetTool(destroyer.ToolBlackHole)
		return m, nil
	case "4":
		m.destroyer.SetTool(destroyer.ToolShake)
		return m, nil

	case "q":
		// Quick exit, no rebuild
		m.destroyer = nil
		m.state = StateNormal
		return m, nil
	}

	return m, nil
}

// handleMouseDestroyer processes mouse events in destroyer mode.
func (m Model) handleMouseDestroyer(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.destroyer == nil || m.destroyer.Phase != destroyer.PhaseDestroy {
		return m, nil
	}

	// Convert terminal coords to content-local coords
	x, y := m.destroyerLocalCoords(msg.X, msg.Y)

	// Track cursor position for all mouse events (draws tool cursor)
	m.destroyer.MouseMove(x, y)

	switch msg.Button {
	case tea.MouseButtonLeft:
		switch msg.Action {
		case tea.MouseActionPress:
			m.destroyer.Click(x, y, true) // shake on press
		case tea.MouseActionMotion:
			m.destroyer.Click(x, y, false) // no shake on drag
		case tea.MouseActionRelease:
			m.destroyer.MouseRelease()
		}
	}

	return m, nil
}

// destroyerLocalCoords converts terminal coordinates to destroyer-local coordinates.
func (m Model) destroyerLocalCoords(termX, termY int) (int, int) {
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1
	}
	x := termX - colOffset
	y := termY - contentStartRow
	return x, y
}
