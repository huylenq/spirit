package app

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/ledger"
	"github.com/huylenq/spirit/internal/ui"
)

// The attention inbox + watch surfaces (W7): chord `ga` opens the inbox
// (unresolved attention items with audit summaries + watch states; `r`
// resolves, `c` cancels a watch), chord `gw` creates a watch on the selected
// session via a two-keystroke picker, and `/watch` in the Lulu input creates a
// default watch on the scoped session. Reactive `attention` stream chunks
// flash + count toward an unseen badge until the inbox is opened.

// --- messages ---

// AttentionListMsg carries the inbox contents fetched from the daemon.
type AttentionListMsg struct {
	Data daemon.AttentionListData
	Err  error
}

// AttentionActionMsg reports the outcome of a watch/attention mutation
// (create/cancel/resolve). Refresh indicates the inbox should re-fetch.
type AttentionActionMsg struct {
	Info    string
	Err     error
	Refresh bool
	// ToCopilot routes the outcome into the Lulu panel (for /watch) instead of
	// the flash bar.
	ToCopilot bool
}

// --- executors (chords) ---

// execOpenAttentionInbox opens the inbox overlay and fetches its contents.
func execOpenAttentionInbox(m *Model) (Model, tea.Cmd) {
	m.state = StateAttentionInbox
	m.attentionUnseen = 0
	m.attention.SetReactiveState(m.reactiveStatus.Enabled, m.reactiveStatus.Paused, m.reactiveStatus.GateReason)
	return *m, tea.Batch(m.fetchAttention(), m.fetchReactiveStatus())
}

// execWatchSelected starts the two-keystroke watch picker for the selected
// session (condition, then response).
func execWatchSelected(m *Model) (Model, tea.Cmd) {
	if _, ok := m.currentSelectedSession(); !ok {
		return *m, m.setFlash("watch: no session selected", true, 2*time.Second)
	}
	m.state = StateWatchPicker
	m.watchPickerCondition = ""
	return *m, nil
}

// --- daemon commands ---

func (m *Model) fetchAttention() tea.Cmd {
	return func() tea.Msg {
		data, err := m.client.AttentionList()
		return AttentionListMsg{Data: data, Err: err}
	}
}

// createWatch registers a watch via the daemon. Defaults (24h expiry, 60s
// cooldown, 20 firings) are applied daemon-side.
func (m *Model) createWatch(sessionID, condition, response string, toCopilot bool) tea.Cmd {
	requestID := daemon.NewCorrelationID()
	return func() tea.Msg {
		w, err := m.client.WatchCreate(daemon.WatchCreateData{
			SessionID:          sessionID,
			Condition:          condition,
			Response:           response,
			CreatedBy:          "tui",
			CreatedByRequestID: requestID,
		})
		if err != nil {
			return AttentionActionMsg{Err: err, ToCopilot: toCopilot}
		}
		return AttentionActionMsg{
			Info:      fmt.Sprintf("watching %s → %s (until %s)", w.Condition, w.Response, w.ExpiresAt.Format("15:04")),
			ToCopilot: toCopilot,
		}
	}
}

func (m *Model) resolveAttentionItem(itemID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.AttentionResolve(itemID, "user ack"); err != nil {
			return AttentionActionMsg{Err: err}
		}
		return AttentionActionMsg{Info: "resolved", Refresh: true}
	}
}

func (m *Model) cancelWatchCmd(watchID string) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.client.WatchCancel(watchID); err != nil {
			return AttentionActionMsg{Err: err}
		}
		return AttentionActionMsg{Info: "watch cancelled", Refresh: true}
	}
}

// --- key handlers ---

func (m Model) handleKeyAttentionInbox(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.state = StateNormal
		return m, nil
	case "j", "down":
		m.attention.MoveCursor(1)
		return m, nil
	case "k", "up":
		m.attention.MoveCursor(-1)
		return m, nil
	case "r":
		if it, ok := m.attention.SelectedItem(); ok {
			return m, m.resolveAttentionItem(it.ID)
		}
		return m, nil
	case "c":
		if w, ok := m.attention.SelectedWatch(); ok {
			return m, m.cancelWatchCmd(w.ID)
		}
		return m, nil
	}
	return m, nil
}

