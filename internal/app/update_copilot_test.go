package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/ui"
)

// Regression: the copilot snapshot the daemon pushes on subscribe (right after
// the initial session snapshot) must hydrate the copilot panel WITHOUT clearing
// the session list. Before the fix it fell through the subscribe read loop's
// default branch, was decoded as an (empty) SessionsData, and blanked the
// sidebar to "No coding sessions found" for a poll cycle.
func TestCopilotSnapshotDoesNotWipeSessions(t *testing.T) {
	m := Model{
		copilot:  ui.NewCopilotModel(),
		sessions: []agent.Session{{PaneID: "p1"}, {PaneID: "p2"}},
	}

	updated, cmd := m.Update(CopilotSnapshotReadyMsg{Snapshot: daemon.CopilotSnapshotData{
		History:   []daemon.CopilotHistoryMsg{{Role: "user", Content: "hi"}},
		SessionID: "sess-abc",
	}})
	got := updated.(Model)

	if len(got.sessions) != 2 {
		t.Fatalf("copilot snapshot wiped session list: got %d sessions, want 2", len(got.sessions))
	}
	if len(got.copilot.Messages()) != 1 {
		t.Fatalf("copilot snapshot did not hydrate history: got %d messages", len(got.copilot.Messages()))
	}
	if got.copilot.SessionID() != "sess-abc" {
		t.Fatalf("copilot snapshot did not set session id: got %q", got.copilot.SessionID())
	}
	if cmd == nil {
		t.Fatal("copilot snapshot handler must re-arm the daemon read loop")
	}
}

func TestCopilotSlashCompletionKeepsInputFocused(t *testing.T) {
	m := Model{
		state:        StateCopilot,
		copilotInput: ui.NewCopilotRelayModel(),
	}
	m.copilotInput.Activate()
	m.copilotInput.TextInput().SetValue("/n")

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)

	if got.state != StateCopilot {
		t.Fatalf("state = %v, want StateCopilot", got.state)
	}
	if value := got.copilotInput.Value(); value != "/new" {
		t.Fatalf("completed value = %q, want /new", value)
	}
}

func TestTabWithoutSlashKeepsCopilotFocused(t *testing.T) {
	m := Model{
		state:          StateCopilot,
		copilotVisible: true,
		copilotInput:   ui.NewCopilotRelayModel(),
	}
	m.copilotInput.Activate()

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)

	if got.state != StateCopilot || !got.copilotVisible {
		t.Fatalf("Tab changed focused Lulu state: state=%v visible=%v", got.state, got.copilotVisible)
	}
}

func TestTabFocusesVisibleCopilot(t *testing.T) {
	m := Model{
		state:          StateNormal,
		copilotVisible: true,
		copilotInput:   ui.NewCopilotRelayModel(),
	}

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)

	if got.state != StateCopilot {
		t.Fatalf("Tab state = %v, want StateCopilot", got.state)
	}
}

func TestEscapeUnfocusesCopilotWithoutHiding(t *testing.T) {
	m := Model{
		state:          StateCopilot,
		copilotVisible: true,
		copilotInput:   ui.NewCopilotRelayModel(),
	}
	m.copilotInput.Activate()

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)

	if got.state != StateNormal || !got.copilotVisible {
		t.Fatalf("Escape state=%v visible=%v, want unfocused but visible", got.state, got.copilotVisible)
	}
}

func TestFloatingCopilotMouseDragMovesOverlay(t *testing.T) {
	m := Model{
		width:             120,
		height:            40,
		ready:             true,
		inFullscreenPopup: true,
		copilotVisible:    true,
		copilotMode:       CopilotModeFloat,
		copilotInput:      ui.NewCopilotRelayModel(),
	}
	m.copilotInput.Activate()
	row, col, _, _ := m.copilotFloatBounds()
	press := tea.MouseMsg{X: col + 3, Y: row + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled || started.copilotDragMode != copilotDragMove {
		t.Fatalf("title press did not start floating move: handled=%v mode=%v", handled, started.copilotDragMode)
	}

	moved, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X - 7, Y: press.Y + 3, Action: tea.MouseActionMotion})
	got := moved.(Model)
	if got.copilotOffX != -7 || got.copilotOffY != 3 {
		t.Fatalf("offset = (%d,%d), want (-7,3)", got.copilotOffX, got.copilotOffY)
	}
}

