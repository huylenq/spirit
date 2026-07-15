package daemon

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/copilot"
	"github.com/huylenq/spirit/internal/ledger"
)

// The W7 reactive engine: processes triggered watches while at least one TUI
// client is subscribed (spec Decision 11, TUI-active milestone). Driven from
// the existing 1s poll loop — no new timers, so the daemon's 10-minute idle
// shutdown is never defeated: with zero clients, watches trigger (persisted
// ledger state) but are not processed until a client attaches.
//
// Response ladder (Decision 9): inbox = audit + inbox row only; notify = one
// coalesced or immediate notification; inspect_and_recommend = one bounded LLM
// run in a session/fork producing a proposal ON the attention item — never a
// prompt to a coding session, never fleet mutation.

// Tunables (package vars so tests can shrink them).
var (
	// immediateNotifyThrottle is the minimum gap between immediate notifications
	// — "at most one immediate notification", globally, for high-salience items.
	immediateNotifyThrottle = 30 * time.Second
	// digestFlushAge is how long low-urgency firings batch before one digest
	// notification flushes them.
	digestFlushAge = 60 * time.Second
	// reactivePromptTimeout bounds a reactive LLM run (user turns are unbounded;
	// reactive runs are not).
	reactivePromptTimeout = 120 * time.Second
)

// maxRecommendEvidence caps the evidence block inlined into a reactive prompt.
const maxRecommendEvidence = 4000

// reactiveTick is called from the poll loop. It expires stale watches, claims
// triggered ones, and dispatches each firing by its response policy.
func (d *Daemon) reactiveTick() {
	if d.perception == nil {
		return
	}
	d.mu.RLock()
	active := d.clientCount > 0
	d.mu.RUnlock()
	if !active {
		return // TUI-active only (Decision 11)
	}

	d.perception.SweepWatches()

	// One recommend slot, and only when no user turn is streaming and no other
	// reactive run is in flight — a reactive run serializes behind the user,
	// never the other way around. Ineligible recommend watches stay triggered.
	recommendSlots := 0
	if !d.userTurnActive() && d.reactiveRunning.CompareAndSwap(false, true) {
		recommendSlots = 1
	}

	claimed := d.perception.ClaimTriggered(recommendSlots)

	recommendClaimed := false
	for _, w := range claimed {
		switch w.Response {
		case ledger.ResponseRecommend:
			recommendClaimed = true
			go d.runRecommend(w)
		case ledger.ResponseNotify:
			d.perception.AppendItemAudit(w.Pending.ItemID, ledger.AuditEvent{
				Kind: ledger.AuditPolicyDecision, WatchID: w.ID,
				Detail: "notify (autonomy: notify)",
			})
			outcome := d.deliverNotify(w)
			d.perception.CompleteFiring(w.ID, outcome)
		default: // inbox
			d.perception.AppendItemAudit(w.Pending.ItemID, ledger.AuditEvent{
				Kind: ledger.AuditPolicyDecision, WatchID: w.ID,
				Detail: "inbox (autonomy: observe) — no interruption",
			})
			d.perception.AppendItemAudit(w.Pending.ItemID, ledger.AuditEvent{
				Kind: ledger.AuditDelivery, WatchID: w.ID, Detail: "inbox row",
			})
			d.perception.CompleteFiring(w.ID, "inboxed")
		}
	}
	if recommendSlots > 0 && !recommendClaimed {
		d.reactiveRunning.Store(false) // slot reserved but nothing to run
	}

	d.flushAttentionDigest(false)
}

// userTurnActive reports whether a user-initiated Lulu turn is streaming.
func (d *Daemon) userTurnActive() bool {
	d.copilotStateMu.RLock()
	defer d.copilotStateMu.RUnlock()
	return d.copilotActive != nil
}

// watchLine renders the one-line human description of a firing for
// notifications and digests.
func (d *Daemon) watchLine(w ledger.Watch) string {
	item, ok := d.perception.ItemByID(w.Pending.ItemID)
	desc := string(w.Condition)
	scope := ""
	if ok {
		desc = d.perception.DescribeItem(&item)
		if item.Scope.SessionID != "" {
			scope = shortSessionLabel(d.currentSessions(), item.Scope.SessionID)
		} else if item.Scope.Project != "" {
			scope = item.Scope.Project
		}
	}
	if scope != "" {
		return fmt.Sprintf("[%s] %s", scope, desc)
	}
	return desc
}

