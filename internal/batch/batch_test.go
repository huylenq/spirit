package batch

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/receipt"
)

// fakeOps is an in-memory batch.Ops with scriptable failures and a full call
// log, so tests can assert both what executed and what did NOT.
type fakeOps struct {
	mu       sync.Mutex
	sessions []agent.Session
	calls    []string
	failOn   map[string]error // call name (e.g. "send:s2") → error
	reported []string         // action failure reports: "op:session"
}

func newFakeOps(sessions ...agent.Session) *fakeOps {
	return &fakeOps{sessions: sessions, failOn: map[string]error{}}
}

func (f *fakeOps) record(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.failOn[call]
}

func (f *fakeOps) Sessions() ([]agent.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Session(nil), f.sessions...), nil
}

func (f *fakeOps) removeSession(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sessions[:0]
	for _, s := range f.sessions {
		if s.SessionID != id {
			out = append(out, s)
		}
	}
	f.sessions = out
}

func (f *fakeOps) Send(sessionID, message string) error {
	return f.record("send:" + sessionID)
}

func (f *fakeOps) Queue(paneID, sessionID, message, actionID string) (string, error) {
	if err := f.record("queue:" + sessionID); err != nil {
		return "", err
	}
	return "qi_fake", nil
}

func (f *fakeOps) Spawn(provider agent.ProviderID, cwd, tmuxSession, message string, remoteControl bool) (string, string, error) {
	if err := f.record(fmt.Sprintf("spawn:%s:remote=%t", cwd, remoteControl)); err != nil {
		return "", "", err
	}
	return "spawned-1", "%9", nil
}

func (f *fakeOps) Kill(sessionID string) error {
	if err := f.record("kill:" + sessionID); err != nil {
		return err
	}
	f.removeSession(sessionID)
	return nil
}

func (f *fakeOps) SetTags(sessionID string, tags []string) error {
	return f.record("tag:" + sessionID)
}

func (f *fakeOps) SetNote(sessionID, note string) error {
	return f.record("note:" + sessionID)
}

func (f *fakeOps) Later(paneID, sessionID string) error {
	return f.record("later:" + sessionID)
}

func (f *fakeOps) LaterKill(paneID string, pid int, sessionID string) error {
	return f.record("laterkill:" + sessionID)
}

func (f *fakeOps) CommitOnly(paneID, sessionID string, pid int) error {
	return f.record("commit:" + sessionID)
}

func (f *fakeOps) CommitAndDone(paneID, sessionID string, pid int) error {
	return f.record("commitdone:" + sessionID)
}

func (f *fakeOps) ReportActionFailure(actionID, operation, sessionID, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reported = append(f.reported, operation+":"+sessionID)
	return nil
}

func idle(id, pane string) agent.Session {
	return agent.Session{SessionID: id, PaneID: pane, Status: agent.StatusUserTurn, FirstMessage: "task " + id}
}

// --- parsing ---

func TestParseBatchAcceptsObjectAndArray(t *testing.T) {
	b, err := ParseBatch([]byte(`{"actions":[{"op":"send","session_id":"s1","message":"hi"}],"on_error":"continue"}`))
	if err != nil || len(b.Actions) != 1 || b.OnError != ContinueOnError {
		t.Fatalf("object parse = %+v, %v", b, err)
	}
	b, err = ParseBatch([]byte(`[{"op":"note","session_id":"s1","note":"n"}]`))
	if err != nil || len(b.Actions) != 1 || b.OnError != StopOnError {
		t.Fatalf("array parse = %+v, %v", b, err)
	}
	if _, err := ParseBatch([]byte(`{"actions":[]}`)); err == nil {
		t.Fatal("empty actions must be rejected")
	}
	if _, err := ParseBatch([]byte(`{"actions":[{"op":"send"}],"on_error":"maybe"}`)); err == nil {
		t.Fatal("invalid on_error must be rejected")
	}
}

// --- risk classes (Decision 5) ---

