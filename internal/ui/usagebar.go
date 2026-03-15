package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// tzCache caches loaded *time.Location by name to avoid repeated disk reads from time.LoadLocation.
var tzCache sync.Map // map[string]*time.Location

const (
	rippleFrames   = 15
	rippleInterval = 60 * time.Millisecond
	rippleWidth    = 5 // characters wide for the bright wave

	colorWeeklyBg   = "#1e1608" // dark amber background for weekly fill
	colorWeeklyTail = "#3d2b00" // muted amber tail for weekly gradient
)

// weekdayPrefixes maps 3-letter lowercase abbreviation → time.Weekday.
// Slice (not map) for deterministic iteration order.
var weekdayPrefixes = []struct {
	prefix  string
	weekday time.Weekday
}{
	{"sun", time.Sunday},
	{"mon", time.Monday},
	{"tue", time.Tuesday},
	{"wed", time.Wednesday},
	{"thu", time.Thursday},
	{"fri", time.Friday},
	{"sat", time.Saturday},
}

// Pre-allocated layout slices to avoid per-call allocation in formatUntil.
var (
	dateTimeLayouts = []string{"Jan 2 3:04pm", "Jan 2 3pm", "Jan 2"}
	timeOnlyLayouts = []string{"3:04pm", "3pm", "15:04"}
)

var (
	labelStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	// Pre-built fixed styles for the usage bar inner loop (avoids per-char allocations)
	pillCapStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorWeeklyBg))
	weeklyFillStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorWeeklyBg)).
			Foreground(ColorBorder)
)

// UsageBarModel renders a thin progress bar showing account-level session usage.
type UsageBarModel struct {
	sessionPct int                // cached separately for ripple delta detection
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
	showPillLeft := corners && m.hasData && weekAllPct > 0
	showPillRight := m.hasData && weekAllPct > 0 && weekAllPct < 100

	var sb strings.Builder
	for i := 0; i < width; i++ {
		glyph := "━"
		isPillLeft := showPillLeft && i == 1
		isPillRight := showPillRight && i == weeklyFilledChars

		if corners {
			switch i {
			case 0:
				glyph = "╭"
			case 1:
				glyph = "─"
			case width - 2:
				glyph = "─"
			case width - 1:
				glyph = "╮"
			}
		}

		// Pill caps take priority — they sit at the boundary of the fill region
		if isPillLeft || isPillRight {
			var cap string
			if isPillLeft {
				cap = IconPillLeft
				sb.WriteString(pillCapStyle.Render(cap))
			} else {
				cap = IconPillRight
				// Right cap fg must match the gradient at the last filled position
				capT := float64(weeklyFilledChars-1-thickStart) / float64(max(weeklyFilledChars-thickStart, 1))
				capT = cubicEaseIn(capT)
				capColor := blendHex(colorWeeklyBg, colorWeeklyTail, capT)
				sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(capColor)).Render(cap))
			}
			continue
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
				style = style.Background(lipgloss.Color(colorWeeklyBg))
			}
			sb.WriteString(style.Render(glyph))
		} else if inWeekly {
			t := float64(i-thickStart) / float64(max(weeklyFilledChars-thickStart, 1))
			t = cubicEaseIn(t) // stays near bg, accelerates sharply at tail
			bg := blendHex(colorWeeklyBg, colorWeeklyTail, t)
			style := lipgloss.NewStyle().
				Background(lipgloss.Color(bg)).
				Foreground(ColorBorder)
			sb.WriteString(style.Render(glyph))
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

	s := m.stats
	parts := []string{fmt.Sprintf("session %d%%", m.sessionPct)}
	if s.SessionResets != "" {
		parts[0] += " " + formatUntil(s.SessionResets)
	}
	if s.WeekAllPct > 0 || s.WeekAllResets != "" {
		seg := fmt.Sprintf("week %d%%", s.WeekAllPct)
		if s.WeekSonnetPct > 0 {
			seg += fmt.Sprintf(" (sonnet %d%%)", s.WeekSonnetPct)
		}
		if s.WeekAllResets != "" {
			seg += " " + formatUntil(s.WeekAllResets)
		}
		if s.WeekSonnetResets != "" && s.WeekSonnetResets != s.WeekAllResets {
			seg += " · sonnet " + formatUntil(s.WeekSonnetResets)
		}
		parts = append(parts, seg)
	}

	return labelStyle.Render(strings.Join(parts, " · "))
}