func floatingCopilotWithHistory() Model {
	m := Model{
		width:             120,
		height:            40,
		ready:             true,
		inFullscreenPopup: true,
		copilotVisible:    true,
		copilotMode:       CopilotModeFloat,
		copilot:           ui.NewCopilotModel(),
		copilotInput:      ui.NewCopilotRelayModel(),
	}
	m.copilotInput.Activate()
	for range 12 {
		m.copilot.AddUserMessage("resize fixture")
	}
	return m
}

func TestFloatingCopilotTopBorderMovesVertically(t *testing.T) {
	m := floatingCopilotWithHistory()
	row, col, width, height := m.copilotFloatBounds()
	press := tea.MouseMsg{X: col + width/2, Y: row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled || started.copilotDragMode != copilotDragMove {
		t.Fatalf("top border did not start move: handled=%v mode=%v", handled, started.copilotDragMode)
	}
	moved, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X, Y: press.Y + 3, Action: tea.MouseActionMotion})
	got := moved.(Model)
	newRow, newCol, newWidth, newHeight := got.copilotFloatBounds()

	if newWidth != width || newHeight != height {
		t.Fatalf("top-border move changed size: %dx%d → %dx%d", width, height, newWidth, newHeight)
	}
	if newRow != row+3 || newCol != col {
		t.Fatalf("top-border geometry row/col = %d/%d, want %d/%d", newRow, newCol, row+3, col)
	}
}

func TestFloatingCopilotBottomBorderResizesHeightOnly(t *testing.T) {
	m := floatingCopilotWithHistory()
	row, col, width, height := m.copilotFloatBounds()
	press := tea.MouseMsg{X: col + width/2, Y: row + height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled || started.copilotDragMode != copilotDragResizeFloat {
		t.Fatalf("bottom border did not start resize: handled=%v mode=%v", handled, started.copilotDragMode)
	}
	resized, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X, Y: press.Y - 3, Action: tea.MouseActionMotion})
	got := resized.(Model)
	newRow, _, newWidth, newHeight := got.copilotFloatBounds()

	if newWidth != width {
		t.Fatalf("bottom-border drag changed width: %d → %d", width, newWidth)
	}
	if newHeight != height-3 || newRow != row {
		t.Fatalf("bottom-border geometry row/height = %d/%d, want %d/%d", newRow, newHeight, row, height-3)
	}
}

func TestFloatingCopilotBottomBorderExpandsHeight(t *testing.T) {
	m := floatingCopilotWithHistory()
	row, col, width, height := m.copilotFloatBounds()
	press := tea.MouseMsg{X: col + width/2, Y: row + height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled || started.copilotDragMode != copilotDragResizeFloat {
		t.Fatalf("bottom border did not start resize: handled=%v mode=%v", handled, started.copilotDragMode)
	}
	resized, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X, Y: press.Y + 3, Action: tea.MouseActionMotion})
	got := resized.(Model)
	newRow, _, newWidth, newHeight := got.copilotFloatBounds()

	if newWidth != width {
		t.Fatalf("bottom-border drag changed width: %d → %d", width, newWidth)
	}
	if newHeight != height+3 || newRow != row {
		t.Fatalf("bottom-border geometry row/height = %d/%d, want %d/%d", newRow, newHeight, row, height+3)
	}
}