func TestRiskClasses(t *testing.T) {
	cases := []struct {
		step Step
		want RiskClass
	}{
		{Step{Op: OpWait, Phase: "idle"}, RiskReadOnly},
		{Step{Op: OpSend}, RiskReversible},
		{Step{Op: OpQueue}, RiskReversible},
		{Step{Op: OpSpawn}, RiskReversible},
		{Step{Op: OpTag}, RiskReversible},
		{Step{Op: OpNote}, RiskReversible},
		{Step{Op: OpLater}, RiskReversible},
		{Step{Op: OpLater, Kill: true}, RiskDestructive},
		{Step{Op: OpCommit}, RiskReversible},
		{Step{Op: OpCommit, Done: true}, RiskDestructive},
		{Step{Op: OpKill}, RiskDestructive},
	}
	for _, c := range cases {
		if got := c.step.Risk(); got != c.want {
			t.Errorf("Risk(%s kill=%v done=%v) = %s, want %s", c.step.Op, c.step.Kill, c.step.Done, got, c.want)
		}
	}
}

// --- plan (dry-run) ---

func TestBuildPlanResolvesTargetsAndExecutesNothing(t *testing.T) {
	ops := newFakeOps(idle("s1", "%1"), idle("s2", "%2"))
	plan, err := BuildPlan(ops, Batch{Actions: []Step{
		{Op: OpQueue, SessionID: "s1", Message: "go"},
		{Op: OpWait, SessionID: "s1", Phase: "cycle"},
		{Op: OpKill, SessionID: "s2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Preview || plan.BatchID == "" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("steps = %d", len(plan.Steps))
	}
	if plan.Steps[0].Target.PaneID != "%1" || plan.Steps[0].Target.DisplayName != "task s1" {
		t.Fatalf("target not resolved: %+v", plan.Steps[0].Target)
	}
	if plan.DestructiveCount != 1 || !plan.Steps[2].ApprovalPoint || plan.Steps[2].Risk != RiskDestructive {
		t.Fatalf("destructive marking wrong: %+v", plan.Steps[2])
	}
	if plan.Steps[1].Risk != RiskReadOnly || plan.Steps[1].ApprovalPoint {
		t.Fatalf("wait step misclassified: %+v", plan.Steps[1])
	}
	// A plan executes NOTHING: the only ops call was the fleet read.
	for _, call := range ops.calls {
		t.Fatalf("plan executed an operation: %s", call)
	}
}

func TestBuildPlanFailFast(t *testing.T) {
	ops := newFakeOps(idle("s1", "%1"))
	cases := []struct {
		name  string
		batch Batch
		want  string
	}{
		{"unknown session", Batch{Actions: []Step{{Op: OpSend, SessionID: "ghost", Message: "x"}}}, "unknown session ghost"},
		{"unknown op", Batch{Actions: []Step{{Op: "reboot", SessionID: "s1"}}}, `unknown op "reboot"`},
		{"missing message", Batch{Actions: []Step{{Op: OpSend, SessionID: "s1"}}}, "message is required"},
		{"missing session", Batch{Actions: []Step{{Op: OpKill}}}, "session_id is required"},
		{"bad wait phase", Batch{Actions: []Step{{Op: OpWait, SessionID: "s1", Phase: "done"}}}, "phase must be"},
		{"spawn without cwd", Batch{Actions: []Step{{Op: OpSpawn}}}, "cwd is required"},
		{"unknown provider", Batch{Actions: []Step{{Op: OpSpawn, CWD: "/tmp", Provider: "gpt"}}}, "unknown agent provider"},
		{"codex remote control", Batch{Actions: []Step{{Op: OpSpawn, CWD: "/tmp", Provider: "codex", RemoteControl: true}}}, "only available for Claude"},
		{"empty", Batch{}, "no actions"},
	}
	for _, c := range cases {
		_, err := BuildPlan(ops, c.batch)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want contains %q", c.name, err, c.want)
		}
	}
	// Fail-fast means nothing was executed either.
	if len(ops.calls) != 0 {
		t.Fatalf("validation executed operations: %v", ops.calls)
	}
}

func TestBuildPlanCapabilityGate(t *testing.T) {
	// Codex sessions do not support the commit workflow (provider registry) —
	// a batch queueing a commit against one must be rejected at plan time.
	codex := idle("c1", "%3")
	codex.Provider = agent.ProviderCodex
	ops := newFakeOps(codex)
	_, err := BuildPlan(ops, Batch{Actions: []Step{{Op: OpCommit, SessionID: "c1"}}})
	if err == nil || !strings.Contains(err.Error(), "step 1") {
		t.Fatalf("capability-gated op must fail at plan time with a precise error; got %v", err)
	}
}

// --- execute ---

func TestExecuteSpawnPropagatesRemoteControl(t *testing.T) {
	ops := newFakeOps()
	res, err := Execute(ops, Batch{Actions: []Step{{
		Op: OpSpawn, CWD: "/tmp/project", Provider: "claude", RemoteControl: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops.calls) != 1 || ops.calls[0] != "spawn:/tmp/project:remote=true" {
		t.Fatalf("spawn calls = %v", ops.calls)
	}
	if len(res.Receipts) != 1 || res.Receipts[0].Params["remote_control"] != true {
		t.Fatalf("spawn receipt = %+v", res.Receipts)
	}
}

func TestExecuteHappyPathReceipts(t *testing.T) {
	ops := newFakeOps(idle("s1", "%1"), idle("s2", "%2"))
	res, err := Execute(ops, Batch{Actions: []Step{
		{Op: OpNote, SessionID: "s1", Note: "n"},
		{Op: OpQueue, SessionID: "s2", Message: "go"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeCompleted || res.Executed != 2 || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Receipts) != 2 {
		t.Fatalf("receipts = %d", len(res.Receipts))
	}
	for _, r := range res.Receipts {
		if r.RequestID != res.BatchID {
			t.Fatalf("receipt %s not stamped with batch id: %q vs %q", r.Operation, r.RequestID, res.BatchID)
		}
		if r.ActionID == "" || r.ObservedState == nil || !r.ObservedState.Alive {
			t.Fatalf("receipt incomplete: %+v", r)
		}
	}
	if res.Receipts[1].DeliveryOutcome != receipt.OutcomeQueued || res.Receipts[1].Params["queue_item_id"] != "qi_fake" {
		t.Fatalf("queue receipt = %+v", res.Receipts[1])
	}
	if len(res.Remainder) != 0 {
		t.Fatalf("remainder = %+v", res.Remainder)
	}
}

func TestExecuteStopOnFailureSkipsAndReturnsRemainder(t *testing.T) {
	// kill s2 succeeds, send s2 then fails at execution time (target gone),
	// note s1 is skipped — the exact plan→action gap partial failure comes from.
	ops := newFakeOps(idle("s1", "%1"), idle("s2", "%2"))
	res, err := Execute(ops, Batch{Actions: []Step{
		{Op: OpKill, SessionID: "s2"},
		{Op: OpSend, SessionID: "s2", Message: "hi"},
		{Op: OpNote, SessionID: "s1", Note: "after"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomePartial || res.Executed != 1 || res.Failed != 1 || res.Skipped != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Receipts[0].DeliveryOutcome != receipt.OutcomeCompleted {
		t.Fatalf("kill receipt = %+v", res.Receipts[0])
	}
	if res.Receipts[1].DeliveryOutcome != receipt.OutcomeFailed || !strings.Contains(res.Receipts[1].Error, "session not found") {
		t.Fatalf("send receipt = %+v", res.Receipts[1])
	}
	if res.Receipts[2].DeliveryOutcome != receipt.OutcomeSkipped {
		t.Fatalf("note receipt = %+v", res.Receipts[2])
	}
	// The skipped step never touched the ops layer.
	for _, call := range ops.calls {
		if call == "note:s1" {
			t.Fatal("skipped step was executed")
		}
	}
	// Remainder is the unexecuted step, verbatim and resubmittable.
	if len(res.Remainder) != 1 || res.Remainder[0].Op != OpNote || res.Remainder[0].SessionID != "s1" {
		t.Fatalf("remainder = %+v", res.Remainder)
	}
	// The failure was reported to the perception ledger.
	if len(ops.reported) != 1 || ops.reported[0] != "send_message:s2" {
		t.Fatalf("reported = %v", ops.reported)
	}

	// Resume: resubmit the remainder with resume_of; it completes.
	resume, err := Execute(ops, Batch{Actions: res.Remainder, ResumeOf: res.BatchID})
	if err != nil {
		t.Fatal(err)
	}
	if resume.Outcome != OutcomeCompleted || resume.ResumeOf != res.BatchID || resume.Executed != 1 {
		t.Fatalf("resume = %+v", resume)
	}
}

func TestExecuteContinueOnError(t *testing.T) {
	ops := newFakeOps(idle("s1", "%1"), idle("s2", "%2"))
	ops.failOn["note:s1"] = fmt.Errorf("boom")
	res, err := Execute(ops, Batch{
		OnError: ContinueOnError,
		Actions: []Step{
			{Op: OpNote, SessionID: "s1", Note: "x"},
			{Op: OpTag, SessionID: "s2", Tags: []string{"t"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomePartial || res.Executed != 1 || res.Failed != 1 || res.Skipped != 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Remainder) != 0 {
		t.Fatalf("continue mode must not accumulate a remainder: %+v", res.Remainder)
	}
	found := false
	for _, call := range ops.calls {
		if call == "tag:s2" {
			found = true
		}
	}
	if !found {
		t.Fatal("later step did not run under continue_on_error")
	}
}

func TestExecuteAllFailedOutcome(t *testing.T) {
	ops := newFakeOps(idle("s1", "%1"))
	ops.failOn["send:s1"] = fmt.Errorf("pane rejected input")
	res, err := Execute(ops, Batch{Actions: []Step{{Op: OpSend, SessionID: "s1", Message: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeFailed || res.Executed != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestExecuteRejectsInvalidBatchWhole(t *testing.T) {
	ops := newFakeOps(idle("s1", "%1"))
	_, err := Execute(ops, Batch{Actions: []Step{
		{Op: OpNote, SessionID: "s1", Note: "fine"},
		{Op: OpSend, SessionID: "ghost", Message: "x"},
	}})
	if err == nil || !strings.Contains(err.Error(), "unknown session ghost") {
		t.Fatalf("err = %v", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("invalid batch was half-executed: %v", ops.calls)
	}
}

func TestExecuteWaitStep(t *testing.T) {
	old := waitPollInterval
	waitPollInterval = time.Millisecond
	defer func() { waitPollInterval = old }()

	working := agent.Session{SessionID: "s1", PaneID: "%1", Status: agent.StatusAgentTurn}
	ops := newFakeOps(working)
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		ops.mu.Lock()
		ops.sessions[0].Status = agent.StatusUserTurn
		ops.mu.Unlock()
		close(done)
	}()
	res, err := Execute(ops, Batch{Actions: []Step{{Op: OpWait, SessionID: "s1", Phase: "cycle", TimeoutSeconds: 5}}})
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeCompleted || res.Receipts[0].DeliveryOutcome != receipt.OutcomeCompleted {
		t.Fatalf("wait result = %+v", res)
	}

	// Timeout is a step failure.
	ops2 := newFakeOps(working)
	res2, err := Execute(ops2, Batch{Actions: []Step{{Op: OpWait, SessionID: "s1", Phase: "idle", TimeoutSeconds: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Outcome != OutcomeFailed || !strings.Contains(res2.Receipts[0].Error, "did not reach phase") {
		t.Fatalf("timeout result = %+v", res2.Receipts[0])
	}
}
