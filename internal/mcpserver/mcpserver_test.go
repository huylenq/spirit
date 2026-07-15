package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
)

// fakeDaemon is an in-process daemonAPI double. It records side-effect calls and can
// be told to reject a Send so error mapping is exercised.
type fakeDaemon struct {
	sessions      []agent.Session
	sendErr       error
	sent          []string // "sessionID|message" for each Send
	queued        []string
	killed        []string
	tagsSet       map[string][]string
	spawnID       string
	spawnPane     string
	actionReports []string // "actionID|operation|sessionID|error" per failed-receipt report
}

func (f *fakeDaemon) Sessions(string) ([]agent.Session, error) { return f.sessions, nil }
func (f *fakeDaemon) Transcript(string) ([]string, claude.CurrentTurn, error) {
	return []string{"hello"}, claude.CurrentTurn{}, nil
}
func (f *fakeDaemon) TranscriptEntries(string) ([]claude.TranscriptEntry, error) { return nil, nil }
func (f *fakeDaemon) DiffStats(string) (map[string]claude.FileDiffStat, error)   { return nil, nil }
func (f *fakeDaemon) DiffHunks(string) ([]claude.FileDiffHunk, error)            { return nil, nil }
func (f *fakeDaemon) HookEvents(string) ([]claude.HookEvent, error)              { return nil, nil }
func (f *fakeDaemon) Summary(string) (*claude.SessionSummary, error)             { return nil, nil }

func (f *fakeDaemon) Send(sessionID, message string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, sessionID+"|"+message)
	// Simulate the target entering agent-turn after a successful send.
	for i := range f.sessions {
		if f.sessions[i].SessionID == sessionID {
			f.sessions[i].Status = agent.StatusAgentTurn
		}
	}
	return nil
}

func (f *fakeDaemon) Queue(paneID, sessionID, message string) error {
	f.queued = append(f.queued, sessionID+"|"+message)
	return nil
}

func (f *fakeDaemon) SpawnProvider(provider agent.ProviderID, cwd, tmuxSession, message, splitFromPane string) (daemon.SpawnResultData, error) {
	f.spawnID = "new-session"
	f.spawnPane = "%99"
	f.sessions = append(f.sessions, agent.Session{SessionID: f.spawnID, PaneID: f.spawnPane})
	return daemon.SpawnResultData{SessionID: f.spawnID, PaneID: f.spawnPane}, nil
}

func (f *fakeDaemon) Kill(sessionID string) error {
	f.killed = append(f.killed, sessionID)
	out := f.sessions[:0]
	for _, s := range f.sessions {
		if s.SessionID != sessionID {
			out = append(out, s)
		}
	}
	f.sessions = out
	return nil
}

func (f *fakeDaemon) SetTags(sessionID string, tags []string) error {
	if f.tagsSet == nil {
		f.tagsSet = map[string][]string{}
	}
	f.tagsSet[sessionID] = tags
	return nil
}
func (f *fakeDaemon) SetNote(string, string) error                { return nil }
func (f *fakeDaemon) Later(string, string, string) error          { return nil }
func (f *fakeDaemon) LaterKill(string, int, string, string) error { return nil }
func (f *fakeDaemon) CommitOnly(string, string, int) error        { return nil }
func (f *fakeDaemon) CommitAndDone(string, string, int) error     { return nil }
func (f *fakeDaemon) ReportActionFailure(actionID, operation, sessionID, errMsg string) error {
	f.actionReports = append(f.actionReports, actionID+"|"+operation+"|"+sessionID+"|"+errMsg)
	return nil
}

// pipeClient drives a Server over io.Pipes and reads newline-delimited responses.
type pipeClient struct {
	inW    io.WriteCloser
	outR   *bufio.Scanner
	nextID int
}

func newPipeClient(t *testing.T, api daemonAPI) *pipeClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := New(api)
	go func() {
		_ = srv.Serve(inR, outW)
		outW.Close()
	}()
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	return &pipeClient{inW: inW, outR: sc}
}

