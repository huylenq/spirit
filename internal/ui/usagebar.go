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

var labelStyle = lipgloss.NewStyle().Foreground(ColorMuted)

// UsageBarModel renders a thin progress bar showing account-level session usage.
type UsageBarModel struct {
	sessionPct int              // cached separately for ripple delta detection
	stats      *claude.UsageStats // full stats for display
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
func (m *UsageBarModel) Resets() string {
	if m.stats == nil {
		return ""
	}
	return m.stats.SessionResets
}

// RippleActive returns whether the ripple animation is running (for debug display).
func (m *UsageBarModel) RippleActive() bool { return m.rippleActive }

// Stats returns the raw usage stats (for debug display). May be nil if no data.
func (m *UsageBarModel) Stats() *claude.UsageStats { return m.stats }

// SetUsage updates the bar with new usage data.
// Returns a tea.Cmd to start the ripple animation if the fill changed visually.
func (m *UsageBarModel) SetUsage(stats *claude.UsageStats) tea.Cmd {
	if stats == nil {
		return nil
	}
	oldPct := m.sessionPct
	m.sessionPct = stats.SessionPct
	m.stats = stats
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

// TopBorderView renders the usage bar as the top border of the TUI frame.
// When corners is true, uses ╭/╮ with thin ─ transitions for a bordered frame.
// When corners is false, renders edge-to-edge ━ bars (for fullscreen).
// When no data is available, renders a plain border using BorderCharStyle.
func (m *UsageBarModel) TopBorderView(width int, corners bool) string {
	if width < 2 {
		return ""
	}

	// The thick ━ region gets usage bar coloring.
	// With corners: positions 2..width-3 (leaving room for ╭─ and ─╮).
	// Without corners: the entire width.
	var thickStart, thickEnd int
	if corners {
		thickStart = 2
		thickEnd = width - 2
	} else {
		thickStart = 0
		thickEnd = width
	}
	thickWidth := thickEnd - thickStart
	filledChars := 0
	if m.hasData && thickWidth > 0 {
		filledChars = thickStart + thickWidth*m.sessionPct/100
	}
	weeklyFilledChars := 0
	weekAllPct := 0
	if m.hasData && m.stats != nil {
		weekAllPct = m.stats.WeekAllPct
	}
	if m.hasData && thickWidth > 0 && weekAllPct > 0 {
		weeklyFilledChars = thickStart + thickWidth*weekAllPct/100
	}
	showPills := corners && m.hasData && weekAllPct > 0

	var sb strings.Builder
	for i := 0; i < width; i++ {
		glyph := "━"
		isPillLeft := showPills && i == 1       // thickStart-1
		isPillRight := showPills && i == width-2 // thickEnd

		if corners {
			switch i {
			case 0:
				glyph = "╭"
			case 1:
				if showPills {
					glyph = "▐"
				} else {
					glyph = "─"
				}
			case width - 2:
				if showPills {
					glyph = "▌"
				} else {
					glyph = "─"
				}
			case width - 1:
				glyph = "╮"
			}
		}

		isThick := i >= thickStart && i < thickEnd
		inWeekly := isThick && weeklyFilledChars > 0 && i < weeklyFilledChars
		inSession := isThick && m.hasData && i < filledChars

		if inSession {
			t := float64(i-thickStart) / float64(max(filledChars-thickStart, 1))
			c := blendHex("#3d2b00", "#8a5a00", t)
			if m.rippleActive {
				rippleCenter := filledChars - rippleWidth + (m.rippleFrame * (rippleWidth + 3) / rippleFrames)
				dist := intAbs(i - rippleCenter)
				if dist < rippleWidth {
					intensity := 1.0 - float64(dist)/float64(rippleWidth)
					c = blendHex(c, "#93c5fd", intensity*0.8)
				}
			}
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(c))
			if inWeekly {
				style = style.Background(lipgloss.Color("#0d2030"))
			}
			sb.WriteString(style.Render(glyph))
		} else if inWeekly {
			sb.WriteString(lipgloss.NewStyle().Background(lipgloss.Color("#0d2030")).Render(glyph))
		} else if isPillLeft || isPillRight {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#2a4a5a")).Render(glyph))
		} else {
			sb.WriteString(BorderCharStyle.Render(glyph))
		}
	}

	return sb.String()
}

// LabelView returns the styled usage label (without positioning).
func (m *UsageBarModel) LabelView() string {
	if !m.hasData {
		return ""
	}

	trimTZ := func(s string) string {
		if i := strings.Index(s, " ("); i >= 0 {
			return s[:i]
		}
		return s
	}

	s := m.stats
	parts := []string{fmt.Sprintf("session %d%%", m.sessionPct)}
	if s.SessionResets != "" {
		parts[0] += " resets " + trimTZ(s.SessionResets)
	}
	if s.WeekAllPct > 0 || s.WeekAllResets != "" {
		seg := fmt.Sprintf("week %d%%", s.WeekAllPct)
		if s.WeekSonnetPct > 0 {
			seg += fmt.Sprintf(" (sonnet %d%%)", s.WeekSonnetPct)
		}
		if s.WeekAllResets != "" {
			seg += " resets " + trimTZ(s.WeekAllResets)
		}
		if s.WeekSonnetResets != "" && s.WeekSonnetResets != s.WeekAllResets {
			seg += " · sonnet resets " + trimTZ(s.WeekSonnetResets)
		}
		parts = append(parts, seg)
	}

	return labelStyle.Render(strings.Join(parts, " · "))
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
