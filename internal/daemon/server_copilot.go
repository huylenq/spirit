package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/copilot"
)

const maxCopilotHistory = 200 // 100 exchanges (user + copilot per exchange)

type copilotActiveState struct {
	Epoch     uint64
	RequestID string // client-generated correlation id for this turn
	ClientID  string // originating client; scopes stream delivery
	SessionID string
	Prompt    string
	Output    string
	Streaming bool
}

// beginCopilotTurn installs a new active turn under a fresh monotonic epoch and
// returns that epoch. Because a newer epoch immediately supersedes the old one,
// a cancelled turn's teardown (guarded by its own epoch) can no longer clobber
// the turn that replaced it.
func (d *Daemon) beginCopilotTurn(prompt, requestID, clientID string) uint64 {
	d.copilotStateMu.Lock()
	defer d.copilotStateMu.Unlock()
	d.copilotEpoch++
	d.copilotActive = &copilotActiveState{
		Epoch:     d.copilotEpoch,
		RequestID: requestID,
		ClientID:  clientID,
		Prompt:    prompt,
		Streaming: true,
	}
	return d.copilotEpoch
}

func (d *Daemon) updateCopilotTurn(epoch uint64, evt CopilotStreamData) {
	d.copilotStateMu.Lock()
	defer d.copilotStateMu.Unlock()
	if d.copilotActive == nil || d.copilotActive.Epoch != epoch {
		return // stale turn — a newer prompt owns the active state
	}
	if evt.Type == "session" {
		d.copilotActive.SessionID = evt.Content
	}
	if evt.Type == "text_delta" {
		d.copilotActive.Output += evt.Content
	}
}

// finishCopilotTurn clears the active turn only if the given epoch still owns it,
// so a dying turn cannot nil out the state of the turn that replaced it.
func (d *Daemon) finishCopilotTurn(epoch uint64) {
	d.copilotStateMu.Lock()
	defer d.copilotStateMu.Unlock()
	if d.copilotActive != nil && d.copilotActive.Epoch == epoch {
		d.copilotActive = nil
	}
}

// isCurrentCopilotTurn reports whether epoch still owns the active turn.
func (d *Daemon) isCurrentCopilotTurn(epoch uint64) bool {
	d.copilotStateMu.RLock()
	defer d.copilotStateMu.RUnlock()
	return d.copilotActive != nil && d.copilotActive.Epoch == epoch
}

func (d *Daemon) copilotSnapshot() CopilotSnapshotData {
	d.copilotHistoryMu.RLock()
	history := make([]CopilotHistoryMsg, len(d.copilotHistory))
	copy(history, d.copilotHistory)
	d.copilotHistoryMu.RUnlock()

	d.copilotStateMu.RLock()
	active := d.copilotActive
	if active != nil {
		active = &copilotActiveState{
			RequestID: active.RequestID,
			ClientID:  active.ClientID,
			SessionID: active.SessionID,
			Prompt:    active.Prompt,
			Output:    active.Output,
			Streaming: active.Streaming,
		}
	}
	d.copilotStateMu.RUnlock()

	snapshot := CopilotSnapshotData{History: history}
	if active != nil {
		snapshot.SessionID = active.SessionID
		snapshot.ActivePrompt = active.Prompt
		snapshot.ActiveOutput = active.Output
		snapshot.ActiveRequestID = active.RequestID
		snapshot.ActiveClientID = active.ClientID
		snapshot.Streaming = active.Streaming
	}
	if snapshot.SessionID == "" {
		snapshot.SessionID = d.acpClient.SessionID()
	}
	return snapshot
}