// call sends a request and returns the parsed response (nil for notifications).
func (p *pipeClient) call(t *testing.T, method string, params any) *rpcResponse {
	t.Helper()
	p.nextID++
	req := map[string]any{"jsonrpc": "2.0", "id": p.nextID, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, _ := json.Marshal(req)
	if _, err := p.inW.Write(append(data, '\n')); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if !p.outR.Scan() {
		t.Fatalf("no response for %s: %v", method, p.outR.Err())
	}
	var resp rpcResponse
	if err := json.Unmarshal(p.outR.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v (%s)", err, p.outR.Text())
	}
	return &resp
}

func (p *pipeClient) close() { p.inW.Close() }

// callTool sends a tools/call and decodes the single text content block into v.
func (p *pipeClient) callTool(t *testing.T, name string, args map[string]any) (*toolCallResult, json.RawMessage) {
	t.Helper()
	resp := p.call(t, "tools/call", map[string]any{"name": name, "arguments": args})
	if resp.Error != nil {
		t.Fatalf("tools/call %s errored: %+v", name, resp.Error)
	}
	raw, _ := json.Marshal(resp.Result)
	var res toolCallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("expected one text content block, got %+v", res.Content)
	}
	return &res, json.RawMessage(res.Content[0].Text)
}

func fixtureSessions() []agent.Session {
	return []agent.Session{
		{SessionID: "sess-1", PaneID: "%1", PID: 100, Status: agent.StatusUserTurn, FirstMessage: "fix the bug"},
		{SessionID: "sess-2", PaneID: "%2", PID: 200, Status: agent.StatusAgentTurn},
	}
}