func TestFloatingCopilotBottomBorderResizeThroughUpdateAtPersistedMinimum(t *testing.T) {
	m := floatingCopilotWithHistory()
	m.copilotDH = -59
	m.copilotOffY = 15
	row, col, width, height := m.copilotFloatBounds()
	press := tea.MouseMsg{X: col + width/2, Y: row + height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	pressed, _ := m.Update(press)
	started := pressed.(Model)
	if started.copilotDragMode != copilotDragResizeFloat {
		t.Fatalf("bottom border press through Update started mode=%v", started.copilotDragMode)
	}
	moved, _ := started.Update(tea.MouseMsg{X: press.X, Y: press.Y + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	got := moved.(Model)
	_, _, newWidth, newHeight := got.copilotFloatBounds()

	if newWidth != width || newHeight != height+3 {
		t.Fatalf("bottom resize through Update size = %dx%d, want %dx%d", newWidth, newHeight, width, height+3)
	}
}

func TestFloatingCopilotBoundsMatchRenderedBorder(t *testing.T) {
	m := NewModel(nil)
	m.width, m.height, m.ready = 249, 78, true
	m.inFullscreenPopup = true
	m.copilotVisible = true
	m.copilotMode = CopilotModeFloat
	m.copilotOffX, m.copilotOffY = -50, 10
	m.copilotDW, m.copilotDH = -10, -59
	m.copilotInput.Activate()
	m.applyLayout()

	lines := strings.Split(ansi.Strip(m.View()), "\n")
	titleRow := -1
	for i, line := range lines {
		if strings.Contains(line, "Lulu") {
			titleRow = i
			break
		}
	}
	if titleRow < 1 {
		t.Fatalf("rendered Lulu title not found in %q", lines[:min(len(lines), 8)])
	}
	topRow := titleRow - 1
	bottomRow := -1
	for i := titleRow + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], "╰") {
			bottomRow = i
			break
		}
	}
	if bottomRow < 0 {
		t.Fatal("rendered Lulu bottom border not found")
	}
	topIndex := strings.Index(lines[topRow], "╭")
	leftCol := ansi.StringWidth(lines[topRow][:topIndex])
	rightIndex := strings.Index(lines[bottomRow], "╯")
	rightCol := ansi.StringWidth(lines[bottomRow][:rightIndex])

	row, col, width, height := m.copilotFloatBounds()
	if row != topRow || col != leftCol || row+height-1 != bottomRow || col+width-1 != rightCol {
		t.Fatalf("bounds row/col/right/bottom = %d/%d/%d/%d, rendered = %d/%d/%d/%d", row, col, col+width-1, row+height-1, topRow, leftCol, rightCol, bottomRow)
	}
}

func TestFloatingCopilotTopCornerResizesBothAxes(t *testing.T) {
	m := floatingCopilotWithHistory()
	row, col, width, height := m.copilotFloatBounds()
	press := tea.MouseMsg{X: col + width - 1, Y: row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled || started.copilotDragMode != copilotDragResizeFloat {
		t.Fatalf("top corner did not start resize: handled=%v mode=%v", handled, started.copilotDragMode)
	}
	resized, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X - 6, Y: press.Y + 4, Action: tea.MouseActionMotion})
	got := resized.(Model)
	newRow, newCol, newWidth, newHeight := got.copilotFloatBounds()

	if newWidth != width-6 || newHeight != height-4 || newRow != row+4 || newCol != col {
		t.Fatalf("top-corner geometry row/col/width/height = %d/%d/%d/%d, want %d/%d/%d/%d", newRow, newCol, newWidth, newHeight, row+4, col, width-6, height-4)
	}
}

func TestFloatingCopilotMouseDragResizesFromCorner(t *testing.T) {
	m := floatingCopilotWithHistory()
	row, col, width, height := m.copilotFloatBounds()
	press := tea.MouseMsg{X: col + width - 1, Y: row + height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled || started.copilotDragMode != copilotDragResizeFloat {
		t.Fatalf("corner press did not start floating resize: handled=%v mode=%v", handled, started.copilotDragMode)
	}

	resized, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X - 6, Y: press.Y - 4, Action: tea.MouseActionMotion})
	got := resized.(Model)
	newRow, newCol, newWidth, newHeight := got.copilotFloatBounds()
	if newWidth != width-6 || newHeight != height-4 || newRow != row || newCol != col {
		t.Fatalf("corner geometry row/col/width/height = %d/%d/%d/%d, want %d/%d/%d/%d", newRow, newCol, newWidth, newHeight, row, col, width-6, height-4)
	}
}

func TestDockedCopilotMouseDragResizesDivider(t *testing.T) {
	m := Model{
		width:             140,
		height:            40,
		ready:             true,
		inFullscreenPopup: true,
		copilotVisible:    true,
		copilotMode:       CopilotModeDocked,
		copilotDockedW:    70,
	}
	edge := m.innerWidth() - m.copilotDockedWidth()
	press := tea.MouseMsg{X: edge, Y: contentStartRow + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled || started.copilotDragMode != copilotDragResizeDocked {
		t.Fatalf("divider press did not start docked resize: handled=%v mode=%v", handled, started.copilotDragMode)
	}

	resized, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X + 10, Y: press.Y, Action: tea.MouseActionMotion})
	got := resized.(Model)
	if got.copilotDockedW != 60 {
		t.Fatalf("docked width = %d, want 60", got.copilotDockedW)
	}
}

