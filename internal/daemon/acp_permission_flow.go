package daemon

import (
	"encoding/json"
	"log"
	"time"
)

// The W4 approval pipeline: ACP → daemon → TUI → daemon → ACP.
//
// Hermes only sends session/request_permission for the things it wants a human to
// approve — dangerous commands and edits (read-only tool calls never prompt). The
// W0 auto-deny stopgap existed because no real flow could reach the human. Now it
// can, so the policy is: forward EVERY arriving permission request to the
// originating TUI client and let the human decide. The coarse autonomy ceiling
// (which edits Hermes auto-approves before it ever asks) lives in the session mode
// (Decision 9); anything Hermes still asks about, the human answers here.
//
// Fail-safe: if no TUI client is connected to answer, deny rather than hold the
// tool call open until Hermes's own 60s timeout. Cancelling the owning prompt, or
// the owning client disconnecting, also denies any pending request so a dying
// turn's approval can't leak into the next one (Gate A).

// copilotPermissionTimeout is how long the daemon waits for a human answer before
// auto-denying. It is set just under Hermes's own 60s auto-deny so the daemon
// resolves first and the outcome is deterministic (a late human answer would race
// Hermes's timeout and could be silently dropped).
const copilotPermissionTimeout = 55 * time.Second

// permissionTimeout returns the auto-deny wait, allowing tests to shorten it via
// the copilotPermTimeout field.
func (d *Daemon) permissionTimeout() time.Duration {
	if d.copilotPermTimeout > 0 {
		return d.copilotPermTimeout
	}
	return copilotPermissionTimeout
}

// permAnswer is the resolution delivered to a waiting decideCopilotPermission.
// optionID is the chosen option id ("" refuses); reason classifies the outcome for
// the transcript receipt (user / expired / cancelled / disconnected).
type permAnswer struct {
	optionID string
	reason   string
}

// pendingPermission is one in-flight approval round-trip.
type pendingPermission struct {
	id        string
	clientID  string // owning client (for disconnect-scoped denial); "" = broadcast
	requestID string // owning turn (for cancel-scoped denial)
	answer    chan permAnswer
}

// decideCopilotPermission is the ACP client's onPermission handler. It runs in the
// reader's per-request goroutine, so it may block up to copilotPermissionTimeout
// waiting for the human without stalling the streaming prompt. It returns the
// chosen option id, or "" to refuse the request.
func (d *Daemon) decideCopilotPermission(params json.RawMessage) string {
	parsed, err := parsePermissionRequest(params)
	if err != nil {
		log.Printf("acp: unparseable permission request denied: %v", err)
		return ""
	}

	// A permission request always arrives mid-prompt, tied to the active turn —
	// route it to that turn's originating client (W2 correlation).
	requestID, ownerClient := d.activeTurnCorrelation()

	// Pick a delivery target. If the owning client is still attached, scope to it;
	// otherwise fall back to broadcasting to whatever clients are attached. With no
	// client at all, fail safe and deny.
	deliverClient, anyClient := d.copilotPermissionTarget(ownerClient)
	if !anyClient {
		log.Printf("acp: no TUI client to answer permission %q; denying (fail-safe)", permissionLabel(parsed))
		return ""
	}

	pend := &pendingPermission{
		id:        NewCorrelationID(),
		clientID:  deliverClient,
		requestID: requestID,
		answer:    make(chan permAnswer, 1),
	}
	d.registerPermission(pend)
	defer d.resolvePending(pend.id) // belt: drop the entry on any return path

	timeout := d.permissionTimeout()
	deadline := time.Now().Add(timeout)
	d.pushCopilotStream(CopilotStreamData{
		Type:       "permission_request",
		RequestID:  requestID,
		ClientID:   deliverClient,
		Permission: parsed.toPayload(pend.id, deadline),
	})

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var ans permAnswer
	select {
	case ans = <-pend.answer:
		// A resolver (user answer / cancel / disconnect) removed the entry and sent.
	case <-timer.C:
		if _, ok := d.resolvePending(pend.id); ok {
			ans = permAnswer{optionID: "", reason: "expired"}
			log.Printf("acp: permission %q expired after %s; denying", permissionLabel(parsed), timeout)
		} else {
			// Lost the race — an answer landed just as the timer fired; take it.
			ans = <-pend.answer
		}
	}

	status := permissionOutcome(parsed, ans)
	d.pushCopilotStream(CopilotStreamData{
		Type:      "permission_resolved",
		RequestID: requestID,
		ClientID:  deliverClient,
		Content:   pend.id,
		Status:    status,
		Kind:      ans.optionID,
		Permission: &CopilotPermissionRequest{
			PermissionID: pend.id,
			Title:        parsed.Title,
			Kind:         parsed.Kind,
		},
	})
	return ans.optionID
}

