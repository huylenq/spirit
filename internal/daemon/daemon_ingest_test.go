package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/ledger"
)

// newIngestDaemon builds a minimal daemon with a real ledger over a temp
// status dir, so patchSession/observeFleet exercise the actual ingest paths
// (including their file-truth confirmations) without touching ~/.spirit.
func newIngestDaemon(t *testing.T) *Daemon {
	t.Helper()
	dir := t.TempDir()
	restore := claude.OverrideStatusDirForTest(dir)
	t.Cleanup(restore)

	led, err := ledger.Open(filepath.Join(dir, "ledger"), 0)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	return &Daemon{
		subscribers:        make(map[*subscriber]struct{}),
		copilotPerms:       make(map[string]*pendingPermission),
		commitDonePanes:    make(map[string]commitDoneEntry),
		queuePanes:         make(map[string][]string),
		synthesizingPanes:  make(map[string]bool),
		pendingPromptPanes: make(map[string]pendingPromptEntry),
		orchestratorIDs:    make(map[string]bool),
		lastSynthTime:      make(map[string]time.Time),
		overlapPanes:       make(map[string]bool),
		perception:         led,
		ledgerBaselined:    true,
		autoSynthDisabled:  true, // synthesis goroutines must not outlive the test
	}
}

func signalCount(d *Daemon, kind ledger.SignalKind) int {
	n := 0
	for _, sig := range d.perception.Signals() {
		if sig.Kind == kind {
			n++
		}
	}
	return n
}

func ledgerItems(d *Daemon) []ledger.AttentionItem { return d.perception.Items() }

func boolp(v bool) *bool { return &v }

func TestPatchSessionWaitingRisingAndFallingEdge(t *testing.T) {
	d := newIngestDaemon(t)
	d.sessions = []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusAgentTurn}}

	// Rising edge: the Notification hook writes status+waiting files, then nudges.
	claude.WriteStatus("s1", agent.StatusUserTurn) //nolint:errcheck
	claude.WriteWaiting("s1", "permission_prompt")
	if res := d.patchSession(NudgeData{PaneID: "%1", SessionID: "s1", Status: "user-turn", IsWaiting: boolp(true)}); res != patchApplied {
		t.Fatalf("patch result = %v", res)
	}
	if got := signalCount(d, ledger.SignalWaitingInput); got != 1 {
		t.Fatalf("waiting_input signals = %d, want 1", got)
	}
	items := ledgerItems(d)
	if len(items) != 1 || items[0].Category != ledger.CategoryNeedsDecision || items[0].Status != ledger.StatusOpen {
		t.Fatalf("unexpected items: %+v", items)
	}

	// A redundant waiting nudge (same state) must not re-signal.
	if res := d.patchSession(NudgeData{PaneID: "%1", SessionID: "s1", Status: "user-turn", IsWaiting: boolp(true)}); res != patchDeduped {
		t.Fatalf("redundant nudge result = %v, want deduped", res)
	}
	if got := signalCount(d, ledger.SignalWaitingInput); got != 1 {
		t.Fatalf("waiting_input re-signaled on redundant nudge")
	}

	// Falling edge: the user answered — PreToolUse clears the waiting file and
	// re-asserts agent-turn; the item resolves.
	claude.RemoveWaiting("s1")
	claude.WriteStatus("s1", agent.StatusAgentTurn) //nolint:errcheck
	if res := d.patchSession(NudgeData{PaneID: "%1", SessionID: "s1", Status: "agent-turn", IsWaiting: boolp(false)}); res != patchApplied {
		t.Fatalf("patch result = %v", res)
	}
	items = ledgerItems(d)
	if items[0].Status != ledger.StatusResolved {
		t.Fatalf("waiting item not resolved on falling edge: %+v", items[0])
	}
}

func TestPatchSessionTurnCompleted(t *testing.T) {
	d := newIngestDaemon(t)
	d.sessions = []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusAgentTurn}}

	claude.WriteStatus("s1", agent.StatusUserTurn) //nolint:errcheck
	if res := d.patchSession(NudgeData{PaneID: "%1", SessionID: "s1", Status: "user-turn"}); res != patchApplied {
		t.Fatalf("patch result = %v", res)
	}
	if got := signalCount(d, ledger.SignalTurnCompleted); got != 1 {
		t.Fatalf("turn_completed signals = %d, want 1", got)
	}
	items := ledgerItems(d)
	if len(items) != 1 || items[0].Category != ledger.CategoryVerifyClaim {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestPatchSessionWaitingPauseIsNotTurnCompletion(t *testing.T) {
	d := newIngestDaemon(t)
	d.sessions = []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusAgentTurn}}

	// A permission Notification flips to user-turn WITH IsWaiting — that is a
	// mid-turn pause, not a completed turn.
	claude.WriteStatus("s1", agent.StatusUserTurn) //nolint:errcheck
	claude.WriteWaiting("s1", "permission_prompt")
	d.patchSession(NudgeData{PaneID: "%1", SessionID: "s1", Status: "user-turn", IsWaiting: boolp(true)})
	if got := signalCount(d, ledger.SignalTurnCompleted); got != 0 {
		t.Fatalf("waiting pause signaled turn_completed")
	}
}

func TestObserveFleetIdlePollsDoNotResignal(t *testing.T) {
	d := newIngestDaemon(t)
	claude.WriteStatus("s1", agent.StatusUserTurn) //nolint:errcheck

	working := []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusAgentTurn}}
	idle := []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusUserTurn}}

	d.observeFleet(working, idle) // the completion edge
	if got := signalCount(d, ledger.SignalTurnCompleted); got != 1 {
		t.Fatalf("turn_completed = %d, want 1", got)
	}

	// 300 idle polls: no edges, no signals.
	for i := 0; i < 300; i++ {
		d.observeFleet(idle, idle)
	}
	// The same edge observed again (poll/nudge race replay): the content-derived
	// anchor (status mtime, unchanged) dedups it.
	d.observeFleet(working, idle)
	if got := signalCount(d, ledger.SignalTurnCompleted); got != 1 {
		t.Fatalf("idle polls re-signaled: turn_completed = %d, want 1", got)
	}
}