// watchPickerConditions/Responses map accelerator keys for the gw picker.
var watchPickerConditions = map[string]string{
	"t": string(ledger.ConditionCompletedTurn),
	"w": string(ledger.ConditionWaiting),
	"o": string(ledger.ConditionOverlap),
	"a": string(ledger.ConditionActionReconciled),
}

var watchPickerResponses = map[string]string{
	"i": string(ledger.ResponseInbox),
	"n": string(ledger.ResponseNotify),
	"r": string(ledger.ResponseRecommend),
}

func (m Model) handleKeyWatchPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.state = StateNormal
		m.watchPickerCondition = ""
		return m, nil
	}
	if m.watchPickerCondition == "" {
		if cond, ok := watchPickerConditions[key]; ok {
			m.watchPickerCondition = cond
		}
		return m, nil
	}
	if resp, ok := watchPickerResponses[key]; ok {
		cond := m.watchPickerCondition
		m.watchPickerCondition = ""
		m.state = StateNormal
		s, ok := m.currentSelectedSession()
		if !ok || s.SessionID == "" {
			return m, m.setFlash("watch: no session selected", true, 2*time.Second)
		}
		return m, m.createWatch(s.SessionID, cond, resp, false)
	}
	return m, nil
}

// renderWatchPicker renders the two-phase accelerator prompt.
func (m Model) renderWatchPicker() string {
	if m.watchPickerCondition == "" {
		return ui.WatchPickerView("watch: condition?", "[t] completed turn  [w] waiting  [o] overlap  [a] action reconciled")
	}
	return ui.WatchPickerView(
		fmt.Sprintf("watch %s: response?", m.watchPickerCondition),
		"[i] inbox  [n] notify  [r] inspect+recommend")
}

// --- inbox data mapping (wire → ui rows) ---

// applyAttentionData maps the RPC payload onto the ui component's row types,
// resolving session ids to display names from current fleet truth.
func (m *Model) applyAttentionData(data daemon.AttentionListData) {
	labels := make(map[string]string, len(m.sessions))
	for _, s := range m.sessions {
		if s.SessionID != "" {
			labels[s.SessionID] = s.DisplayName()
		}
	}
	shortLabel := func(id string) string {
		if id == "" {
			return ""
		}
		if name := labels[id]; name != "" {
			return name
		}
		if len(id) > 8 {
			return id[:8]
		}
		return id
	}

	items := make([]ui.AttentionItemRow, 0, len(data.Items))
	for _, it := range data.Items {
		items = append(items, ui.AttentionItemRow{
			ID:             it.ID,
			Category:       string(it.Category),
			Severity:       string(it.Severity),
			Status:         string(it.Status),
			SessionLabel:   shortLabel(it.Scope.SessionID),
			Description:    data.Descriptions[it.ID],
			Recommendation: firstAttentionLine(it.Recommendation),
			AuditSummary:   summarizeAudit(it.Audit),
			UpdatedAt:      it.UpdatedAt,
		})
	}

	watches := make([]ui.WatchRow, 0, len(data.Watches))
	for _, w := range data.Watches {
		scope := "fleet"
		if w.Scope.SessionID != "" {
			scope = shortLabel(w.Scope.SessionID)
		} else if w.Scope.Project != "" {
			scope = w.Scope.Project
		}
		watches = append(watches, ui.WatchRow{
			ID:         w.ID,
			ScopeLabel: scope,
			Condition:  string(w.Condition),
			Response:   string(w.Response),
			State:      string(w.State),
			Firings:    fmt.Sprintf("%d/%d", w.Firings, w.MaxFirings),
			Outcome:    w.LastOutcome,
		})
	}
	m.attention.SetData(items, watches)
}

// summarizeAudit compresses the causal chain into one line: the audit kinds in
// order, deduplicated consecutively (e.g. "watch_triggered → llm_run → delivery").
func summarizeAudit(audit []ledger.AuditEvent) string {
	if len(audit) == 0 {
		return ""
	}
	var kinds []string
	for _, ev := range audit {
		if n := len(kinds); n > 0 && kinds[n-1] == ev.Kind {
			continue
		}
		kinds = append(kinds, ev.Kind)
	}
	return strings.Join(kinds, " → ")
}

func firstAttentionLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