// handleCopilotChat starts a streaming copilot prompt in the background.
// Returns an immediate "streaming" ack; actual tokens arrive via the subscribe connection.
func (d *Daemon) handleCopilotChat(data json.RawMessage) *Response {
	var req CopilotChatData
	if err := json.Unmarshal(data, &req); err != nil || req.Message == "" {
		r := errResponse("invalid copilot_chat request")
		return &r
	}

	// Validate the request-scoped selection against current fleet truth. A vanished
	// selected session fails the request eagerly (fail-fast) rather than silently
	// retargeting a title-match sibling — the exact ambiguity Gate A tests.
	sessions := d.currentSessions()
	var scoped *agent.Session
	if req.Scope != nil && req.Scope.SelectedSessionID != "" {
		s := findSessionByID(sessions, req.Scope.SelectedSessionID)
		if s == nil {
			r := errResponse(fmt.Sprintf("scoped session %s no longer exists; reselect and retry", req.Scope.SelectedSessionID))
			return &r
		}
		scoped = s
	}

	// Assemble the prompt: the scoped dossier (always, when a valid selection
	// exists) plus a fleet snapshot that is injected only when it materially
	// changed since Lulu last saw it.
	fullPrompt := d.buildCopilotPrompt(req, scoped, sessions)

	// Cancel the previous turn, then begin the new one and claim the cancel slot —
	// all under copilotMu so the two operations are ordered. The new epoch makes the
	// old turn stale immediately; its teardown is now a no-op against this turn.
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotCancel()
		// Deny any permission still pending on the turn being superseded so its
		// approval round-trip (and stream events) can't bleed into the new turn.
		d.denyPermissionsForActiveTurn("cancelled")
	}
	epoch := d.beginCopilotTurn(req.Message, req.RequestID, req.ClientID)
	// No prompt timeout: Hermes turns are deliberately unbounded (long tool-using
	// turns are normal). Cancellation is a user action (handleCopilotCancel) plus
	// subprocess-death detection in the ACP client's reader goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	d.copilotCancel = cancel
	d.copilotCancelEpoch = epoch
	d.copilotMu.Unlock()

	// Run streaming in background; results push to the originating client.
	go func() {
		defer d.clearCopilotCancel(epoch)
		// Away-delta (W6 Track A): what happened while the user was away,
		// pulled from the perception ledger at each user-initiated turn. The
		// ACP session is ensured first so the Hermes session UUID — the
		// cursor's owner — is known even on the very first prompt of a fresh
		// conversation (/new ⇒ fresh UUID ⇒ open-item snapshot, no history).
		prompt := fullPrompt
		if d.perception != nil {
			if err := d.acpClient.ensureReady(); err == nil {
				if delta, ok := d.perception.ConsumeDelta(d.acpClient.SessionID(), req.RequestID); ok {
					prompt = delta + "\n\n" + prompt
				}
			} // on error, Prompt() below surfaces it through the normal path
		}
		output, err := d.runCopilotPromptStreaming(ctx, epoch, req.RequestID, req.ClientID, prompt)
		if err != nil {
			return // error + done already sent by runCopilotPromptStreaming
		}
		// Persist the exchange for TUI display across reopens and daemon restarts.
		now := time.Now()
		d.appendCopilotHistory(
			CopilotHistoryMsg{Role: "user", Content: req.Message, Time: now},
			CopilotHistoryMsg{Role: "copilot", Content: output, Time: now},
		)
	}()

	r := resultResponse(map[string]string{"status": "streaming", "requestId": req.RequestID})
	return &r
}

// findSessionByID returns a pointer to the session with the given id, or nil.
func findSessionByID(sessions []agent.Session, id string) *agent.Session {
	for i := range sessions {
		if sessions[i].SessionID == id {
			return &sessions[i]
		}
	}
	return nil
}

// buildCopilotPrompt assembles the daemon-injected context for one Lulu turn.
//
// The scoped dossier is included whenever a valid selection exists (independent
// of the /preamble toggle) — it is small, request-specific, and the whole point
// of Decision 2's "review this" grounding. The /preamble toggle now governs only
// the compact fleet snapshot, which is injected delta-based: skipped entirely
// when the fleet's material state is unchanged since Lulu last saw it, so the
// persistent Hermes session stops accumulating N verbatim snapshots over N turns.
func (d *Daemon) buildCopilotPrompt(req CopilotChatData, scoped *agent.Session, sessions []agent.Session) string {
	var sections []string
	if scoped != nil {
		sections = append(sections, copilot.BuildDossier(*scoped, req.Scope.ActiveView, req.Scope.ActiveLane, req.Scope.ActiveProject))
	}
	if d.copilotPreamble.Load() {
		if snapshot, changed := d.fleetSnapshotDelta(sessions); changed {
			sections = append(sections, snapshot)
		}
	}
	if len(sections) == 0 {
		return req.Message
	}
	return strings.Join(sections, "\n\n") + "\n\n" + req.Message
}