func TestObserveFleetCodexTurnAnchorUsesTurnID(t *testing.T) {
	d := newIngestDaemon(t)
	claude.WriteSessionMeta(claude.SessionMeta{Provider: agent.ProviderCodex, SessionID: "cx1", TurnID: "turn-42"}) //nolint:errcheck
	claude.WriteStatus("cx1", agent.StatusUserTurn)                                                                 //nolint:errcheck

	working := []agent.Session{{SessionID: "cx1", PaneID: "%1", Provider: agent.ProviderCodex, Status: agent.StatusAgentTurn}}
	idle := []agent.Session{{SessionID: "cx1", PaneID: "%1", Provider: agent.ProviderCodex, Status: agent.StatusUserTurn}}

	d.observeFleet(working, idle)
	// Rewrite the status file (new mtime); the turn-id anchor must still dedup
	// a replay of the same turn.
	time.Sleep(5 * time.Millisecond)
	claude.WriteStatus("cx1", agent.StatusUserTurn) //nolint:errcheck
	d.observeFleet(working, idle)
	if got := signalCount(d, ledger.SignalTurnCompleted); got != 1 {
		t.Fatalf("codex turn replay re-signaled: %d, want 1", got)
	}

	// A genuinely new turn (new turn_id) signals again.
	claude.WriteSessionMeta(claude.SessionMeta{Provider: agent.ProviderCodex, SessionID: "cx1", TurnID: "turn-43"}) //nolint:errcheck
	d.observeFleet(working, idle)
	if got := signalCount(d, ledger.SignalTurnCompleted); got != 2 {
		t.Fatalf("new codex turn not signaled: %d, want 2", got)
	}
}

func TestObserveFleetSessionLifecycle(t *testing.T) {
	d := newIngestDaemon(t)
	s1 := agent.Session{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusUserTurn}

	// Appears → session_started (no attention item, still a signal).
	d.observeFleet(nil, []agent.Session{s1})
	if got := signalCount(d, ledger.SignalSessionStarted); got != 1 {
		t.Fatalf("session_started = %d, want 1", got)
	}
	// Vanishes → session_ended.
	d.observeFleet([]agent.Session{s1}, nil)
	if got := signalCount(d, ledger.SignalSessionEnded); got != 1 {
		t.Fatalf("session_ended = %d, want 1", got)
	}
	// Replay of both edges dedups on the session-id anchor.
	d.observeFleet(nil, []agent.Session{s1})
	d.observeFleet([]agent.Session{s1}, nil)
	if got := signalCount(d, ledger.SignalSessionStarted) + signalCount(d, ledger.SignalSessionEnded); got != 2 {
		t.Fatalf("lifecycle replay re-signaled: %d signals", got)
	}
}

func TestObserveFleetPhantomRemovalIsNotSessionEnded(t *testing.T) {
	d := newIngestDaemon(t)
	phantom := agent.Session{SessionID: "s1", PaneID: "%1", Project: "p", IsPhantom: true, LaterID: "b1", Status: agent.StatusUserTurn}
	d.observeFleet([]agent.Session{phantom}, nil)
	if got := signalCount(d, ledger.SignalSessionEnded); got != 0 {
		t.Fatalf("phantom removal signaled session_ended")
	}
}

func TestObserveFleetLaterWoke(t *testing.T) {
	d := newIngestDaemon(t)
	wake := time.Now().Add(-time.Minute)
	parked := agent.Session{SessionID: "s1", PaneID: "%1", Project: "p", IsPhantom: true, LaterID: "b1", LaterWakeAt: &wake, Status: agent.StatusUserTurn}

	d.observeFleet([]agent.Session{parked}, nil)
	if got := signalCount(d, ledger.SignalLaterWoke); got != 1 {
		t.Fatalf("later_woke = %d, want 1", got)
	}
	if got := signalCount(d, ledger.SignalSessionEnded); got != 0 {
		t.Fatalf("wake also signaled session_ended")
	}
	// Replay dedups on the later-record id.
	d.observeFleet([]agent.Session{parked}, nil)
	if got := signalCount(d, ledger.SignalLaterWoke); got != 1 {
		t.Fatalf("later_woke replay re-signaled")
	}
}

func TestObserveOverlapsEdgeAndClear(t *testing.T) {
	d := newIngestDaemon(t)
	sessions := []agent.Session{
		{SessionID: "s1", PaneID: "%1", Project: "p"},
		{SessionID: "s2", PaneID: "%2", Project: "p"},
	}
	overlap := []claude.FileOverlap{{FilePath: "main.go", PaneIDs: []string{"%1", "%2"}, SessionIDs: []string{"s2", "s1"}}}

	d.observeOverlaps(overlap, sessions)
	d.observeOverlaps(overlap, sessions) // steady state: no re-signal
	if got := signalCount(d, ledger.SignalOverlapDetected); got != 1 {
		t.Fatalf("overlap_detected = %d, want 1", got)
	}

	d.observeOverlaps(nil, sessions) // cleared
	for _, it := range ledgerItems(d) {
		if it.Category == ledger.CategoryOverlap && it.Status != ledger.StatusResolved {
			t.Fatalf("overlap item not resolved on clear: %+v", it)
		}
	}
}
