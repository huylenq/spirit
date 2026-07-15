package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/ui"
)

// --- Copilot state helpers ---

// setCopilotVisible sets visibility, persists the preference, and recalculates
// layout when in docked mode (since the detail panel width changes).
func (m *Model) setCopilotVisible(v bool) {
	m.copilotVisible = v
	savePrefBool("copilotVisible", v)
	if m.copilotMode == CopilotModeDocked {
		m.applyLayout()
	}
}

// focusCopilot activates copilot input and sets focused styling.
func (m *Model) focusCopilot() {
	m.copilotInput.TextInput().Focus()
	m.copilotInput.SetPromptStyle(ui.CopilotPromptStyle)
}

// unfocusCopilot transitions the copilot from focused to unfocused (visible but read-only).
func (m *Model) unfocusCopilot() {
	m.state = StateNormal
	m.copilotInput.TextInput().Blur()
	m.copilotInput.SetPromptStyle(ui.CopilotPromptDimStyle)
}

// hideCopilot hides the copilot and resets any active copilot state.
func (m *Model) hideCopilot() {
	m.setCopilotVisible(false)
	if m.state == StateCopilot || m.state == StateAdjustCopilot {
		m.state = StateNormal
		m.copilotInput.TextInput().Blur()
	}
}

// showCopilotFocused shows and focuses the copilot from a hidden state.
func (m *Model) showCopilotFocused() {
	m.state = StateCopilot
	m.setCopilotVisible(true)
	m.copilotInput.Activate()
}

// --- Copilot exec functions ---

// execOpenCopilot opens or re-focuses the copilot. Never hides it.
// Tab behavior:
//   - Hidden → show + focus
//   - Unfocused → focus
//   - Focused → remains focused so Tab is available to slash completion
func execOpenCopilot(m *Model) (Model, tea.Cmd) {
	if m.state == StateCopilot {
		return *m, nil
	}
	if m.copilotVisible {
		// Visible but unfocused → re-focus.
		m.state = StateCopilot
		m.focusCopilot()
	} else {
		m.showCopilotFocused()
	}
	return *m, nil
}

// execToggleCopilot toggles copilot visibility (gc chord):
//   - Hidden → show + focus
//   - Visible (any focus) → hide
func execToggleCopilot(m *Model) (Model, tea.Cmd) {
	if m.copilotVisible {
		m.hideCopilot()
	} else {
		m.showCopilotFocused()
	}
	return *m, nil
}

// execSwitchCopilotMode toggles between float and docked mode.
func execSwitchCopilotMode(m *Model) (Model, tea.Cmd) {
	if m.copilotMode == CopilotModeFloat {
		m.copilotMode = CopilotModeDocked
	} else {
		m.copilotMode = CopilotModeFloat
	}
	savePrefString("copilotMode", m.copilotMode)
	m.applyLayout()
	cmd := m.setFlash("Lulu: "+m.copilotMode, false, 2*time.Second)
	return *m, cmd
}

// --- Key handlers ---

