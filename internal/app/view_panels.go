package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

func (m Model) renderEffectsPanel() string {
	var lines []string
	lines = append(lines, ui.DebugTitleStyle.Render("EFFECTS"))

	if len(m.globalEffects) == 0 {
		lines = append(lines, ui.ItemDetailStyle.Render("(no handled effects)"))
	} else {
		for _, ev := range m.globalEffects {
			avatar := ui.AvatarStyle(ev.ColorIdx).Render(ui.AvatarGlyph(ev.AnimalIdx))
			effect := ev.Effect
			suffix := ""
			if strings.HasSuffix(effect, claude.HookEffectDedupSuffix) {
				effect = strings.TrimSuffix(effect, claude.HookEffectDedupSuffix)
				suffix += ui.ItemDetailStyle.Render(claude.HookEffectDedupSuffix)
			}
			if ev.Count > 1 {
				suffix += ui.ItemDetailStyle.Render(fmt.Sprintf(" ×%d", ev.Count))
			}
			lines = append(lines,
				avatar+" "+ui.ItemDetailStyle.Render(ev.Time+" "+ev.HookType+": ")+ui.TranscriptMsgStyle.Render(effect)+suffix)
		}
	}

	return ui.DebugOverlayStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderSessionPanel() string {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return ""
	}

	line := func(label, v string) string {
		if v == "" {
			v = "(empty)"
		}
		return ui.ItemDetailStyle.Render(label+": ") + ui.TranscriptMsgStyle.Render(v)
	}

	var lines []string
	lines = append(lines, ui.DebugTitleStyle.Render("SESSION"))
	lines = append(lines, line("PaneID", s.PaneID))
	lines = append(lines, line("SessionID", s.SessionID))
	lines = append(lines, line("Status", s.Status.String()))
	lines = append(lines, line("CustomTitle", s.CustomTitle))
	lines = append(lines, line("Headline", s.Headline))
	lines = append(lines, line("FirstMsg", debugTruncate(s.FirstMessage, 40)))
	lines = append(lines, line("LastUserMsg", debugTruncate(s.LastUserMessage, 40)))
	lines = append(lines, line("PermMode", s.PermissionMode))
	lines = append(lines, line("Project", s.Project))
	lines = append(lines, line("CWD", s.CWD))
	lines = append(lines, line("GitBranch", s.GitBranch))
	lines = append(lines, line("SynthPending", fmt.Sprintf("%v", s.SynthesizePending)))
	lines = append(lines, line("HasOverlap", fmt.Sprintf("%v", s.HasOverlap)))

	return ui.DebugOverlayStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderUsageDebugPanel() string {
	line := func(label, v string) string {
		if v == "" {
			v = "(empty)"
		}
		return ui.ItemDetailStyle.Render(label+": ") + ui.TranscriptMsgStyle.Render(v)
	}

	var lines []string
	lines = append(lines, ui.DebugTitleStyle.Render("USAGE BAR"))
	if m.usageBar.HasData() {
		lines = append(lines, line("RippleActive", fmt.Sprintf("%v", m.usageBar.RippleActive())))
		if s := m.usageBar.Stats(); s != nil {
			lines = append(lines, line("SessionPct", fmt.Sprintf("%d%%", s.SessionPct)))
			lines = append(lines, line("SessionResets", s.SessionResets))
			lines = append(lines, line("WeekAllPct", fmt.Sprintf("%d%%", s.WeekAllPct)))
			lines = append(lines, line("WeekAllResets", s.WeekAllResets))
			lines = append(lines, line("WeekSonnetPct", fmt.Sprintf("%d%%", s.WeekSonnetPct)))
			lines = append(lines, line("WeekSonnetResets", s.WeekSonnetResets))
		}
	} else {
		lines = append(lines, ui.ItemDetailStyle.Render("(no usage data yet)"))
	}

	return ui.DebugOverlayStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderSynthesizeDebugPanel() string {
	s, ok := m.sidebar.SelectedItem()
	if !ok || s.SessionID == "" {
		return ""
	}

	line := func(label, v string) string {
		if v == "" {
			v = "(empty)"
		}
		return ui.ItemDetailStyle.Render(label+": ") + ui.TranscriptMsgStyle.Render(v)
	}

	cached := claude.ReadCachedSummary(s.SessionID)
	sMod, tMod, fresh := claude.SummaryCacheInfo(s.SessionID)

	var lines []string
	lines = append(lines, ui.DebugTitleStyle.Render("SYNTHESIZE CACHE"))
	if cached != nil {
		const jsonWrap = 50
		data, _ := json.MarshalIndent(cached, "", "  ")
		for _, jsonLine := range strings.Split(string(data), "\n") {
			for len(jsonLine) > jsonWrap {
				lines = append(lines, ui.HighlightJSON(jsonLine[:jsonWrap]))
				jsonLine = "    " + jsonLine[jsonWrap:] // indent continuation
			}
			lines = append(lines, ui.HighlightJSON(jsonLine))
		}
	} else {
		lines = append(lines, ui.ItemDetailStyle.Render("(no cached synthesize)"))
	}
	freshStr := "stale"
	if fresh {
		freshStr = "fresh"
	}
	if sMod == "" {
		freshStr = "n/a"
	}
	lines = append(lines, line("SynthMod", sMod))
	lines = append(lines, line("TranscriptMod", tMod))
	lines = append(lines, line("CacheFresh", freshStr))

	// Auto-synthesis pref
	autoSynth := loadPrefString("autoSynthesize", "on")
	if autoSynth == "false" {
		autoSynth = "off"
	} else {
		autoSynth = "on"
	}
	lines = append(lines, line("AutoSynth", autoSynth))

	// Digest cache
	digest := claude.ReadCachedDigest()
	if digest != nil {
		lines = append(lines, line("DigestAt", digest.GeneratedAt.Format("15:04:05")))
		lines = append(lines, line("DigestSessions", fmt.Sprintf("%d", digest.SessionCount)))
		lines = append(lines, line("DigestFiles", fmt.Sprintf("%d", digest.FileCount)))
		summary := debugTruncate(digest.Summary, 50)
		lines = append(lines, line("Digest", summary))
	} else {
		lines = append(lines, line("Digest", "(none)"))
	}

	// Synthesizer usage stats
	stats := claude.ReadSynthStats()
	fmtPeriod := func(p claude.SynthPeriod) string {
		if p.Calls == 0 {
			return "—"
		}
		if p.Words < 1000 {
			return fmt.Sprintf("%d calls / %d words", p.Calls, p.Words)
		}
		return fmt.Sprintf("%d calls / %dk words", p.Calls, p.Words/1000)
	}
	lines = append(lines, line("UsageToday", fmtPeriod(stats.Today)))
	lines = append(lines, line("Usage7d", fmtPeriod(stats.Week)))
	lines = append(lines, line("Usage30d", fmtPeriod(stats.Month)))

	return ui.DebugOverlayStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderJumpTrailPanel() string {
	line := func(label, v string) string {
		if v == "" {
			v = "(empty)"
		}
		return ui.ItemDetailStyle.Render(label+": ") + ui.TranscriptMsgStyle.Render(v)
	}

	sessionByPane := make(map[string]claude.ClaudeSession)
	for _, sess := range m.sessions {
		sessionByPane[sess.PaneID] = sess
	}

	var lines []string
	lines = append(lines, ui.DebugTitleStyle.Render("JUMP TRAIL"))
	lines = append(lines, line("Cursor", fmt.Sprintf("%d/%d", m.jumpCursor, len(m.jumpTrail))))
	for i, pid := range m.jumpTrail {
		marker := ui.ItemDetailStyle.Render("  ")
		if i == m.jumpCursor {
			marker = ui.ItemDetailStyle.Render("> ")
		}
		var avatar string
		if sess, ok := sessionByPane[pid]; ok {
			avatar = ui.AvatarStyle(sess.AvatarColorIdx).Render(ui.AvatarGlyph(sess.AvatarAnimalIdx))
		} else {
			avatar = ui.ItemDetailStyle.Render("?")
		}
		lines = append(lines, marker+ui.ItemDetailStyle.Render(fmt.Sprintf("[%d] ", i))+avatar)
	}
	if m.jumpCursor >= len(m.jumpTrail) {
		lines = append(lines, ui.ItemDetailStyle.Render("> (head)"))
	}

	return ui.DebugOverlayStyle.Render(strings.Join(lines, "\n"))
}

func debugTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// renderQueueSection renders the queue items below the detail panel.
// Always visible when items are pending; interactive when in StateQueueRelay.
func (m Model) renderQueueSection(s claude.ClaudeSession, width int) string {
	items := s.QueuePending
	inQueueMode := m.state == StateQueueRelay
	innerWidth := width - 2 // padding

	var lines []string

	// Header
	header := fmt.Sprintf("❮ queued (%d)", len(items))
	lines = append(lines, ui.QueuePromptStyle.Render(header))

	// Items (capped at ~30% of preview height, scrollable later if needed)
	maxItems := max((m.height-6)*30/100, 3)
	for i, msg := range items {
		if i >= maxItems {
			lines = append(lines, ui.ItemDetailStyle.Render(fmt.Sprintf("  …+%d more", len(items)-maxItems)))
			break
		}
		prefix := fmt.Sprintf("  %d. ", i+1)
		maxMsgWidth := innerWidth - lipgloss.Width(prefix)
		truncated := ansi.Truncate(msg, maxMsgWidth, "…")
		if inQueueMode && i == m.queueCursor {
			// Highlighted item
			lines = append(lines, ui.SelectedBgStyle.Render(prefix+truncated+strings.Repeat(" ", max(innerWidth-lipgloss.Width(prefix+truncated), 0))))
		} else {
			lines = append(lines, ui.ItemDetailStyle.Render(prefix+truncated))
		}
	}

	return strings.Join(lines, "\n")
}