// toPayload materializes the wire payload forwarded to the TUI, stamping the
// daemon-minted permission id and the absolute auto-deny deadline.
func (p parsedPermission) toPayload(permissionID string, deadline time.Time) *CopilotPermissionRequest {
	return &CopilotPermissionRequest{
		PermissionID: permissionID,
		ToolCallID:   p.ToolCallID,
		Title:        p.Title,
		Kind:         p.Kind,
		Command:      p.Command,
		Diffs:        p.Diffs,
		Options:      p.Options,
		Sensitive:    p.Sensitive,
		SensitiveHit: p.SensitiveHit,
		DeadlineUnix: deadline.Unix(),
	}
}

// permissionOutcome classifies the round-trip result for the transcript receipt.
func permissionOutcome(parsed parsedPermission, ans permAnswer) string {
	switch ans.reason {
	case "expired":
		return "expired"
	case "cancelled":
		return "cancelled"
	case "disconnected":
		return "denied"
	default:
		if ans.optionID != "" && parsed.isAllowOptionID(ans.optionID) {
			return "approved"
		}
		return "denied"
	}
}

func permissionLabel(p parsedPermission) string {
	if p.Title != "" {
		return p.Title
	}
	if p.Command != "" {
		return p.Command
	}
	return p.Kind
}

// --- pending registry ---

func (d *Daemon) registerPermission(pend *pendingPermission) {
	d.copilotPermMu.Lock()
	d.copilotPerms[pend.id] = pend
	d.copilotPermMu.Unlock()
}

// resolvePending atomically removes and returns the pending entry. Exactly one
// caller (answer / timeout / cancel / disconnect) wins the removal; the losers get
// ok=false and must treat their action as a no-op.
func (d *Daemon) resolvePending(id string) (*pendingPermission, bool) {
	d.copilotPermMu.Lock()
	defer d.copilotPermMu.Unlock()
	pend, ok := d.copilotPerms[id]
	if !ok {
		return nil, false
	}
	delete(d.copilotPerms, id)
	return pend, true
}

// handleCopilotPermissionAnswer resolves a pending request with the human's choice.
// A stale/duplicate answer (already resolved by timeout, cancel, or an earlier
// answer) is an informative no-op.
func (d *Daemon) handleCopilotPermissionAnswer(data json.RawMessage) *Response {
	var req CopilotPermissionAnswerData
	if err := json.Unmarshal(data, &req); err != nil || req.PermissionID == "" {
		r := errResponse("invalid copilot_permission_answer request")
		return &r
	}
	pend, ok := d.resolvePending(req.PermissionID)
	if !ok {
		r := resultResponse(map[string]string{"status": "already_resolved"})
		return &r
	}
	pend.answer <- permAnswer{optionID: req.OptionID, reason: "user"}
	r := resultResponse(map[string]string{"status": "answered"})
	return &r
}

// denyPermissionsForActiveTurn denies every pending request tied to the current
// active turn's request id (called when that turn is cancelled/superseded). When
// there is no active turn it denies all pending requests, since a permission can
// only belong to the turn that was just cancelled.
func (d *Daemon) denyPermissionsForActiveTurn(reason string) {
	requestID, _ := d.activeTurnCorrelation()
	d.copilotPermMu.Lock()
	var victims []*pendingPermission
	for id, pend := range d.copilotPerms {
		if requestID == "" || pend.requestID == requestID {
			victims = append(victims, pend)
			delete(d.copilotPerms, id)
		}
	}
	d.copilotPermMu.Unlock()
	for _, pend := range victims {
		pend.answer <- permAnswer{optionID: "", reason: reason}
	}
}

// denyPermissionsForClient denies every pending request owned by a client that
// disconnected, so a vanished answerer can't hold a tool call open.
func (d *Daemon) denyPermissionsForClient(clientID string) {
	if clientID == "" {
		return
	}
	d.copilotPermMu.Lock()
	var victims []*pendingPermission
	for id, pend := range d.copilotPerms {
		if pend.clientID == clientID {
			victims = append(victims, pend)
			delete(d.copilotPerms, id)
		}
	}
	d.copilotPermMu.Unlock()
	for _, pend := range victims {
		pend.answer <- permAnswer{optionID: "", reason: "disconnected"}
	}
}

// activeTurnCorrelation returns the request/client ids of the in-flight turn, or
// empty strings if none is active.
func (d *Daemon) activeTurnCorrelation() (requestID, clientID string) {
	d.copilotStateMu.RLock()
	defer d.copilotStateMu.RUnlock()
	if d.copilotActive == nil {
		return "", ""
	}
	return d.copilotActive.RequestID, d.copilotActive.ClientID
}

// copilotPermissionTarget decides where to route a permission prompt. If the owning
// client is still subscribed, deliver to it (scoped). Otherwise, if any client is
// subscribed, broadcast (deliverTo ""). Returns any=false when no client is
// attached at all — the caller then denies (fail-safe).
func (d *Daemon) copilotPermissionTarget(ownerClient string) (deliverTo string, any bool) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	if len(d.subscribers) == 0 {
		return "", false
	}
	if ownerClient != "" {
		for sub := range d.subscribers {
			if sub.clientID == ownerClient {
				return ownerClient, true
			}
		}
	}
	return "", true // owner gone (or unknown) but others attached → broadcast
}