func TestDockedCopilotMouseDragStartsFromRenderedClampedWidth(t *testing.T) {
	m := Model{
		width:             100,
		height:            40,
		inFullscreenPopup: true,
		copilotVisible:    true,
		copilotMode:       CopilotModeDocked,
		copilotDockedW:    70, // persisted width; rendered width is clamped to 50
	}
	edge := m.innerWidth() - m.copilotDockedWidth()
	press := tea.MouseMsg{X: edge, Y: contentStartRow + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

	started, handled := m.beginCopilotMouseDrag(press)
	if !handled {
		t.Fatal("divider press was not handled")
	}
	resized, _ := started.handleCopilotDragMotion(tea.MouseMsg{X: press.X + 10, Y: press.Y, Action: tea.MouseActionMotion})
	if got := resized.(Model).copilotDockedW; got != 40 {
		t.Fatalf("docked width = %d, want 40 from rendered width 50", got)
	}
}

func TestApplyLayoutFastUsesWorkQueueGeometry(t *testing.T) {
	m := Model{
		width:             140,
		height:            40,
		inFullscreenPopup: true,
		viewMode:          ViewWorkQueue,
		copilotVisible:    true,
		copilotMode:       CopilotModeDocked,
		copilotDockedW:    50,
		sidebarWidthPct:   25,
		sidebar:           ui.NewSidebarModel(),
		detail:            ui.NewDetailModel(),
	}

	m.applyLayoutFast()

	if got, want := m.detail.Width(), m.innerWidth()-m.copilotDockedWidth(); got != want {
		t.Fatalf("work queue detail width = %d, want %d", got, want)
	}
}

func TestLateCopilotStatusDoesNotOverwriteStreamSession(t *testing.T) {
	m := Model{copilot: ui.NewCopilotModel()}

	streamed, _ := m.Update(CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{Type: "session", Content: "new-session"}})
	staleStatus, _ := streamed.(Model).Update(CopilotStatusReadyMsg{Status: &daemon.CopilotStatusData{SessionID: "stale-session"}})
	got := staleStatus.(Model)

	if sessionID := got.copilot.SessionID(); sessionID != "new-session" {
		t.Fatalf("late status overwrote stream session: %q", sessionID)
	}
}

func TestCopilotResetReadyClearsHistoryAndSession(t *testing.T) {
	m := Model{copilot: ui.NewCopilotModel()}
	m.copilot.SetSessionID("old-session")
	m.copilot.AddUserMessage("keep only on failed reset")

	updated, _ := m.Update(CopilotResetReadyMsg{})
	got := updated.(Model)

	if got.copilot.SessionID() != "" {
		t.Fatalf("session ID after confirmed reset = %q", got.copilot.SessionID())
	}
	if len(got.copilot.Messages()) != 0 {
		t.Fatalf("history after confirmed reset has %d messages", len(got.copilot.Messages()))
	}
	if !got.copilotSessionKnown {
		t.Fatal("confirmed reset must block stale startup status")
	}
}

func TestCopilotStatusAppliesEffectiveModelAndCompletions(t *testing.T) {
	m := Model{copilot: ui.NewCopilotModel(), copilotInput: ui.NewCopilotRelayModel()}
	models := daemon.CopilotModelState{
		CurrentModelID: "openai-codex:gpt-5.4",
		AvailableModels: []daemon.CopilotModelInfo{
			{ModelID: "openai-codex:gpt-5.4", Name: "gpt-5.4"},
			{ModelID: "openai-codex:gpt-5.4-mini", Name: "gpt-5.4-mini"},
		},
	}
	updated, _ := m.Update(CopilotStatusReadyMsg{Status: &daemon.CopilotStatusData{Models: models}})
	got := updated.(Model)

	if got.copilot.ModelID() != models.CurrentModelID {
		t.Fatalf("effective model = %q, want %q", got.copilot.ModelID(), models.CurrentModelID)
	}
	got.copilotInput.Activate()
	got.copilotInput.TextInput().SetValue("/model openai-codex:gpt-5.4-m")
	if !got.copilotInput.CompleteSuggestion() || got.copilotInput.Value() != "/model openai-codex:gpt-5.4-mini" {
		t.Fatalf("dynamic model completion = %q", got.copilotInput.Value())
	}
}

