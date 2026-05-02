package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/ui"
)

// DinoTickMsg is the per-frame tick for the empty work-queue dino game.
type DinoTickMsg = ui.DinoTickMsg

// initDino seeds the game with the persisted high score and registers the
// save callback. Safe to call multiple times — skips if already initialised.
func (m *Model) initDino() {
	game := m.workQueue.Dino()
	if game.OnNewHi != nil {
		return
	}
	game.SetHi(loadPrefInt("dinoHi", 0))
	game.OnNewHi = func(hi int) { savePrefInt("dinoHi", hi) }
}

// startDinoTickIfNeeded fires a single tick if the empty work-queue game is
// visible and not already ticking. Subsequent ticks are scheduled by the game
// itself in handleDinoTick.
func (m *Model) startDinoTickIfNeeded() tea.Cmd {
	if m.dinoTicking {
		return nil
	}
	if m.viewMode != ViewWorkQueue || !m.workQueue.IsGameVisible() {
		return nil
	}
	m.initDino()
	m.dinoTicking = true
	return tea.Tick(ui.DinoTickInterval, func(time.Time) tea.Msg { return DinoTickMsg{} })
}

// execToggleDinoGame is the gxd chord handler. Toggles the manual override
// that forces the dino game on top of the work queue strip. Gracefully
// switches to work queue view first if the user is in sidebar mode.
func (m *Model) execToggleDinoGame() (Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.viewMode != ViewWorkQueue {
		m.viewMode = ViewWorkQueue
		savePrefString("viewMode", m.viewMode)
		m.applyLayout()
		// Force the game on when entering via gxd from sidebar mode.
		if !m.workQueue.DinoForce() {
			m.workQueue.ToggleDinoForce()
		}
		cmds = append(cmds,
			m.reconcileWorkQueueSelection(),
			m.syncAllQuietAnim(),
			m.setFlash("dino game on", false, 2*time.Second),
		)
	} else {
		on := m.workQueue.ToggleDinoForce()
		flash := "dino game off"
		if on {
			flash = "dino game on"
		}
		cmds = append(cmds, m.setFlash(flash, false, 2*time.Second))
	}
	if cmd := m.startDinoTickIfNeeded(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return *m, tea.Batch(cmds...)
}

// handleDinoTick advances the empty-state dino game one frame and reschedules
// itself unless the game has been hidden (queue populated, view changed).
func (m Model) handleDinoTick() (tea.Model, tea.Cmd) {
	m.dinoTicking = false
	if m.viewMode != ViewWorkQueue || !m.workQueue.IsGameVisible() {
		return m, nil
	}
	game := m.workQueue.Dino()
	cmd := game.Tick()
	if cmd != nil {
		m.dinoTicking = true
	}
	return m, cmd
}
