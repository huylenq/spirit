package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/ledger"
)

// This file holds the perception-ledger ingest points (W6 Track A): the glue
// that turns raw fleet observations into normalized, deduplicated signals.
//
// Two racing observation paths report the same transitions — patchSession
// (hook nudge) and poll (1s discovery, which commits a possibly-stale
// pre-hook snapshot wholesale). Correctness rests on two rules applied here:
//
//  1. every edge is confirmed against file truth (ReadStatus/ReadWaiting)
//     before ingest, so a stale poll snapshot cannot fabricate an edge; and
//  2. every anchor is content-derived (transcript uuid, waiting-file mtime,
//     record ids), so when both paths do report the same fact it dedups on
//     (kind, anchor) instead of double-signaling.

// observeFleet diffs the previous in-memory fleet against the freshly
// discovered one and ingests transition signals. Called from poll() after the
// equality check; the first poll after daemon start is baseline-only so a
// restart never replays session_started for the standing fleet.
func (d *Daemon) observeFleet(old, cur []agent.Session) {
	if d.perception == nil {
		return
	}
	now := time.Now()

	oldByID := make(map[string]*agent.Session, len(old))
	for i := range old {
		if old[i].SessionID != "" {
			oldByID[old[i].SessionID] = &old[i]
		}
	}
	curByID := make(map[string]*agent.Session, len(cur))
	for i := range cur {
		if cur[i].SessionID != "" {
			curByID[cur[i].SessionID] = &cur[i]
		}
	}

	for i := range cur {
		s := &cur[i]
		if s.SessionID == "" || s.IsPhantom {
			continue
		}
		if _, existed := oldByID[s.SessionID]; !existed {
			d.signalSessionStarted(*s)
		}
	}

	for i := range old {
		prev := &old[i]
		if prev.SessionID == "" {
			continue
		}
		s, alive := curByID[prev.SessionID]
		if !alive {
			// A Later record whose wake time passed clears via discovery; that
			// removal is a wake, not a death.
			if prev.LaterID != "" && prev.LaterWakeAt != nil && now.After(*prev.LaterWakeAt) {
				d.signalLaterWoke(*prev)
				continue
			}
			if prev.IsPhantom {
				continue // parked sessions come and go without "ending"
			}
			d.signalSessionEnded(*prev)
			continue
		}
		// Live session whose Later record expired (pane survived the park).
		if prev.LaterID != "" && s.LaterID == "" && prev.LaterWakeAt != nil && now.After(*prev.LaterWakeAt) {
			d.signalLaterWoke(*prev)
		}
		if prev.Status == agent.StatusAgentTurn && s.Status == agent.StatusUserTurn && !s.IsWaiting {
			d.signalTurnCompleted(*s, prev.LastChanged)
		}
		if !prev.IsWaiting && s.IsWaiting {
			d.signalWaitingRise(*s)
		}
		if prev.IsWaiting && !s.IsWaiting {
			d.signalWaitingFall(s.SessionID)
		}
	}
}

// signalTurnCompleted ingests a turn_completed signal for a session that just
// crossed agent-turn → user-turn without a waiting pause.
//
// Anchor (the per-turn discriminator, verified against the code):
//   - Codex: the hook-provided turn_id from session meta — per-turn and stable.
//     (Claude Code hooks never send turn_id; it is Codex-only.)
//   - Claude: the transcript uuid of the last assistant text entry — every
//     transcript entry carries a uuid, and the entry is flushed before the
//     Stop hook fires, so at transition time it identifies exactly this turn.
//   - Fallback: the status file's mtime; at a confirmed user-turn observation
//     the last write was Stop's, so it is per-turn at the instant it matters.
//
// A turn interrupted before any assistant text keeps the previous anchor and
// dedups away — deliberate: there is no new claim to verify.
func (d *Daemon) signalTurnCompleted(s agent.Session, turnStarted time.Time) {
	if d.perception == nil || s.SessionID == "" {
		return
	}
	// Confirm the edge against file truth (rule 1 above).
	if st, err := claude.ReadStatus(s.SessionID); err != nil || st != agent.StatusUserTurn {
		return
	}
	if claude.ReadWaiting(s.SessionID) {
		return
	}

	anchor := ""
	info := claude.ReadLastAssistantInfo(s.SessionID)
	if s.Provider == agent.ProviderCodex {
		if turnID := claude.ReadSessionMeta(s.SessionID).TurnID; turnID != "" {
			anchor = s.SessionID + "/" + turnID
		}
	}
	if anchor == "" && info.UUID != "" {
		anchor = s.SessionID + "/" + info.UUID
	}
	if anchor == "" {
		if mt := claude.StatusModTime(s.SessionID); !mt.IsZero() {
			anchor = fmt.Sprintf("%s/mtime-%d", s.SessionID, mt.UnixNano())
		} else {
			return // no discriminator at all — do not fabricate one
		}
	}

	ev := map[string]any{"title": s.DisplayName()}
	if info.Message != "" {
		ev["claim"] = info.Message
	}
	if !turnStarted.IsZero() {
		if dur := time.Since(turnStarted).Round(time.Second); dur > 0 {
			ev["duration"] = dur.String()
		}
	}
	// Queued-delivery → turn linkage (W8): if this turn was started by a
	// delivered queue item, stamp the causing item/action so the chain
	// queued message → delivery → turn → reconciliation is walkable.
	if attrib, ok := d.takeTurnAttribution(s.SessionID); ok {
		ev["caused_by_queue_item"] = attrib.QueueItemID
		if attrib.ActionID != "" {
			ev["caused_by_action"] = attrib.ActionID
		}
	}
	d.perception.Ingest(ledger.SignalTurnCompleted, anchor, s.SessionID, s.Project, ev, "")
}