func TestInitializeAndToolsList(t *testing.T) {
	pc := newPipeClient(t, &fakeDaemon{sessions: fixtureSessions()})
	defer pc.close()

	init := pc.call(t, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if init.Error != nil {
		t.Fatalf("initialize errored: %+v", init.Error)
	}
	raw, _ := json.Marshal(init.Result)
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities map[string]any `json:"capabilities"`
	}
	json.Unmarshal(raw, &ir)
	if ir.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want echo of client version", ir.ProtocolVersion)
	}
	if ir.ServerInfo.Name != "spirit" {
		t.Errorf("serverInfo.name = %q, want spirit", ir.ServerInfo.Name)
	}
	if _, ok := ir.Capabilities["tools"]; !ok {
		t.Errorf("capabilities missing tools: %+v", ir.Capabilities)
	}

	list := pc.call(t, "tools/list", nil)
	if list.Error != nil {
		t.Fatalf("tools/list errored: %+v", list.Error)
	}
	raw, _ = json.Marshal(list.Result)
	var lr struct {
		Tools []toolDescriptor `json:"tools"`
	}
	json.Unmarshal(raw, &lr)
	if len(lr.Tools) != len(buildTools()) {
		t.Errorf("tools/list returned %d tools, want %d", len(lr.Tools), len(buildTools()))
	}
	// Every tool must carry a non-empty inputSchema that parses as JSON.
	for _, td := range lr.Tools {
		if td.Name == "" || len(td.InputSchema) == 0 {
			t.Errorf("tool %q has empty schema", td.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(td.InputSchema, &schema); err != nil {
			t.Errorf("tool %q schema not valid JSON: %v", td.Name, err)
		}
	}
}

func TestReadOnlyToolReturnsData(t *testing.T) {
	pc := newPipeClient(t, &fakeDaemon{sessions: fixtureSessions()})
	defer pc.close()

	res, body := pc.callTool(t, "list_sessions", map[string]any{})
	if res.IsError {
		t.Fatalf("list_sessions marked as error: %s", body)
	}
	var sessions []agent.Session
	if err := json.Unmarshal(body, &sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].SessionID != "sess-1" {
		t.Errorf("unexpected sessions: %+v", sessions)
	}
}

func TestSideEffectToolReturnsReceipt(t *testing.T) {
	fd := &fakeDaemon{sessions: fixtureSessions()}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, body := pc.callTool(t, "send_message", map[string]any{"session_id": "sess-1", "message": "run the tests"})
	if res.IsError {
		t.Fatalf("send_message marked as error: %s", body)
	}
	var rc struct {
		ActionID        string `json:"action_id"`
		Operation       string `json:"operation"`
		DeliveryOutcome string `json:"delivery_outcome"`
		AcceptedAt      string `json:"accepted_at"`
		Target          struct {
			SessionID  string `json:"session_id"`
			ResolvedBy string `json:"resolved_by"`
		} `json:"target"`
		ObservedState struct {
			Status string `json:"status"`
			Alive  bool   `json:"alive"`
		} `json:"observed_state_after"`
	}
	if err := json.Unmarshal(body, &rc); err != nil {
		t.Fatalf("decode receipt: %v (%s)", err, body)
	}
	if !strings.HasPrefix(rc.ActionID, "act_") {
		t.Errorf("action_id = %q, want act_ prefix", rc.ActionID)
	}
	if rc.Operation != "send_message" || rc.DeliveryOutcome != "delivered" {
		t.Errorf("operation/outcome = %q/%q", rc.Operation, rc.DeliveryOutcome)
	}
	if rc.AcceptedAt == "" {
		t.Error("accepted_at is empty")
	}
	if rc.Target.SessionID != "sess-1" || rc.Target.ResolvedBy != "explicit_id" {
		t.Errorf("target = %+v", rc.Target)
	}
	// Reconciliation: the observed post-send state should show the target working.
	if rc.ObservedState.Status != "agent-turn" || !rc.ObservedState.Alive {
		t.Errorf("observed_state_after = %+v, want agent-turn/alive", rc.ObservedState)
	}
	if len(fd.sent) != 1 {
		t.Errorf("Send called %d times, want 1", len(fd.sent))
	}
}

func TestSideEffectErrorMapsToFailedReceipt(t *testing.T) {
	fd := &fakeDaemon{sessions: fixtureSessions(), sendErr: fmt.Errorf("session is busy")}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, body := pc.callTool(t, "send_message", map[string]any{"session_id": "sess-1", "message": "hi"})
	if !res.IsError {
		t.Fatalf("expected isError=true on daemon rejection, got: %s", body)
	}
	var rc struct {
		DeliveryOutcome string `json:"delivery_outcome"`
		Error           string `json:"error"`
	}
	json.Unmarshal(body, &rc)
	if rc.DeliveryOutcome != "failed" || !strings.Contains(rc.Error, "busy") {
		t.Errorf("failed receipt = %+v", rc)
	}
	// The failed receipt is reported back to the daemon for the perception
	// ledger (action_failed signal).
	if len(fd.actionReports) != 1 || !strings.Contains(fd.actionReports[0], "send_message|sess-1|") {
		t.Errorf("action report = %v", fd.actionReports)
	}

	// A successful side effect reports nothing.
	fd2 := &fakeDaemon{sessions: fixtureSessions()}
	pc2 := newPipeClient(t, fd2)
	defer pc2.close()
	pc2.callTool(t, "send_message", map[string]any{"session_id": "sess-1", "message": "hi"})
	if len(fd2.actionReports) != 0 {
		t.Errorf("successful send reported an action failure: %v", fd2.actionReports)
	}
}

func TestKillReceiptObservesGoneSession(t *testing.T) {
	fd := &fakeDaemon{sessions: fixtureSessions()}
	pc := newPipeClient(t, fd)
	defer pc.close()

	_, body := pc.callTool(t, "kill_session", map[string]any{"session_id": "sess-1"})
	var rc struct {
		DeliveryOutcome string `json:"delivery_outcome"`
		ObservedState   struct {
			Alive bool `json:"alive"`
		} `json:"observed_state_after"`
	}
	json.Unmarshal(body, &rc)
	if rc.DeliveryOutcome != "completed" {
		t.Errorf("outcome = %q, want completed", rc.DeliveryOutcome)
	}
	if rc.ObservedState.Alive {
		t.Error("observed_state_after.alive = true after kill, want false")
	}
	if len(fd.killed) != 1 {
		t.Errorf("Kill called %d times, want 1", len(fd.killed))
	}
}

func TestUnknownMethodAndTool(t *testing.T) {
	pc := newPipeClient(t, &fakeDaemon{sessions: fixtureSessions()})
	defer pc.close()

	resp := pc.call(t, "no/such/method", nil)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Errorf("unknown method: want method-not-found, got %+v", resp.Error)
	}

	resp = pc.call(t, "tools/call", map[string]any{"name": "no_such_tool", "arguments": map[string]any{}})
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Errorf("unknown tool: want method-not-found, got %+v", resp.Error)
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	pc := newPipeClient(t, &fakeDaemon{sessions: fixtureSessions()})
	defer pc.close()

	// A notification (no id) must produce no reply. Send it, then a real request,
	// and assert the first response we read belongs to the real request.
	note, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	pc.inW.Write(append(note, '\n'))

	resp := pc.call(t, "ping", nil)
	if resp.Error != nil {
		t.Fatalf("ping after notification errored: %+v", resp.Error)
	}
}
