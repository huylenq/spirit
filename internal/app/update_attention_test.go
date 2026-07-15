package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/ledger"
	"github.com/huylenq/spirit/internal/ui"
)

// keyRunes builds a plain-rune KeyMsg (e.g. "j", "r").
func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestAttentionChunkFlashesAndCounts(t *testing.T) {
	m := Model{copilot: ui.NewCopilotModel()}

	updated, cmd := m.Update(CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
		Type: "attention", Kind: "notify", Content: "[odb] waiting: permission_prompt",
	}})
	got := updated.(Model)

	if got.attentionUnseen != 1 {
		t.Fatalf("attentionUnseen = %d, want 1", got.attentionUnseen)
	}
	if cmd == nil {
		t.Fatal("attention chunk must flash and re-arm the read loop")
	}
	// The chunk must NOT enter the streaming-turn machinery.
	if got.copilot.Streaming() {
		t.Fatal("attention chunk started a streaming turn")
	}
}

func TestOpenInboxResetsBadgeAndFetches(t *testing.T) {
	m := &Model{attentionUnseen: 3}
	got, cmd := execOpenAttentionInbox(m)
	if got.state != StateAttentionInbox {
		t.Fatalf("state = %v", got.state)
	}
	if got.attentionUnseen != 0 {
		t.Fatalf("badge not reset: %d", got.attentionUnseen)
	}
	if cmd == nil {
		t.Fatal("no fetch command issued")
	}
}

func TestApplyAttentionDataMapsRows(t *testing.T) {
	m := &Model{sessions: []agent.Session{{SessionID: "sess-1", CustomTitle: "spirit-fix"}}}
	m.applyAttentionData(daemon.AttentionListData{
		Items: []ledger.AttentionItem{{
			ID: "item-1", Category: ledger.CategoryVerifyClaim, Severity: ledger.SeverityAttend,
			Status: ledger.StatusOpen, Scope: ledger.Scope{SessionID: "sess-1"},
			Recommendation: "verify\nsecond line ignored",
			Audit: []ledger.AuditEvent{
				{Kind: ledger.AuditWatchTriggered}, {Kind: ledger.AuditWatchTriggered},
				{Kind: ledger.AuditPolicyDecision}, {Kind: ledger.AuditLLMRun}, {Kind: ledger.AuditDelivery},
			},
		}},
		Watches:      []ledger.Watch{{ID: "w1", Condition: ledger.ConditionWaiting, Response: ledger.ResponseNotify, State: ledger.WatchActive, MaxFirings: 20}},
		Descriptions: map[string]string{"item-1": "turn completed: done"},
	})

	it, ok := m.attention.SelectedItem()
	if !ok {
		t.Fatal("no selected item after SetData")
	}
	if it.SessionLabel != "spirit-fix" {
		t.Fatalf("session label = %q", it.SessionLabel)
	}
	if it.Description != "turn completed: done" {
		t.Fatalf("description = %q", it.Description)
	}
	if it.Recommendation != "verify" {
		t.Fatalf("recommendation = %q (must be first line only)", it.Recommendation)
	}
	if it.AuditSummary != "watch_triggered → policy_decision → llm_run → delivery" {
		t.Fatalf("audit summary = %q", it.AuditSummary)
	}
	m.attention.MoveCursor(1)
	if w, ok := m.attention.SelectedWatch(); !ok || w.ScopeLabel != "fleet" || w.Firings != "0/20" {
		t.Fatalf("watch row = %+v ok=%v", w, ok)
	}
}

func TestInboxKeysNavigateAndClose(t *testing.T) {
	m := Model{state: StateAttentionInbox}
	m.applyAttentionData(daemon.AttentionListData{
		Items: []ledger.AttentionItem{{ID: "i1"}, {ID: "i2"}},
	})

	updated, _ := m.handleKey(keyRunes("j"))
	got := updated.(Model)
	if it, _ := got.attention.SelectedItem(); it.ID != "i2" {
		t.Fatalf("selection after j = %q", it.ID)
	}

	updated, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	if updated.(Model).state != StateNormal {
		t.Fatalf("esc did not close the inbox")
	}
}

func TestWatchPickerPhases(t *testing.T) {
	m := Model{state: StateWatchPicker}

	// Unknown key in phase 1 is ignored.
	updated, _ := m.handleKey(keyRunes("x"))
	got := updated.(Model)
	if got.watchPickerCondition != "" || got.state != StateWatchPicker {
		t.Fatalf("unknown key advanced the picker: %+v", got.watchPickerCondition)
	}

	// t = completed_turn.
	updated, _ = got.handleKey(keyRunes("t"))
	got = updated.(Model)
	if got.watchPickerCondition != string(ledger.ConditionCompletedTurn) {
		t.Fatalf("condition = %q", got.watchPickerCondition)
	}
	if !strings.Contains(got.renderWatchPicker(), "response?") {
		t.Fatalf("picker phase 2 not rendered")
	}

	// esc cancels cleanly.
	updated, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	got = updated.(Model)
	if got.state != StateNormal || got.watchPickerCondition != "" {
		t.Fatalf("esc did not reset the picker: state=%v cond=%q", got.state, got.watchPickerCondition)
	}
}

func TestAttentionActionMsgRoutesToCopilot(t *testing.T) {
	m := Model{copilot: ui.NewCopilotModel()}
	updated, _ := m.Update(AttentionActionMsg{Info: "watching completed_turn → inspect_and_recommend", ToCopilot: true})
	got := updated.(Model)
	found := false
	for _, msg := range got.copilot.Messages() {
		if strings.Contains(msg.Content, "watching completed_turn") {
			found = true
		}
	}
	if !found {
		t.Fatalf("copilot panel missing watch info message")
	}
}
