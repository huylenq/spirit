package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

const (
	rippleFrames   = 15
	rippleInterval = 60 * time.Millisecond
	rippleWidth    = 5 // characters wide for the bright wave
)

// UsageBarModel renders a thin progress bar showing account-level session usage.
type UsageBarModel struct {
	sessionPct int    // 0-100
	resets     string // e.g. "6pm (Asia/Saigon)"
	hasData    bool

	// Ripple animation
	rippleActive bool
	rippleFrame  int
}

// HasData returns true if usage data has been received.
func (m *UsageBarModel) HasData() bool {
	return m.hasData
}

// SessionPct returns the current session usage percentage (for debug display).
func (m *UsageBarModel) SessionPct() int { return m.sessionPct }

// Resets returns the reset time string (for debug display).
func (m *UsageBarModel) Resets() string { return m.resets }

// RippleActive returns whether the ripple animation is running (for debug display).
func (m *UsageBarModel) RippleActive() bool { return m.rippleActive }

// SetUsage updates the bar with new usage data.
// Returns a tea.Cmd to start the ripple animation if the fill changed visually.
func (m *UsageBarModel) SetUsage(stats *claude.UsageStats) tea.Cmd {
	if stats == nil {
		return nil
	}
	oldPct := m.sessionPct
	m.sessionPct = stats.SessionPct
	m.resets = stats.SessionResets
	wasEmpty := !m.hasData
	m.hasData = true

	// Trigger ripple on visible change (>= 2%), but not on first load
	if !wasEmpty && intAbs(m.sessionPct-oldPct) >= 2 && !m.rippleActive {
		m.rippleActive = true
		m.rippleFrame = 0
		return tickUsageBar()
	}
	return nil
}

// Tick advances the ripple animation by one frame.
func (m *UsageBarModel) Tick() tea.Cmd {
	if !m.rippleActive {
		return nil
	}
	m.rippleFrame++
	if m.rippleFrame >= rippleFrames {
		m.rippleActive = false
		m.rippleFrame = 0
		return nil
	}
	return tickUsageBar()
}

// UsageBarTickMsg advances the usage bar ripple animation.
type UsageBarTickMsg struct{}

func tickUsageBar() tea.Cmd {
	return tea.Tick(rippleInterval, func(time.Time) tea.Msg {
		return UsageBarTickMsg{}
	})
}

// InlineView renders a compact usage bar for the header line.
// Uses ▀ (upper half block) for filled — visually thinner than full blocks.
func (m *UsageBarModel) InlineView(availWidth int) string {
	if !m.hasData {
		return ""
	}

	label := fmt.Sprintf("session %d%%", m.sessionPct)
	if m.resets != "" {
		label += " · resets " + m.resets
	}
	labelW := len(label) + 1 // +1 for space before label

	barWidth := availWidth - labelW
	if barWidth < 8 {
		barWidth = 8
	}

	filledChars := barWidth * m.sessionPct / 100

	var sb strings.Builder
	for i := 0; i < barWidth; i++ {
		if i < filledChars {
			t := float64(i) / float64(max(filledChars, 1))
			c := blendHex("#3d2b00", "#8a5a00", t)

			if m.rippleActive {
				rippleCenter := filledChars - rippleWidth + (m.rippleFrame * (rippleWidth + 3) / rippleFrames)
				dist := intAbs(i - rippleCenter)
				if dist < rippleWidth {
					intensity := 1.0 - float64(dist)/float64(rippleWidth)
					c = blendHex(c, "#93c5fd", intensity*0.8)
				}
			}

			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("▔"))
		} else {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#e0e0e0", Dark: "#1a1a1a"}).Render("▔"))
		}
	}

	sb.WriteString(" ")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render(label))
	return sb.String()
}

// blendHex linearly interpolates between two hex colors.
func blendHex(from, to string, t float64) string {
	if t <= 0 {
		return from
	}
	if t >= 1 {
		return to
	}
	r1, g1, b1 := parseHexRGB(from)
	r2, g2, b2 := parseHexRGB(to)
	r := uint8(float64(r1) + t*(float64(r2)-float64(r1)))
	g := uint8(float64(g1) + t*(float64(g2)-float64(g1)))
	b := uint8(float64(b1) + t*(float64(b2)-float64(b1)))
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func parseHexRGB(hex string) (uint8, uint8, uint8) {
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return 0, 0, 0
	}
	var r, g, b uint8
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return r, g, b
}

func intAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