// handleKeyCopilot handles key events when the copilot chat panel is active.
func (m Model) handleKeyCopilot(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "tab":
		if strings.HasPrefix(m.copilotInput.Value(), "/") && m.copilotInput.CompleteSuggestion() {
			return m, nil
		}
		// Tab belongs to prompt completion; Escape is the only unfocus key.
		return m, nil

	case key.Matches(msg, Keys.Escape):
		m.unfocusCopilot()
		return m, nil

	case msg.String() == "enter":
		if !m.copilot.Streaming() {
			text := m.copilotInput.Value()
			if text == "/new" {
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				return m, m.clearCopilotHistory()
			}
			if text == "/preamble" {
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				return m, m.toggleCopilotPreamble()
			}
			if text == "/watch" {
				// Create a default reactive watch on the scoped session:
				// completed_turn → inspect_and_recommend, daemon defaults for
				// expiry/cooldown/firings (W7).
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				s, ok := m.currentSelectedSession()
				if !ok || s.SessionID == "" {
					m.copilot.AddInfoMessage("watch: no session selected")
					return m, nil
				}
				m.copilot.AddInfoMessage("watching " + s.DisplayName() + " (completed_turn → inspect_and_recommend)")
				return m, m.createWatch(s.SessionID, "completed_turn", "inspect_and_recommend", true)
			}
			if text == "/model" || strings.HasPrefix(text, "/model ") {
				modelID := strings.TrimSpace(strings.TrimPrefix(text, "/model"))
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				if modelID == "" {
					current := m.copilot.ModelID()
					if current == "" {
						current = "unknown"
					}
					m.copilot.AddInfoMessage("model: " + current)
					return m, nil
				}
				return m, m.setCopilotModel(modelID)
			}
			if text == "/mode" || strings.HasPrefix(text, "/mode ") {
				modeID := strings.TrimSpace(strings.TrimPrefix(text, "/mode"))
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				if modeID == "" {
					current := m.copilot.ModeID()
					if current == "" {
						current = "default"
					}
					hint := current
					if len(m.copilotModeIDs) > 0 {
						hint += " (available: " + strings.Join(m.copilotModeIDs, ", ") + ")"
					}
					m.copilot.AddInfoMessage("mode: " + hint)
					return m, nil
				}
				return m, m.setCopilotMode(modeID)
			}
			if text != "" {
				m.copilot.AddUserMessage(text)
				m.copilot.SetStreaming(true)
				m.copilot.ResetScroll()
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				// Mint the correlation id here (in the Update handler, not the async
				// cmd) so the stream filter is armed before any chunk can arrive.
				m.copilotRequestID = daemon.NewCorrelationID()
				cmd := m.sendCopilotChat(text, m.copilotRequestID)
				// Float mode: auto-unfocus after submit
				if m.copilotMode == CopilotModeFloat {
					m.unfocusCopilot()
				}
				return m, cmd
			}
		}
		return m, nil

	case msg.String() == "ctrl+c":
		if m.copilot.Streaming() {
			// Forget the in-flight request id so any late chunk still in flight
			// over the socket is dropped by the stream filter (belt to the
			// daemon's turn-epoch suspenders).
			m.copilotRequestID = ""
			return m, m.cancelCopilotChat()
		}
		m.unfocusCopilot()
		return m, nil

	case msg.String() == "ctrl+d":
		m.copilot.ScrollDown(5)
		return m, nil

	case msg.String() == "ctrl+u":
		m.copilot.ScrollUp(5)
		return m, nil

	case msg.String() == "shift+left":
		if m.copilotMode == CopilotModeDocked {
			m.copilotDockedW = max(m.copilotDockedW-5, minCopilotDockedW)
			savePrefInt("copilotDockedW", m.copilotDockedW)
			m.applyLayout()
			return m, nil
		}
		// Forward to input in float mode
		var cmd tea.Cmd
		*m.copilotInput.TextInput(), cmd = m.copilotInput.TextInput().Update(msg)
		return m, cmd

	case msg.String() == "shift+right":
		if m.copilotMode == CopilotModeDocked {
			m.copilotDockedW = min(m.copilotDockedW+5, m.innerWidth()/2)
			savePrefInt("copilotDockedW", m.copilotDockedW)
			m.applyLayout()
			return m, nil
		}
		var cmd tea.Cmd
		*m.copilotInput.TextInput(), cmd = m.copilotInput.TextInput().Update(msg)
		return m, cmd

	case msg.String() == "alt+\"":
		// Adjust mode: float only
		if m.copilotMode == CopilotModeFloat {
			m.state = StateAdjustCopilot
			m.copilotInput.TextInput().Blur()
			m.copilotInput.SetPromptStyle(ui.CopilotPromptDimStyle)
		}
		return m, nil

	default:
		// Forward to copilot input relay
		var cmd tea.Cmd
		*m.copilotInput.TextInput(), cmd = m.copilotInput.TextInput().Update(msg)
		return m, cmd
	}
}

// --- Copilot daemon RPCs ---

// sendCopilotChat fires off the copilot prompt to the daemon, carrying the
// per-prompt request id and the request-scoped selection captured now, at the
// originating client. Stream events arrive via the subscribe connection, not the
// RPC return.
func (m *Model) sendCopilotChat(message, requestID string) tea.Cmd {
	data := daemon.CopilotChatData{
		Message:   message,
		RequestID: requestID,
		Scope:     m.buildCopilotScope(),
	}
	return func() tea.Msg {
		if err := m.client.CopilotChat(data); err != nil {
			return CopilotStreamChunkMsg{RequestID: requestID, Msg: ui.CopilotStreamMsg{
				Type:    "error",
				Content: err.Error(),
			}}
		}
		return nil // stream events arrive via subscribe connection
	}
}