func TestCopilotModelChangesOnlyAfterAcknowledgement(t *testing.T) {
	m := Model{copilot: ui.NewCopilotModel(), copilotInput: ui.NewCopilotRelayModel()}
	m.copilot.SetModelID("openrouter:old")

	failed, _ := m.Update(CopilotModelReadyMsg{Err: assertError("denied")})
	failedModel := failed.(Model)
	if got := failedModel.copilot.ModelID(); got != "openrouter:old" {
		t.Fatalf("failed switch changed model to %q", got)
	}

	models := daemon.CopilotModelState{CurrentModelID: "anthropic:claude-sonnet-4"}
	succeeded, _ := failedModel.Update(CopilotModelReadyMsg{Models: models})
	succeededModel := succeeded.(Model)
	if got := succeededModel.copilot.ModelID(); got != models.CurrentModelID {
		t.Fatalf("acknowledged model = %q, want %q", got, models.CurrentModelID)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }

// buildCopilotScope must capture the session the originating client currently has
// selected — a copy, not a name lookup — so the daemon can ground "review this".
func TestBuildCopilotScopeCapturesSelection(t *testing.T) {
	m := Model{viewMode: ViewWorkQueue}
	sel := agent.Session{
		SessionID:    "sess-1",
		Provider:     agent.ProviderClaude,
		Status:       agent.StatusUserTurn,
		Project:      "spirit",
		GitBranch:    "main",
		CWD:          "/src/spirit",
		FirstMessage: "review the parser change",
		Note:         "needs a test",
		Tags:         []string{"review"},
	}
	m.workQueue.SetItems([]agent.Session{sel}, "")

	scope := m.buildCopilotScope()
	if scope == nil {
		t.Fatal("scope is nil despite a selected session")
	}
	if scope.SelectedSessionID != "sess-1" {
		t.Fatalf("selected session id = %q, want sess-1", scope.SelectedSessionID)
	}
	if scope.Selected == nil || scope.Selected.Project != "spirit" || scope.Selected.Note != "needs a test" {
		t.Fatalf("scope snapshot did not copy the selection: %+v", scope.Selected)
	}
	if scope.ActiveView != ViewWorkQueue {
		t.Fatalf("active view = %q, want %q", scope.ActiveView, ViewWorkQueue)
	}
	if len(scope.VisibleSessionIDs) != 1 || scope.VisibleSessionIDs[0] != "sess-1" {
		t.Fatalf("visible session ids = %v, want [sess-1]", scope.VisibleSessionIDs)
	}
}

// With nothing selected the request is fleet-wide: scope must be nil.
func TestBuildCopilotScopeNilWhenNoSelection(t *testing.T) {
	m := Model{viewMode: ViewSidebar, sidebar: ui.NewSidebarModel()}
	if scope := m.buildCopilotScope(); scope != nil {
		t.Fatalf("scope should be nil with no selection, got %+v", scope)
	}
}

// A stream chunk whose request id no longer matches the in-flight turn (cancelled
// or superseded) must be dropped on the client side and never touch the panel.
func TestLateCopilotChunkDroppedAfterSupersede(t *testing.T) {
	m := Model{copilot: ui.NewCopilotModel()}
	m.copilotRequestID = "req-2"

	stale, _ := m.Update(CopilotStreamChunkMsg{RequestID: "req-1", Msg: ui.CopilotStreamMsg{Type: "text_delta", Content: "STALE"}})
	got := stale.(Model)
	for _, msg := range got.copilot.Messages() {
		if strings.Contains(msg.Content, "STALE") {
			t.Fatalf("late chunk from a superseded request leaked into the panel: %q", msg.Content)
		}
	}

	fresh, _ := got.Update(CopilotStreamChunkMsg{RequestID: "req-2", Msg: ui.CopilotStreamMsg{Type: "text_delta", Content: "LIVE"}})
	freshModel := fresh.(Model)
	found := false
	for _, msg := range freshModel.copilot.Messages() {
		if strings.Contains(msg.Content, "LIVE") {
			found = true
		}
	}
	if !found {
		t.Fatal("matching chunk for the in-flight request was dropped")
	}
}

// A forwarded permission request opens the confirm state and stores the payload;
// answering an option restores the prior state and clears the prompt.
func TestCopilotPermissionOpensConfirmAndAnswers(t *testing.T) {
	m := Model{
		state:          StateNormal,
		copilot:        ui.NewCopilotModel(),
		copilotInput:   ui.NewCopilotRelayModel(),
		copilotVisible: true,
	}
	perm := ui.CopilotPermission{
		PermissionID: "perm-1",
		Title:        "Approve edit: main.go",
		Kind:         "edit",
		Diffs:        []ui.CopilotPermissionDiff{{Path: "main.go", OldText: "a\n", NewText: "b\n"}},
		Options: []ui.CopilotPermissionOption{
			{OptionID: "allow_once", Kind: "allow_once", Name: "Allow edit", Key: "y"},
			{OptionID: "deny", Kind: "reject_once", Name: "Deny", Key: "n"},
		},
		DeadlineUnix: 9999999999,
	}
	opened, cmd := m.Update(CopilotPermissionMsg{RequestID: "", Permission: perm})
	got := opened.(Model)
	if got.state != StateCopilotConfirm {
		t.Fatalf("state = %v, want StateCopilotConfirm", got.state)
	}
	if got.copilotPermission == nil || got.copilotPermission.PermissionID != "perm-1" {
		t.Fatalf("pending permission not stored: %+v", got.copilotPermission)
	}
	if got.copilotPriorState != StateNormal {
		t.Fatalf("prior state = %v, want StateNormal", got.copilotPriorState)
	}
	if cmd == nil {
		t.Fatal("permission handler must re-arm the daemon read loop")
	}

	// Press 'y' to allow: clears the prompt and restores the prior state.
	answered, ansCmd := got.handleKeyCopilotConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	am := answered.(Model)
	if am.copilotPermission != nil {
		t.Fatal("permission not cleared after answering")
	}
	if am.state != StateNormal {
		t.Fatalf("state after answer = %v, want StateNormal", am.state)
	}
	if ansCmd == nil {
		t.Fatal("answering should dispatch the answer RPC command")
	}
}

// The resolved event dismisses the confirm UI and drops a receipt line into the
// transcript (e.g. resolved out-of-band by timeout or cancellation).
func TestCopilotPermissionResolvedDismissesAndReceipts(t *testing.T) {
	m := Model{
		state:        StateCopilotConfirm,
		copilot:      ui.NewCopilotModel(),
		copilotInput: ui.NewCopilotRelayModel(),
	}
	perm := ui.CopilotPermission{PermissionID: "perm-9", Title: "edit X", Kind: "edit"}
	m.copilotPermission = &perm
	m.copilotPriorState = StateCopilot

	updated, _ := m.Update(CopilotPermissionResolvedMsg{
		PermissionID: "perm-9", Status: "expired", Title: "edit X", Kind: "edit",
	})
	got := updated.(Model)
	if got.copilotPermission != nil {
		t.Fatal("resolved event did not dismiss the pending permission")
	}
	msgs := got.copilot.Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "auto-denied") {
		t.Fatalf("receipt line = %#v", msgs)
	}
}

