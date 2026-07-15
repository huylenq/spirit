package mcpserver

import (
	"encoding/json"
	"fmt"

	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/receipt"
)

// Watch + attention tools (W7). create_watch stamps the receipt's action_id
// into the watch's created_by_request_id, so every Lulu-created watch is
// traceable to the exact tool call that made it.

func handleListWatches(api daemonAPI, args json.RawMessage) (any, bool) {
	watches, err := api.WatchList()
	if err != nil {
		return errPayload("list_watches", err), true
	}
	return watches, false
}

func handleListAttention(api daemonAPI, args json.RawMessage) (any, bool) {
	data, err := api.AttentionList()
	if err != nil {
		return errPayload("list_attention", err), true
	}
	return map[string]any{
		"items":        data.Items,
		"descriptions": data.Descriptions,
		"watches":      data.Watches,
	}, false
}

// handleReactiveStatus reports the read-only durable-reactivity status (W9).
// There is deliberately no enable/disable tool — the autonomy switch is human-
// only (`spirit reactive enable`), off the reactive/agent path entirely.
func handleReactiveStatus(api daemonAPI, args json.RawMessage) (any, bool) {
	st, err := api.ReactiveStatus()
	if err != nil {
		return errPayload("reactive_status", err), true
	}
	return st, false
}

func handleCreateWatch(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID        string `json:"session_id"`
		Project          string `json:"project"`
		ActionID         string `json:"action_id"`
		Condition        string `json:"condition"`
		Response         string `json:"response"`
		ExpiresInMinutes int    `json:"expires_in_minutes"`
		CooldownSeconds  int    `json:"cooldown_seconds"`
		MaxFirings       int    `json:"max_firings"`
		LLMBudget        int    `json:"llm_budget"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Condition == "" || a.Response == "" {
		return receipt.New("create_watch", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}).
			Fail(fmt.Errorf("condition and response are required")), true
	}
	rcpt := receipt.New("create_watch", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit})
	rcpt.Params = map[string]any{
		"condition": a.Condition, "response": a.Response, "project": a.Project,
		"expires_in_minutes": a.ExpiresInMinutes, "cooldown_seconds": a.CooldownSeconds,
		"max_firings": a.MaxFirings, "llm_budget": a.LLMBudget,
	}
	if a.ActionID != "" {
		rcpt.Params["action_id"] = a.ActionID
	}
	w, err := api.WatchCreate(daemon.WatchCreateData{
		SessionID:          a.SessionID,
		Project:            a.Project,
		ActionID:           a.ActionID,
		Condition:          a.Condition,
		Response:           a.Response,
		ExpiresInMinutes:   a.ExpiresInMinutes,
		CooldownSeconds:    a.CooldownSeconds,
		MaxFirings:         a.MaxFirings,
		LLMBudget:          a.LLMBudget,
		CreatedBy:          "lulu",
		CreatedByRequestID: rcpt.ActionID,
	})
	if err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.Params["watch"] = w
	return rcpt, false
}

func handleCancelWatch(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		WatchID string `json:"watch_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.WatchID == "" {
		return receipt.New("cancel_watch", receipt.Target{}).Fail(fmt.Errorf("watch_id is required")), true
	}
	rcpt := receipt.New("cancel_watch", receipt.Target{ResolvedBy: receipt.ResolvedExplicit})
	rcpt.Params = map[string]any{"watch_id": a.WatchID}
	w, err := api.WatchCancel(a.WatchID)
	if err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.Params["watch"] = w
	return rcpt, false
}

func handleResolveAttention(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		ItemID     string `json:"item_id"`
		Resolution string `json:"resolution"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.ItemID == "" {
		return receipt.New("resolve_attention", receipt.Target{}).Fail(fmt.Errorf("item_id is required")), true
	}
	rcpt := receipt.New("resolve_attention", receipt.Target{ResolvedBy: receipt.ResolvedExplicit})
	resolution := a.Resolution
	if resolution == "" {
		resolution = "resolved by lulu"
	}
	rcpt.Params = map[string]any{"item_id": a.ItemID, "resolution": resolution}
	if err := api.AttentionResolve(a.ItemID, resolution); err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	return rcpt, false
}