// fleetSnapshotDelta returns the compact fleet preamble only when the fleet's
// material state changed since it was last injected; otherwise it reports no
// change and the caller omits the snapshot. This is the anti-accumulation guard
// for the persistent Hermes session (Audit finding: "the preamble accumulates").
func (d *Daemon) fleetSnapshotDelta(sessions []agent.Session) (string, bool) {
	digest := copilot.FleetDigest(sessions)
	d.copilotFleetMu.Lock()
	defer d.copilotFleetMu.Unlock()
	if digest == d.copilotLastFleetDigest {
		return "", false
	}
	d.copilotLastFleetDigest = digest
	return copilot.BuildSessionsPreamble(sessions), true
}

// resetFleetDelta forgets the last-injected fleet digest so the next prompt of a
// fresh conversation re-injects the fleet once (called on /new).
func (d *Daemon) resetFleetDelta() {
	d.copilotFleetMu.Lock()
	d.copilotLastFleetDigest = ""
	d.copilotFleetMu.Unlock()
}

// handleCopilotHistory returns the copilot conversation for TUI restore on open.
// Served from the daemon's in-memory history, which is loaded from chat_history.json
// at startup so it survives both TUI reopen and daemon restart. Kept cheap (no
// subprocess) because the TUI fetches this eagerly on every launch.
func (d *Daemon) handleCopilotHistory() *Response {
	d.copilotHistoryMu.RLock()
	msgs := make([]CopilotHistoryMsg, len(d.copilotHistory))
	copy(msgs, d.copilotHistory)
	d.copilotHistoryMu.RUnlock()
	r := resultResponse(CopilotHistoryData{Messages: msgs})
	return &r
}

// handleCopilotClearHistory wipes display history and resets the Hermes session so
// the next prompt starts a fresh conversation (triggered by /new in the TUI).
func (d *Daemon) handleCopilotClearHistory() *Response {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = nil
	d.copilotHistoryMu.Unlock()
	os.Remove(copilotHistoryFile()) //nolint:errcheck

	// Forget the last-injected fleet digest so the fresh conversation gets the
	// fleet snapshot once on its first prompt.
	d.resetFleetDelta()

	// Kill the ACP subprocess and forget the Hermes session UUID so the next
	// prompt starts a brand-new conversation instead of resuming the old one.
	d.acpClient.ResetSession()

	r := resultResponse(map[string]string{"status": "cleared"})
	return &r
}

// runCopilotPromptStreaming sends a prompt via the ACP client (hermes acp subprocess),
// streaming events to subscribers in real-time. Returns the full accumulated text
// response for history persistence. Always sends a "done" event as the final stream event.
func (d *Daemon) runCopilotPromptStreaming(ctx context.Context, epoch uint64, requestID, clientID, prompt string) (string, error) {
	// Stamp every event with the originating turn's correlation ids so the client
	// can drop late chunks and the daemon can scope delivery to the requester.
	stamp := func(evt CopilotStreamData) CopilotStreamData {
		evt.RequestID = requestID
		evt.ClientID = clientID
		return evt
	}
	output, err := d.acpClient.Prompt(ctx, prompt, func(evt CopilotStreamData) {
		// Drop events from a superseded turn so a cancelled turn's trailing chunks
		// can't corrupt the live turn's state or the TUI stream.
		if !d.isCurrentCopilotTurn(epoch) {
			return
		}
		d.updateCopilotTurn(epoch, evt)
		d.pushCopilotStream(stamp(evt))
	})
	// Only the current turn emits terminal events and clears active state; a stale
	// turn stays silent so its done/error can't end the live turn early.
	if !d.isCurrentCopilotTurn(epoch) {
		return output, err
	}
	if err != nil {
		d.pushCopilotStream(stamp(CopilotStreamData{Type: "error", Content: err.Error()}))
	}
	d.finishCopilotTurn(epoch)
	d.pushCopilotStream(stamp(CopilotStreamData{Type: "done"}))
	return output, err
}