func shortSessionLabel(sessions []agent.Session, sessionID string) string {
	for i := range sessions {
		if sessions[i].SessionID == sessionID {
			return sessions[i].DisplayName()
		}
	}
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}

// highSalience reports whether an item's category merits an immediate
// notification (waits and failures; everything else batches — Decision 11).
func highSalience(cat ledger.Category) bool {
	switch cat {
	case ledger.CategoryNeedsDecision, ledger.CategoryBlocked, ledger.CategoryDeliveryFailure:
		return true
	}
	return false
}

// deliverNotify delivers one firing as an immediate notification (high-salience
// items only, under the global throttle) or folds it into the triage digest.
// Returns the firing outcome recorded on the watch.
func (d *Daemon) deliverNotify(w ledger.Watch) string {
	line := d.watchLine(w)
	item, ok := d.perception.ItemByID(w.Pending.ItemID)

	immediate := false
	if ok && highSalience(item.Category) {
		d.reactiveMu.Lock()
		if time.Since(d.lastImmediateNotify) >= immediateNotifyThrottle {
			d.lastImmediateNotify = time.Now()
			immediate = true
		}
		d.reactiveMu.Unlock()
	}

	if immediate {
		d.perception.AppendItemAudit(w.Pending.ItemID, ledger.AuditEvent{
			Kind: ledger.AuditDelivery, WatchID: w.ID, Detail: "immediate notification",
		})
		d.pushCopilotStream(CopilotStreamData{Type: "attention", Kind: "notify", Content: line})
		return "notified"
	}

	d.reactiveMu.Lock()
	if len(d.digestLines) == 0 {
		d.digestOldest = time.Now()
	}
	d.digestLines = append(d.digestLines, line)
	d.reactiveMu.Unlock()
	d.perception.AppendItemAudit(w.Pending.ItemID, ledger.AuditEvent{
		Kind: ledger.AuditDelivery, WatchID: w.ID, Detail: "batched into triage digest",
	})
	return "batched"
}

// flushAttentionDigest emits the coalesced triage digest once its oldest entry
// is old enough (or immediately when forced).
func (d *Daemon) flushAttentionDigest(force bool) {
	d.reactiveMu.Lock()
	if len(d.digestLines) == 0 || (!force && time.Since(d.digestOldest) < digestFlushAge) {
		d.reactiveMu.Unlock()
		return
	}
	lines := d.digestLines
	d.digestLines = nil
	d.reactiveMu.Unlock()

	const maxLines = 5
	shown := lines
	extra := 0
	if len(shown) > maxLines {
		extra = len(shown) - maxLines
		shown = shown[:maxLines]
	}
	content := fmt.Sprintf("%d watched event(s): %s", len(lines), strings.Join(shown, "; "))
	if extra > 0 {
		content += fmt.Sprintf("; +%d more", extra)
	}
	d.pushCopilotStream(CopilotStreamData{Type: "attention", Kind: "digest", Content: content})
}

