package daemon

import (
	"strconv"
	"strings"
	"time"
)

// Quiet hours (W9 §6). A local-time window HH:MM-HH:MM in the pref
// reactive.quiet_hours (empty = none). Enforced at the delivery boundary in the
// SHARED reactive path (deliverNotify / runRecommend) so TUI-active and durable
// modes obey identical rules: during quiet hours the OS notification is
// suppressed and inspect_and_recommend degrades to inbox — but the durable
// attention item + audit are always recorded (only the interruption is withheld).

// quietWindow is a parsed HH:MM-HH:MM window in minutes-since-midnight. A window
// that wraps midnight (start > end, e.g. 22:00-07:00) is honored.
type quietWindow struct {
	startMin int
	endMin   int
	ok       bool
}

// parseQuietHours parses "HH:MM-HH:MM". An empty or malformed value yields a
// zero window (ok=false → never quiet), fail-soft: a bad pref never silences.
func parseQuietHours(s string) quietWindow {
	s = strings.TrimSpace(s)
	if s == "" {
		return quietWindow{}
	}
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		return quietWindow{}
	}
	start, ok1 := parseHHMM(lo)
	end, ok2 := parseHHMM(hi)
	if !ok1 || !ok2 {
		return quietWindow{}
	}
	return quietWindow{startMin: start, endMin: end, ok: true}
}

func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(h))
	mm, err2 := strconv.Atoi(strings.TrimSpace(m))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// active reports whether t (local) falls inside the window.
func (w quietWindow) active(t time.Time) bool {
	if !w.ok || w.startMin == w.endMin {
		return false
	}
	cur := t.Hour()*60 + t.Minute()
	if w.startMin < w.endMin {
		return cur >= w.startMin && cur < w.endMin
	}
	// Wraps midnight (e.g. 22:00-07:00).
	return cur >= w.startMin || cur < w.endMin
}

// inQuietHours reports whether the given local time is inside the configured
// quiet-hours window.
func (d *Daemon) inQuietHours(now time.Time) bool {
	return parseQuietHours(d.readPref("reactive.quiet_hours")).active(now)
}
