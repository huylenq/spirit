package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
)

// Spirit-owned scheduler + optional Hermes cron (W9 §5). Per Decision 11's "use
// the right primitive by trigger shape": high-frequency Spirit transitions are
// already normalized by the daemon poll loop and need no scheduler. The only
// thing W9 schedules is TIME-OF-DAY digests — and delayed reminders need nothing
// at all, because Later-timer wakes (signalLaterWoke) are deterministic daemon
// mechanics that fire headlessly the moment the lease keeps the daemon awake.
//
// The primary scheduler is this Spirit-owned coarse timer inside `reactive run`.
// It has ZERO dependency on the Hermes gateway. The optional `--with-cron`
// Hermes job is strictly best-effort garnish: when the gateway is down it simply
// does not fire, which is why nothing (least of all Gate E) depends on it.

const (
	// reactiveSchedulerInterval is the coarse timer granularity. Digests are
	// time-of-day, so checking every couple of minutes is ample.
	reactiveSchedulerInterval = 2 * time.Minute
)

// reactiveScheduler runs the worker-side coarse timer while the lease is held.
func reactiveScheduler(stop <-chan struct{}) {
	ticker := time.NewTicker(reactiveSchedulerInterval)
	defer ticker.Stop()
	lastDigestDay := ""
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			at := claude.ReadPref("reactive.digest_at") // "HH:MM", empty = off
			if reactiveDigestDue(now, at, lastDigestDay) {
				lastDigestDay = now.Format("2006-01-02")
				nudgeDailyDigest()
			}
		}
	}
}

// reactiveDigestDue reports whether a daily digest should fire now: a digest
// time is configured, the current local time is at/after it, and no digest has
// fired today yet. An unset/malformed time never fires (fail-soft).
func reactiveDigestDue(now time.Time, at, lastDigestDay string) bool {
	if at == "" {
		return false
	}
	mins, ok := parseHHMMField(at)
	if !ok {
		return false
	}
	today := now.Format("2006-01-02")
	if lastDigestDay == today {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	return cur >= mins
}

// nudgeDailyDigest asks the daemon to compose+deliver the digest. Best-effort:
// a transient daemon hiccup just means the next tick retries.
func nudgeDailyDigest() {
	client, err := daemon.ConnectRPCOnly()
	if err != nil {
		log.Printf("reactive scheduler: digest connect: %v", err)
		return
	}
	defer client.Close()
	if _, err := client.ReactiveDigest(); err != nil {
		log.Printf("reactive scheduler: digest: %v", err)
	}
}

// --- optional Hermes cron (best-effort, gateway-down inert) -------------------

// reactiveCronPath is the descriptor for the optional Lulu-authored morning
// digest cron job under ~/.hermes/. Its sole sanctioned shape is an
// attach_to_session job — it delivers into Lulu's own session so the digest
// lands as a real Lulu message on next open. When the Hermes gateway is down the
// job simply does not fire; the Spirit-owned scheduler above is the reliable one.
func reactiveCronPath() string {
	return filepath.Join(os.Getenv("HOME"), ".hermes", "cron", "spirit-daily-digest.json")
}

// reactiveRegisterCron writes the best-effort Hermes cron descriptor. Failure is
// logged, never fatal — the Spirit-owned scheduler is the mechanism that matters.
func reactiveRegisterCron() {
	at := claude.ReadPref("reactive.digest_at")
	if at == "" {
		at = "09:00"
	}
	job := map[string]any{
		"id":       "spirit-daily-digest",
		"schedule": at, // HH:MM local; the gateway interprets its own cron format
		"kind":     "attach_to_session",
		"note":     "Best-effort Lulu morning digest; inert when the Hermes gateway is down.",
	}
	path := reactiveCronPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("reactive cron: mkdir: %v", err)
		return
	}
	data, _ := json.MarshalIndent(job, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("reactive cron: write: %v", err)
	}
}

// reactiveDeregisterCron removes the best-effort Hermes cron descriptor.
func reactiveDeregisterCron() {
	_ = os.Remove(reactiveCronPath())
}

// parseHHMMField parses "HH:MM" into minutes-since-midnight (mirrors the daemon's
// quiet-hours parser; kept local so the worker has no daemon-internal dep).
func parseHHMMField(s string) (int, bool) {
	var hh, mm int
	n, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &hh, &mm)
	if err != nil || n != 2 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}