// runRecommend executes one inspect_and_recommend firing: bounded evidence is
// inlined into a prompt against a disposable session/fork (no tools), and the
// resulting proposal is recorded on the attention item and broadcast — nothing
// else. Runs in its own goroutine; owns the reactiveRunning slot.
func (d *Daemon) runRecommend(w ledger.Watch) {
	defer d.reactiveRunning.Store(false)
	led := d.perception
	itemID := w.Pending.ItemID

	downgrade := func(reason string) {
		led.AppendItemAudit(itemID, ledger.AuditEvent{
			Kind: ledger.AuditPolicyDecision, WatchID: w.ID,
			Detail: "recommend downgraded to notify: " + reason,
		})
		outcome := d.deliverNotify(w)
		led.CompleteFiring(w.ID, outcome+" ("+reason+")")
	}

	// Budget spends on attempt (a flapping failure cannot overspend).
	if !led.SpendLLM(w.ID) {
		downgrade("llm budget exhausted")
		return
	}

	led.AppendItemAudit(itemID, ledger.AuditEvent{
		Kind: ledger.AuditPolicyDecision, WatchID: w.ID,
		Detail: fmt.Sprintf("inspect_and_recommend (autonomy: recommend), llm budget %d/%d", w.LLMUsed+1, w.LLMBudget),
	})

	start := time.Now()
	forkID, err := d.acpClient.ForkSession()
	if err == errACPForkUnsupported {
		led.AppendItemAudit(itemID, ledger.AuditEvent{
			Kind: ledger.AuditLLMRun, WatchID: w.ID,
			Detail: "skipped: agent does not advertise session/fork",
		})
		downgrade("fork unavailable")
		return
	}
	if err != nil {
		led.AppendItemAudit(itemID, ledger.AuditEvent{
			Kind: ledger.AuditLLMRun, WatchID: w.ID, Detail: "failed: fork: " + err.Error(),
		})
		led.FailFiring(w.ID, "fork: "+err.Error())
		return
	}

	prompt := d.buildRecommendPrompt(w)
	ctx, cancel := context.WithTimeout(context.Background(), reactivePromptTimeout)
	defer cancel()
	text, err := d.acpClient.PromptSession(ctx, forkID, prompt)
	dur := time.Since(start).Round(time.Millisecond)
	if err != nil || strings.TrimSpace(text) == "" {
		reason := "empty response"
		if err != nil {
			reason = err.Error()
		}
		led.AppendItemAudit(itemID, ledger.AuditEvent{
			Kind: ledger.AuditLLMRun, WatchID: w.ID,
			Detail: fmt.Sprintf("failed after %s (fork %s): %s", dur, forkID, reason),
		})
		led.FailFiring(w.ID, reason)
		return
	}

	led.SetRecommendation(itemID, text)
	led.AppendItemAudit(itemID, ledger.AuditEvent{
		Kind: ledger.AuditLLMRun, WatchID: w.ID,
		Detail: fmt.Sprintf("ok in %s (fork %s, 1 prompt, no tools)", dur, forkID),
	})
	led.AppendItemAudit(itemID, ledger.AuditEvent{
		Kind: ledger.AuditDelivery, WatchID: w.ID, Detail: "recommendation attached + broadcast",
	})
	d.pushCopilotStream(CopilotStreamData{
		Type:    "attention",
		Kind:    "recommendation",
		Content: d.watchLine(w) + " — " + firstNotifyLine(text),
	})
	led.CompleteFiring(w.ID, "recommended")
	log.Printf("reactive: watch %s recommended on item %s (%s)", w.ID, itemID, dur)
}

// buildRecommendPrompt assembles the bounded evidence block for one reactive
// run: the watched session's dossier from fleet truth plus the attention item's
// signal digests. Capped — a reactive run never inlines transcripts wholesale.
func (d *Daemon) buildRecommendPrompt(w ledger.Watch) string {
	var b strings.Builder
	b.WriteString("<reactive-attention-run>\n")
	b.WriteString("You are Spirit's reactive attention branch — a disposable fork of your main conversation. ")
	b.WriteString("A watch you were asked to keep has fired. This run is read-only: you have NO tools; do not attempt any action, do not address the user mid-task.\n\n")
	fmt.Fprintf(&b, "Watch: %s (scope session=%q project=%q, response=%s)\n", w.Condition, w.Scope.SessionID, w.Scope.Project, w.Response)

	item, ok := d.perception.ItemByID(w.Pending.ItemID)
	if ok {
		fmt.Fprintf(&b, "Attention item: %s/%s — %s\n", item.Category, item.Severity, d.perception.DescribeItem(&item))
	}

	var evidence strings.Builder
	if ok && item.Scope.SessionID != "" {
		if s := findSessionByID(d.currentSessions(), item.Scope.SessionID); s != nil {
			evidence.WriteString(copilot.BuildDossier(*s, "", "", "", nil))
			evidence.WriteString("\n")
		}
	}
	if ok {
		for _, sig := range d.perception.ItemSignals(item.ID) {
			fmt.Fprintf(&evidence, "signal %s at %s", sig.Kind, sig.ObservedAt.Format(time.RFC3339))
			for k, v := range sig.Evidence {
				fmt.Fprintf(&evidence, " %s=%v", k, v)
			}
			evidence.WriteString("\n")
		}
	}
	ev := evidence.String()
	if len(ev) > maxRecommendEvidence {
		ev = ev[:maxRecommendEvidence] + "…"
	}
	if ev != "" {
		b.WriteString("\nEvidence:\n")
		b.WriteString(ev)
	}

	b.WriteString("\nReply with a concise recommendation (max 3 sentences): classify the item as verify / unblock / park / follow-up / discard, say why based on the evidence, and name the single next action the user should take. No preamble.\n")
	b.WriteString("</reactive-attention-run>")
	return b.String()
}

// firstNotifyLine bounds the recommendation excerpt used in the broadcast line.
func firstNotifyLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
