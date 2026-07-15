package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/daemon"
)

// Durable reactivity TUI surface (W9): a statusline glyph, the `gR` chord that
// cycles enable → pause ⇄ resume, and palette entries for the explicit
// enable/pause/resume/disable verbs. Each verb shells out to `spirit reactive
// <verb>` — the single implementation of the pref + launchd load/unload lives
// in the subcommand, so the TUI never re-implements supervision. Enabling
// durable reactivity is a human act; that is exactly what this surface is.

// ReactiveStatusMsg carries the fetched durable-reactivity status.
type ReactiveStatusMsg struct {
	Status daemon.ReactiveStatusData
}

// ReactiveActionMsg reports the outcome of a `spirit reactive <verb>` run.
type ReactiveActionMsg struct {
	Verb string
	Err  error
}

// fetchReactiveStatus reads the durable-reactivity status from the daemon.
func (m Model) fetchReactiveStatus() tea.Cmd {
	return func() tea.Msg {
		st, err := m.client.ReactiveStatus()
		if err != nil {
			return ReactiveStatusMsg{} // leave prior state; a transient RPC miss isn't fatal
		}
		return ReactiveStatusMsg{Status: st}
	}
}

// runReactiveVerb execs `spirit reactive <verb>` and reports the outcome.
func runReactiveVerb(verb string) tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			return ReactiveActionMsg{Verb: verb, Err: err}
		}
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		cmd := exec.Command(exe, "reactive", verb)
		if err := cmd.Run(); err != nil {
			return ReactiveActionMsg{Verb: verb, Err: err}
		}
		return ReactiveActionMsg{Verb: verb}
	}
}

// execToggleReactive is the `gR` chord: cycle the durable-reactivity state.
// disabled → enable; enabled+running → pause; enabled+paused → resume. Disable
// is palette-only (an explicit, deliberate teardown).
func execToggleReactive(m *Model) (Model, tea.Cmd) {
	return *m, runReactiveVerb(nextReactiveVerb(m.reactiveStatus.Enabled, m.reactiveStatus.Paused))
}

// nextReactiveVerb picks the `gR` cycle step: off → enable; running → pause;
// paused → resume. (Disable is deliberately palette-only.)
func nextReactiveVerb(enabled, paused bool) string {
	switch {
	case !enabled:
		return "enable"
	case paused:
		return "resume"
	default:
		return "pause"
	}
}

func execReactiveEnable(m *Model) (Model, tea.Cmd)  { return *m, runReactiveVerb("enable") }
func execReactiveDisable(m *Model) (Model, tea.Cmd) { return *m, runReactiveVerb("disable") }
func execReactivePause(m *Model) (Model, tea.Cmd)   { return *m, runReactiveVerb("pause") }
func execReactiveResume(m *Model) (Model, tea.Cmd)  { return *m, runReactiveVerb("resume") }

// reactiveActionFlash renders the outcome of a reactive verb + refetches status.
func (m *Model) reactiveActionFlash(msg ReactiveActionMsg) tea.Cmd {
	if msg.Err != nil {
		return m.setFlash("reactive "+msg.Verb+": "+msg.Err.Error(), true, 4*time.Second)
	}
	return tea.Batch(
		m.setFlash("durable reactivity: "+msg.Verb, false, 2*time.Second),
		m.fetchReactiveStatus(),
	)
}

// reactiveIndicator renders the statusline durable-reactivity glyph: a steady ⚡
// when enabled, ⏸ when paused, empty when off (distinct from the ⚡N unseen
// attention badge).
func (m Model) reactiveIndicator() string {
	if !m.reactiveStatus.Enabled {
		return ""
	}
	if m.reactiveStatus.Paused {
		return reactiveIndicatorStyle.Render("⏸⚡")
	}
	return reactiveIndicatorStyle.Render("⚡durable")
}
