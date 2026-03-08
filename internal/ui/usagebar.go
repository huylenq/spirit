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

// View renders the usage bar at the given width. Returns "" if no data.
func (m *UsageBarModel) View(width int) string {
	if !m.hasData || width < 10 {
		return ""
	}

	// Right-side label: " 38% · resets 6pm"
	label := fmt.Sprintf(" %d%%", m.sessionPct)
	if m.resets != "" {
		label += " · resets " + m.resets
	}
	labelLen := lipgloss.Width(label)

	barWidth := width - labelLen
	if barWidth < 5 {
		barWidth = width
		label = ""
	}

	filledChars := barWidth * m.sessionPct / 100

	var sb strings.Builder
	sb.Grow(width * 4)

	for i := 0; i < barWidth; i++ {
		if i < filledChars {
			// Gradient from dark to bright across the filled portion
			t := float64(i) / float64(max(filledChars, 1))
			baseColor := blendHex("#2a4a7f", "#60a5fa", t)

			// Ripple effect: bright wave traveling from tail toward end
			if m.rippleActive {
				rippleCenter := filledChars - rippleWidth + (m.rippleFrame * (rippleWidth + 3) / rippleFrames)
				dist := intAbs(i - rippleCenter)
				if dist < rippleWidth {
					intensity := 1.0 - float64(dist)/float64(rippleWidth)
					baseColor = blendHex(baseColor, "#93c5fd", intensity*0.8)
				}
			}

			style := lipgloss.NewStyle().Foreground(lipgloss.Color(baseColor))
			sb.WriteString(style.Render("█"))
		} else {
			style := lipgloss.NewStyle().Foreground(ColorUsageBarEmpty)
			sb.WriteString(style.Render("░"))
		}
	}

	if label != "" {
		labelStyle := lipgloss.NewStyle().Foreground(ColorUsageBarText)
		sb.WriteString(labelStyle.Render(label))
	}

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