// The confirm overlay renders the tool title, the diff, the options with keys, and
// a countdown — a render smoke test.
func TestCopilotPermissionOverlayRenders(t *testing.T) {
	perm := ui.CopilotPermission{
		PermissionID: "p",
		Title:        "Approve edit: internal/app/main.go",
		Kind:         "edit",
		Diffs:        []ui.CopilotPermissionDiff{{Path: "internal/app/main.go", OldText: "old line\n", NewText: "new line\n"}},
		Options: []ui.CopilotPermissionOption{
			{OptionID: "allow_once", Kind: "allow_once", Name: "Allow edit", Key: "y"},
			{OptionID: "deny", Kind: "reject_once", Name: "Deny", Key: "n"},
		},
		DeadlineUnix: time.Now().Add(42 * time.Second).Unix(),
	}
	out := ansi.Strip(ui.RenderCopilotPermission(perm, 70, time.Now()))
	for _, want := range []string{"Approve edit", "internal/app/main.go", "new line", "Allow edit", "Deny", "auto-deny in"} {
		if !strings.Contains(out, want) {
			t.Fatalf("permission overlay missing %q:\n%s", want, out)
		}
	}
}

func TestCopilotModelCommandReportsCurrentWithoutSendingPrompt(t *testing.T) {
	m := Model{
		state:        StateCopilot,
		copilot:      ui.NewCopilotModel(),
		copilotInput: ui.NewCopilotRelayModel(),
	}
	m.copilot.SetModelID("openai-codex:gpt-5.4")
	m.copilotInput.Activate()
	m.copilotInput.TextInput().SetValue("/model")

	updated, cmd := m.handleKeyCopilot(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("/model without an argument unexpectedly returned an RPC command")
	}
	messages := got.copilot.Messages()
	if len(messages) != 1 || messages[0].Role != "info" || messages[0].Content != "model: openai-codex:gpt-5.4" {
		t.Fatalf("/model messages = %#v", messages)
	}
}