// buildCopilotScope snapshots the local UI attention state — the selected
// session (as a copy, not a name lookup), plus the active view/lane/project and
// the visible session ids — so the daemon can ground "review this" in exactly
// this session (spec Decisions 1, 2). Returns nil when nothing is selected, i.e.
// a fleet-wide request.
func (m *Model) buildCopilotScope() *daemon.CopilotScope {
	s, ok := m.currentSelectedSession()
	if !ok || s.SessionID == "" {
		return nil
	}
	scope := &daemon.CopilotScope{
		SelectedSessionID: s.SessionID,
		Selected: &daemon.CopilotScopeSession{
			SessionID:       s.SessionID,
			Provider:        string(s.Provider),
			Name:            s.DisplayName(),
			Project:         s.Project,
			GitBranch:       s.GitBranch,
			CWD:             s.CWD,
			Model:           s.Model,
			Status:          s.Status.String(),
			Note:            s.Note,
			Tags:            s.Tags,
			IsWaiting:       s.IsWaiting,
			HasOverlap:      s.HasOverlap,
			IsWorktree:      s.IsWorktree,
			WorktreeName:    s.WorktreeName,
			LastUserMessage: s.LastUserMessage,
		},
		ActiveProject:     s.Project,
		ActiveView:        m.viewMode,
		VisibleSessionIDs: m.visibleSessionIDs(),
	}
	return scope
}

// currentSelectedSession returns the session the originating client currently has
// highlighted, honoring the active view (work-queue strip vs sidebar list).
func (m *Model) currentSelectedSession() (claude.ClaudeSession, bool) {
	if m.viewMode == ViewWorkQueue {
		return m.workQueue.SelectedItem()
	}
	return m.sidebar.SelectedItem()
}

// visibleSessionIDs lists the session ids currently visible in the active view,
// so Lulu knows the set the user is looking at (bounded, ids only).
func (m *Model) visibleSessionIDs() []string {
	var items []claude.ClaudeSession
	if m.viewMode == ViewWorkQueue {
		items = m.workQueue.Items()
	} else {
		items = m.sidebar.Items()
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		if it.SessionID != "" {
			ids = append(ids, it.SessionID)
		}
	}
	return ids
}

// clearCopilotHistory tells the daemon to wipe history, then clears the local model.
func (m *Model) clearCopilotHistory() tea.Cmd {
	return func() tea.Msg {
		if err := m.client.CopilotClearHistory(); err != nil {
			return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
				Type:    "error",
				Content: "clear history: " + err.Error(),
			}}
		}
		return CopilotResetReadyMsg{}
	}
}

// toggleCopilotPreamble toggles live session injection and shows the new state.
func (m *Model) toggleCopilotPreamble() tea.Cmd {
	return func() tea.Msg {
		state, err := m.client.CopilotTogglePreamble()
		if err != nil {
			return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
				Type:    "error",
				Content: "toggle preamble: " + err.Error(),
			}}
		}
		m.copilot.AddInfoMessage("preamble: " + state)
		return nil
	}
}

func (m *Model) setCopilotModel(modelID string) tea.Cmd {
	return func() tea.Msg {
		models, err := m.client.CopilotSetModel(modelID)
		return CopilotModelReadyMsg{Models: models, Err: err}
	}
}

func (m *Model) setCopilotMode(modeID string) tea.Cmd {
	return func() tea.Msg {
		modes, err := m.client.CopilotSetMode(modeID)
		return CopilotModeReadyMsg{Modes: modes, Err: err}
	}
}

// --- Permission approval (StateCopilotConfirm) ---

// handleKeyCopilotConfirm handles keys while a Lulu permission prompt is pending.
// Option accelerators (y/a/n/N) select the matching option; esc denies; ctrl+c
// refuses outright. On any resolution the confirm UI is dismissed locally for
// responsiveness — the daemon's permission_resolved event lands the receipt line.
func (m Model) handleKeyCopilotConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	perm := m.copilotPermission
	if perm == nil {
		m.state = m.copilotPriorState
		return m, nil
	}
	key := msg.String()
	switch key {
	case "esc":
		if opt, ok := perm.DenyOption(); ok {
			return m.answerPermission(perm.PermissionID, opt.OptionID)
		}
		return m.answerPermission(perm.PermissionID, "") // refuse
	case "ctrl+c":
		return m.answerPermission(perm.PermissionID, "")
	default:
		if opt, ok := perm.OptionForKey(key); ok {
			return m.answerPermission(perm.PermissionID, opt.OptionID)
		}
	}
	return m, nil
}

// answerPermission dismisses the confirm UI, restores the prior state, and sends
// the chosen option id (or "" to refuse) to the daemon.
func (m Model) answerPermission(permissionID, optionID string) (tea.Model, tea.Cmd) {
	m.copilotPermission = nil
	m.state = m.copilotPriorState
	if m.state == StateCopilot {
		m.focusCopilot()
	}
	return m, func() tea.Msg {
		_ = m.client.CopilotPermissionAnswer(permissionID, optionID)
		return nil
	}
}

