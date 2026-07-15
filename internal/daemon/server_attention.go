package daemon

import (
	"encoding/json"
	"time"

	"github.com/huylenq/spirit/internal/ledger"
)

// Watch + attention-inbox RPC handlers (W7). All of them are thin veneers over
// the ledger — the FSM and validity rules live there, so every surface (TUI,
// MCP, Lua) gets identical semantics.

// Watch creation defaults applied when the caller leaves limits zero. The
// ledger still enforces validity (a watch without expiry or rate limit is
// invalid), so these are conveniences, not fallbacks around bad input.
const (
	defaultWatchExpiry   = 24 * time.Hour
	defaultWatchCooldown = 60 // seconds
	defaultWatchFirings  = 20
)

func (d *Daemon) handleWatchCreate(data json.RawMessage) *Response {
	var req WatchCreateData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("invalid watch_create request")
		return &r
	}
	if d.perception == nil {
		r := errResponse("perception ledger is disabled")
		return &r
	}

	// A session-scoped watch must name a live session — fail eagerly rather
	// than silently watching an id that can never fire.
	if req.SessionID != "" {
		if findSessionByID(d.currentSessions(), req.SessionID) == nil {
			r := errResponse("watch target session not found: " + req.SessionID)
			return &r
		}
	}

	expiry := defaultWatchExpiry
	if req.ExpiresInMinutes > 0 {
		expiry = time.Duration(req.ExpiresInMinutes) * time.Minute
	}
	cooldown := req.CooldownSeconds
	if cooldown == 0 {
		cooldown = defaultWatchCooldown
	}
	firings := req.MaxFirings
	if firings == 0 {
		firings = defaultWatchFirings
	}

	w, err := d.perception.CreateWatch(ledger.Watch{
		Scope:              ledger.WatchScope{SessionID: req.SessionID, Project: req.Project, ActionID: req.ActionID},
		Condition:          ledger.WatchCondition(req.Condition),
		Response:           ledger.WatchResponse(req.Response),
		ExpiresAt:          time.Now().Add(expiry),
		CooldownSeconds:    cooldown,
		MaxFirings:         firings,
		LLMBudget:          req.LLMBudget,
		CreatedBy:          req.CreatedBy,
		CreatedByRequestID: req.CreatedByRequestID,
	})
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(WatchResultData{Watch: *w})
	return &r
}

func (d *Daemon) handleWatchList() *Response {
	if d.perception == nil {
		r := errResponse("perception ledger is disabled")
		return &r
	}
	r := resultResponse(AttentionListData{Watches: d.perception.Watches()})
	return &r
}

func (d *Daemon) handleWatchCancel(data json.RawMessage) *Response {
	var req WatchIDData
	if err := json.Unmarshal(data, &req); err != nil || req.WatchID == "" {
		r := errResponse("invalid watch_cancel request")
		return &r
	}
	if d.perception == nil {
		r := errResponse("perception ledger is disabled")
		return &r
	}
	if err := d.perception.CancelWatch(req.WatchID); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	w, _ := d.perception.WatchByID(req.WatchID)
	r := resultResponse(WatchResultData{Watch: w})
	return &r
}

func (d *Daemon) handleAttentionList() *Response {
	if d.perception == nil {
		r := errResponse("perception ledger is disabled")
		return &r
	}
	items := d.perception.UnresolvedItems()
	desc := make(map[string]string, len(items))
	for i := range items {
		desc[items[i].ID] = d.perception.DescribeItem(&items[i])
	}
	r := resultResponse(AttentionListData{
		Items:        items,
		Watches:      d.perception.Watches(),
		Descriptions: desc,
	})
	return &r
}

func (d *Daemon) handleAttentionResolve(data json.RawMessage) *Response {
	var req AttentionResolveData
	if err := json.Unmarshal(data, &req); err != nil || req.ItemID == "" {
		r := errResponse("invalid attention_resolve request")
		return &r
	}
	if d.perception == nil {
		r := errResponse("perception ledger is disabled")
		return &r
	}
	if err := d.perception.ResolveItem(req.ItemID, req.Resolution); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(map[string]string{"status": "resolved", "itemID": req.ItemID})
	return &r
}