// formatUntil converts a reset time string like "6pm (Asia/Saigon)", "Mon 6pm (UTC)",
// or "Mar 14 (Asia/Saigon)" into a relative duration like "resets in 2h 30m".
// Falls back to "resets <time>" on parse failure.
func formatUntil(resetStr string) string {
	now := time.Now()

	// Extract timezone from parentheses
	loc := time.Local
	timeStr := strings.TrimSpace(resetStr)
	if i := strings.Index(resetStr, "("); i >= 0 {
		timeStr = strings.TrimSpace(resetStr[:i])
		if j := strings.Index(resetStr[i:], ")"); j >= 0 {
			tzName := resetStr[i+1 : i+j]
			if v, ok := tzCache.Load(tzName); ok {
				loc = v.(*time.Location)
			} else if l, err := time.LoadLocation(tzName); err == nil {
				tzCache.Store(tzName, l)
				loc = l
			}
		}
	}

	nowInLoc := now.In(loc)

	// Normalize "at" separator: "Mar 14 at 3pm" → "Mar 14 3pm"
	timeStr = strings.Replace(timeStr, " at ", " ", 1)

	// Try date-based formats first: "Mar 14", "Mar 14 6pm", "Mar 14 6:30pm"
	for _, layout := range dateTimeLayouts {
		if t, err := time.Parse(layout, timeStr); err == nil {
			reset := time.Date(nowInLoc.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			// If the date is in the past, assume next year
			if !reset.After(now) {
				reset = reset.AddDate(1, 0, 0)
			}
			return IconClock + " " + formatCompactDuration(reset.Sub(now), true)
		}
	}

	// Try day-of-week prefix: "Mon 6pm", "Mon 6:30pm", "Mon"
	lower := strings.ToLower(timeStr)
	for _, wp := range weekdayPrefixes {
		if !strings.HasPrefix(lower, wp.prefix) {
			continue
		}
		rest := strings.TrimSpace(lower[len(wp.prefix):])

		// Parse optional time after weekday
		hour, min := 0, 0
		if rest != "" {
			timeParsed := false
			for _, layout := range timeOnlyLayouts {
				if t, err := time.Parse(layout, rest); err == nil {
					hour, min = t.Hour(), t.Minute()
					timeParsed = true
					break
				}
			}
			if !timeParsed {
				break // weekday prefix matched but time didn't parse — fall through
			}
		}

		reset := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), hour, min, 0, 0, loc)
		daysAhead := (int(wp.weekday) - int(nowInLoc.Weekday()) + 7) % 7
		if daysAhead == 0 && !reset.After(now) {
			daysAhead = 7
		}
		reset = reset.Add(time.Duration(daysAhead) * 24 * time.Hour)
		return IconClock + " " + formatCompactDuration(reset.Sub(now), true)
	}

	// Try time-only: "6pm", "6:30pm", "18:00"
	for _, layout := range []string{"3:04pm", "3pm", "15:04"} {
		if t, err := time.Parse(layout, strings.ToLower(timeStr)); err == nil {
			reset := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if !reset.After(now) {
				reset = reset.Add(24 * time.Hour)
			}
			return IconClock + " " + formatCompactDuration(reset.Sub(now), true)
		}
	}

	return IconClock + " " + timeStr
}

// cubicEaseIn applies a cubic ease-in curve: slow start, sharp acceleration.
func cubicEaseIn(t float64) float64 { return t * t * t }

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