// toUIPermission converts the daemon wire payload into the ui-local view type.
func toUIPermission(p *daemon.CopilotPermissionRequest) ui.CopilotPermission {
	if p == nil {
		return ui.CopilotPermission{}
	}
	out := ui.CopilotPermission{
		PermissionID: p.PermissionID,
		ToolCallID:   p.ToolCallID,
		Title:        p.Title,
		Kind:         p.Kind,
		Command:      p.Command,
		Sensitive:    p.Sensitive,
		SensitiveHit: p.SensitiveHit,
		DeadlineUnix: p.DeadlineUnix,
	}
	for _, d := range p.Diffs {
		out.Diffs = append(out.Diffs, ui.CopilotPermissionDiff{Path: d.Path, OldText: d.OldText, NewText: d.NewText})
	}
	for _, s := range p.BatchSteps {
		out.BatchSteps = append(out.BatchSteps, ui.CopilotPermissionBatchStep{Index: s.Index, Op: s.Op, Target: s.Target, Detail: s.Detail, Risk: s.Risk})
	}
	for _, o := range p.Options {
		out.Options = append(out.Options, ui.CopilotPermissionOption{OptionID: o.OptionID, Kind: o.Kind, Name: o.Name, Key: o.Key})
	}
	return out
}

// permissionReceiptLine formats the transcript receipt for a resolved permission.
func permissionReceiptLine(msg CopilotPermissionResolvedMsg) string {
	target := msg.Title
	if target == "" {
		target = msg.Kind
	}
	switch msg.Status {
	case "approved":
		return "✔ approved " + target
	case "expired":
		return "✘ auto-denied (timed out) " + target
	case "cancelled":
		return "✘ cancelled " + target
	default:
		return "✘ denied " + target
	}
}

func (m *Model) applyCopilotModelState(models daemon.CopilotModelState) {
	suggestions := []string{"/new", "/preamble", "/model", "/mode", "/watch"}
	for _, model := range models.AvailableModels {
		if model.ModelID == "" {
			continue
		}
		suggestions = append(suggestions, "/model "+model.ModelID)
	}
	for _, id := range m.copilotModeIDs {
		suggestions = append(suggestions, "/mode "+id)
	}
	m.copilot.SetModelID(models.CurrentModelID)
	m.copilotInput.SetSuggestions(suggestions)
}

// applyCopilotModeState records the advertised session modes (for /mode
// completion) and the current mode shown in the panel title.
func (m *Model) applyCopilotModeState(modes daemon.CopilotModeState) {
	m.copilotModeIDs = m.copilotModeIDs[:0]
	for _, mode := range modes.AvailableModes {
		if mode.ID != "" {
			m.copilotModeIDs = append(m.copilotModeIDs, mode.ID)
		}
	}
	m.copilot.SetModeID(modes.CurrentModeID)
}

// cancelCopilotChat cancels the in-flight copilot prompt.
func (m *Model) cancelCopilotChat() tea.Cmd {
	return func() tea.Msg {
		_ = m.client.CopilotCancel()
		return nil
	}
}

// --- Adjust mode (float only) ---

// handleKeyAdjustCopilot handles key events in StateAdjustCopilot (resize/reposition mode).
func (m Model) handleKeyAdjustCopilot(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		m.copilotOffY--
		savePrefInt("copilotOffY", m.copilotOffY)
	case "down":
		m.copilotOffY++
		savePrefInt("copilotOffY", m.copilotOffY)
	case "left":
		m.copilotOffX--
		savePrefInt("copilotOffX", m.copilotOffX)
	case "right":
		m.copilotOffX++
		savePrefInt("copilotOffX", m.copilotOffX)
	case "shift+left":
		m.copilotDW -= 5
		savePrefInt("copilotDW", m.copilotDW)
	case "shift+right":
		m.copilotDW += 5
		savePrefInt("copilotDW", m.copilotDW)
	case "shift+up":
		m.copilotDH += 3
		savePrefInt("copilotDH", m.copilotDH)
	case "shift+down":
		m.copilotDH -= 3
		savePrefInt("copilotDH", m.copilotDH)
	case "r":
		m.copilotOffX, m.copilotOffY, m.copilotDW, m.copilotDH = 0, 0, 0, 0
		savePrefInt("copilotOffX", 0)
		savePrefInt("copilotOffY", 0)
		savePrefInt("copilotDW", 0)
		savePrefInt("copilotDH", 0)
	case "esc", "enter":
		m.state = StateCopilot
		m.focusCopilot()
	}
	return m, nil
}