// handleCopilotCancel cancels any in-flight copilot prompt and denies any
// permission requests tied to the cancelled turn so their stream events can't leak
// into the next turn (Gate A).
func (d *Daemon) handleCopilotCancel() *Response {
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotCancel()
		d.copilotCancel = nil
	}
	d.copilotMu.Unlock()
	d.denyPermissionsForActiveTurn("cancelled")
	r := resultResponse(map[string]string{"status": "cancelled"})
	return &r
}

// handleCopilotSetMode switches the Hermes session mode (autonomy ceiling) and
// returns the new mode selector state.
func (d *Daemon) handleCopilotSetMode(data json.RawMessage) *Response {
	var req CopilotSetModeData
	if err := json.Unmarshal(data, &req); err != nil || req.ModeID == "" {
		r := errResponse("invalid copilot_set_mode request")
		return &r
	}
	modes, err := d.acpClient.SetMode(req.ModeID)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(modes)
	return &r
}

// handleCopilotTogglePreamble toggles injection of live session context into copilot prompts.
func (d *Daemon) handleCopilotTogglePreamble() *Response {
	newVal := !d.copilotPreamble.Load()
	d.copilotPreamble.Store(newVal)
	state := "off"
	if newVal {
		state = "on"
	}
	r := resultResponse(map[string]string{"preamble": state})
	return &r
}

func (d *Daemon) handleCopilotSetModel(data json.RawMessage) *Response {
	var req CopilotSetModelData
	if err := json.Unmarshal(data, &req); err != nil || req.ModelID == "" {
		r := errResponse("invalid copilot_set_model request")
		return &r
	}
	models, err := d.acpClient.SetModel(req.ModelID)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(models)
	return &r
}

// handleCopilotStatus returns copilot readiness and stats.
func (d *Daemon) handleCopilotStatus() *Response {
	models, err := d.acpClient.ModelStatus()
	if err != nil {
		r := errResponse("copilot status: " + err.Error())
		return &r
	}
	modes, err := d.acpClient.ModeStatus()
	if err != nil {
		r := errResponse("copilot status: " + err.Error())
		return &r
	}

	r := resultResponse(CopilotStatusData{
		Ready:       true,
		EventsToday: d.perception.SignalsToday(),
		SessionID:   d.acpClient.SessionID(),
		Models:      models,
		Modes:       modes,
	})
	return &r
}

// clearCopilotCancel releases the cancel slot when a copilot turn finishes, but
// only if that turn still owns it. A superseded turn finishing later must not
// cancel or nil the cancel func belonging to the turn that replaced it.
func (d *Daemon) clearCopilotCancel(epoch uint64) {
	d.copilotMu.Lock()
	if d.copilotCancelEpoch == epoch && d.copilotCancel != nil {
		d.copilotCancel()
		d.copilotCancel = nil
	}
	d.copilotMu.Unlock()
}

// appendCopilotHistory appends messages, trims to max, and persists to disk so the
// conversation survives TUI reopen and daemon restart.
func (d *Daemon) appendCopilotHistory(msgs ...CopilotHistoryMsg) {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = append(d.copilotHistory, msgs...)
	if len(d.copilotHistory) > maxCopilotHistory {
		d.copilotHistory = d.copilotHistory[len(d.copilotHistory)-maxCopilotHistory:]
	}
	snapshot := make([]CopilotHistoryMsg, len(d.copilotHistory))
	copy(snapshot, d.copilotHistory)
	d.copilotHistoryMu.Unlock()

	saveCopilotHistory(snapshot)
}

// copilotHistoryFile is the on-disk store for copilot display history
// (~/.spirit/copilot/chat_history.json).
func copilotHistoryFile() string {
	return filepath.Join(claude.StatusDir(), "copilot", "chat_history.json")
}

// loadCopilotHistory reads persisted display history at daemon startup.
func loadCopilotHistory() []CopilotHistoryMsg {
	data, err := os.ReadFile(copilotHistoryFile())
	if err != nil {
		return nil
	}
	var msgs []CopilotHistoryMsg
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil
	}
	return msgs
}

// saveCopilotHistory writes display history to disk (best-effort).
func saveCopilotHistory(msgs []CopilotHistoryMsg) {
	path := copilotHistoryFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(msgs)
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0o644) //nolint:errcheck
}