// signalWaitingRise ingests a waiting_input signal on the rising edge of a
// waiting episode. Anchor: the waiting file's mtime — written once per
// notification, identical across both observation paths.
func (d *Daemon) signalWaitingRise(s agent.Session) {
	if d.perception == nil || s.SessionID == "" {
		return
	}
	kind, at := claude.ReadWaitingInfo(s.SessionID)
	if at.IsZero() {
		return // file truth says not waiting — stale snapshot, skip
	}
	anchor := fmt.Sprintf("%s/wait-%d", s.SessionID, at.UnixNano())
	ev := map[string]any{"title": s.DisplayName()}
	if kind != "" {
		ev["waiting_kind"] = kind
	}
	if s.LastUserMessage != "" {
		ev["last_user_intent"] = s.LastUserMessage
	}
	d.perception.Ingest(ledger.SignalWaitingInput, anchor, s.SessionID, s.Project, ev, "")
}

// signalWaitingFall resolves the session's waiting-derived attention items on
// the falling edge of a waiting episode (the user or the agent moved on).
func (d *Daemon) signalWaitingFall(sessionID string) {
	if d.perception == nil || sessionID == "" {
		return
	}
	if claude.ReadWaiting(sessionID) {
		return // file truth says still waiting — stale snapshot, skip
	}
	d.perception.ResolveSessionItems(sessionID,
		[]ledger.Category{ledger.CategoryNeedsDecision, ledger.CategoryBlocked},
		"waiting ended")
}

func (d *Daemon) signalSessionStarted(s agent.Session) {
	if d.perception == nil || s.SessionID == "" {
		return
	}
	ev := map[string]any{"provider": string(s.Provider), "title": s.DisplayName()}
	d.perception.Ingest(ledger.SignalSessionStarted, s.SessionID, s.SessionID, s.Project, ev, "")
}

func (d *Daemon) signalSessionEnded(s agent.Session) {
	if d.perception == nil || s.SessionID == "" {
		return
	}
	ev := map[string]any{"provider": string(s.Provider), "title": s.DisplayName()}
	d.perception.Ingest(ledger.SignalSessionEnded, s.SessionID, s.SessionID, s.Project, ev, "")
	// A dead session's turn can no longer be attributed; drop any pending link.
	d.takeTurnAttribution(s.SessionID)
	// A dead session can no longer be unblocked; close its waiting items.
	d.perception.ResolveSessionItems(s.SessionID,
		[]ledger.Category{ledger.CategoryNeedsDecision, ledger.CategoryBlocked},
		"session ended")
}

func (d *Daemon) signalLaterWoke(s agent.Session) {
	if d.perception == nil || s.LaterID == "" {
		return
	}
	ev := map[string]any{"title": s.DisplayName()}
	d.perception.Ingest(ledger.SignalLaterWoke, s.LaterID, s.SessionID, s.Project, ev, "")
}

// observeOverlaps ingests overlap_detected signals for overlaps that just
// appeared and resolves overlap items once the fleet is overlap-free. Overlap
// items are fleet-scoped (an overlap names 2+ sessions), so partial clears
// keep the coalesced item open until every overlap is gone.
func (d *Daemon) observeOverlaps(overlaps []claude.FileOverlap, sessions []agent.Session) {
	if d.perception == nil {
		return
	}
	projectBySession := make(map[string]string, len(sessions))
	for _, s := range sessions {
		if s.SessionID != "" {
			projectBySession[s.SessionID] = s.Project
		}
	}
	current := make(map[string]bool, len(overlaps))
	for _, o := range overlaps {
		ids := append([]string(nil), o.SessionIDs...)
		sort.Strings(ids)
		anchor := strings.Join(ids, "+") + "|" + o.FilePath
		current[anchor] = true
		project := ""
		if len(ids) > 0 {
			project = projectBySession[ids[0]]
		}
		ev := map[string]any{
			"file":     o.FilePath,
			"sessions": strings.Join(ids, ", "),
		}
		d.perception.Ingest(ledger.SignalOverlapDetected, anchor, "", project, ev, "")
	}
	if len(current) == 0 && d.hadOverlaps {
		d.perception.ResolveCategoryItems(ledger.CategoryOverlap, "overlaps cleared")
	}
	d.hadOverlaps = len(current) > 0
}

// signalQueueOutcome records the delivery outcome of one PENDING prompt
// (spawn-time prompts keyed by pane). Pending prompts carry no item id, so the
// anchor is a content hash under an anchorScope (session id, or "pane:%s" for
// a prompt whose session never materialized) — a retried failure does not
// re-signal, and a successful delivery supersedes a prior failure signal for
// the same message.
func (d *Daemon) signalQueueOutcome(delivered bool, anchorScope, sessionID, project, message, errMsg string) {
	if d.perception == nil {
		return
	}
	anchor := anchorScope + "/" + contentHash(message)
	ev := map[string]any{"prompt": message}
	if delivered {
		supersedes := d.perception.SignalID(ledger.SignalQueueFailed, anchor)
		d.perception.Ingest(ledger.SignalQueueDelivered, anchor, sessionID, project, ev, supersedes)
		return
	}
	if errMsg != "" {
		ev["error"] = errMsg
	}
	d.perception.Ingest(ledger.SignalQueueFailed, anchor, sessionID, project, ev, "")
}

// signalQueueItemOutcome records the delivery outcome of one queued item.
// Anchor migration (W8, deliberate): queue items now carry durable ids minted
// at enqueue, so the anchor is the item id — stable across delivery retries
// (same item, same anchor) while two identical messages are two distinct
// facts. Evidence carries queue_item_id and, when the item came from a
// batch/MCP action, its action_id — the hook `action_reconciled` watches
// anchor on.
func (d *Daemon) signalQueueItemOutcome(delivered bool, sessionID, project string, item agent.QueueItem, errMsg string) {
	if d.perception == nil {
		return
	}
	anchor := sessionID + "/" + item.ID
	ev := map[string]any{"prompt": item.Message, "queue_item_id": item.ID}
	if item.ActionID != "" {
		ev["action_id"] = item.ActionID
	}
	if delivered {
		supersedes := d.perception.SignalID(ledger.SignalQueueFailed, anchor)
		d.perception.Ingest(ledger.SignalQueueDelivered, anchor, sessionID, project, ev, supersedes)
		return
	}
	if errMsg != "" {
		ev["error"] = errMsg
	}
	d.perception.Ingest(ledger.SignalQueueFailed, anchor, sessionID, project, ev, "")
}

// turnAttribution links a delivered queue item to the next completed turn of
// its session (queued message → delivery → the turn it caused, W8).
type turnAttribution struct {
	QueueItemID string
	ActionID    string
}

// recordTurnAttribution notes that the session's next completed turn was
// caused by the just-delivered queue item. Called under queueMu from the
// resolver; uses its own lock so signalTurnCompleted (poll path) can consume
// it without touching queue state.
func (d *Daemon) recordTurnAttribution(sessionID string, item agent.QueueItem) {
	d.turnAttribMu.Lock()
	d.turnAttrib[sessionID] = turnAttribution{QueueItemID: item.ID, ActionID: item.ActionID}
	d.turnAttribMu.Unlock()
}

// takeTurnAttribution consumes (at most once) the pending attribution for a
// session's completed turn.
func (d *Daemon) takeTurnAttribution(sessionID string) (turnAttribution, bool) {
	d.turnAttribMu.Lock()
	defer d.turnAttribMu.Unlock()
	attrib, ok := d.turnAttrib[sessionID]
	if ok {
		delete(d.turnAttrib, sessionID)
	}
	return attrib, ok
}

// handleActionReport ingests a failed side-effect operation reported by an
// out-of-process surface (the `spirit mcp` server) as an action_failed signal,
// anchored on the receipt's action id.
func (d *Daemon) handleActionReport(data json.RawMessage) *Response {
	var req ActionReportData
	if err := json.Unmarshal(data, &req); err != nil || req.ActionID == "" {
		r := errResponse("invalid action_report request")
		return &r
	}
	ev := map[string]any{"operation": req.Operation}
	if req.Error != "" {
		ev["error"] = req.Error
	}
	if req.SessionID != "" {
		for _, s := range d.currentSessions() {
			if s.SessionID == req.SessionID {
				ev["title"] = s.DisplayName()
				if req.Project == "" {
					req.Project = s.Project
				}
				break
			}
		}
	}
	d.perception.Ingest(ledger.SignalActionFailed, req.ActionID, req.SessionID, req.Project, ev, "")
	r := resultResponse("ok")
	return &r
}

func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}
