package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestACPSessionResultParsesEffectiveModelState(t *testing.T) {
	var result acpSessionResult
	raw := `{"sessionId":"session-123","models":{"currentModelId":"anthropic:claude-sonnet-4","availableModels":[{"modelId":"anthropic:claude-sonnet-4","name":"claude-sonnet-4"}]}}`
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Models == nil || result.Models.CurrentModelID != "anthropic:claude-sonnet-4" {
		t.Fatalf("parsed model state = %#v", result.Models)
	}
}

func TestParseCapabilitiesFromInitialize(t *testing.T) {
	raw := json.RawMessage(`{
		"protocolVersion": 1,
		"agentInfo": {"name": "hermes-agent", "version": "0.18.2"},
		"agentCapabilities": {
			"loadSession": true,
			"sessionCapabilities": {"fork": {}, "list": {}, "resume": {}}
		}
	}`)
	caps := parseCapabilities(raw)
	if caps.ProtocolVersion != 1 || !caps.LoadSession || !caps.ForkSessions || !caps.ListSessions || !caps.ResumeSessions {
		t.Fatalf("caps = %#v", caps)
	}
	if caps.AgentName != "hermes-agent" || caps.AgentVersion != "0.18.2" {
		t.Fatalf("agent info = %q %q", caps.AgentName, caps.AgentVersion)
	}

	// Missing session capabilities → false, not a crash.
	bare := parseCapabilities(json.RawMessage(`{"protocolVersion":1,"agentCapabilities":{"loadSession":false}}`))
	if bare.LoadSession || bare.ForkSessions || bare.ListSessions {
		t.Fatalf("bare caps should be all-false: %#v", bare)
	}
}

// SetModel resolves a friendly name to the canonical available model id and puts
// that id on the wire — verified through the fake agent.
func TestACPSetModelUsesCanonicalAvailableChoice(t *testing.T) {
	f := &fakeHermes{}
	c := newFakeClient(t, f)
	f.start()

	// Seed available models as if returned by session/new.
	c.mu.Lock()
	c.models = CopilotModelState{
		CurrentModelID: "openai-codex:gpt-5.4",
		AvailableModels: []CopilotModelInfo{
			{ModelID: "openai-codex:gpt-5.4", Name: "gpt-5.4"},
			{ModelID: "openai-codex:gpt-5.4-mini", Name: "gpt-5.4-mini"},
		},
	}
	c.mu.Unlock()

	state, err := c.SetModel("gpt-5.4-mini")
	if err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if state.CurrentModelID != "openai-codex:gpt-5.4-mini" {
		t.Fatalf("current model = %q", state.CurrentModelID)
	}

	f.mu.Lock()
	last := f.lastSetModel
	f.mu.Unlock()
	if last == nil || last["modelId"] != "openai-codex:gpt-5.4-mini" {
		t.Fatalf("wire set_model = %#v", last)
	}
}

// A model call issued while a prompt is streaming must be served concurrently —
// the whole point of the demultiplexed wire.
func TestACPDemuxServesModelCallDuringPrompt(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			f.textDelta(f.sessionID, "hello ")
			close(started)
			<-release // hold the turn open
			f.textDelta(f.sessionID, "world")
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		},
	}
	c := newFakeClient(t, f)
	f.start()

	c.mu.Lock()
	c.models = CopilotModelState{AvailableModels: []CopilotModelInfo{{ModelID: "m2", Name: "m2"}}}
	c.mu.Unlock()

	var out string
	var perr error
	done := make(chan struct{})
	go func() {
		out, perr = c.Prompt(context.Background(), "hi", func(CopilotStreamData) {})
		close(done)
	}()

	<-started // prompt is mid-stream, turn held open

	// This must return while the prompt is still blocked.
	if _, err := c.SetModel("m2"); err != nil {
		t.Fatalf("SetModel during prompt: %v", err)
	}

	close(release)
	<-done
	if perr != nil {
		t.Fatalf("Prompt: %v", perr)
	}
	if out != "hello world" {
		t.Fatalf("accumulated text = %q", out)
	}
}

// A permission request arriving DURING a streaming prompt is answered by the
// client's W0 policy (edit-kind → denied via the reject option) without blocking
// the prompt's own response.
func TestACPPermissionDuringPrompt(t *testing.T) {
	var outcome map[string]any
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			f.textDelta(f.sessionID, "working ")
			outcome = f.requestPermission(f.sessionID,
				map[string]any{"kind": "edit", "locations": []any{map[string]any{"path": "main.go"}}},
				[]any{
					map[string]any{"optionId": "yes", "kind": "allow_once"},
					map[string]any{"optionId": "no", "kind": "reject_once"},
				})
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		},
	}
	c := newFakeClient(t, f) // no onPermission → inline W0 policy
	f.start()

	out, err := c.Prompt(context.Background(), "edit please", func(CopilotStreamData) {})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if out != "working" {
		t.Fatalf("text = %q", out)
	}
	sel, _ := outcome["outcome"].(map[string]any)
	if sel == nil || sel["outcome"] != "selected" || sel["optionId"] != "no" {
		t.Fatalf("edit-kind permission should select reject option 'no'; got %#v", outcome)
	}
}

// The daemon-provided handler overrides the client's inline policy.
func TestACPPermissionUsesDaemonHandler(t *testing.T) {
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			out := f.requestPermission(f.sessionID,
				map[string]any{"kind": "read"},
				[]any{map[string]any{"optionId": "ok", "kind": "allow_once"}})
			// Handler forced a cancel regardless of policy.
			sel, _ := out["outcome"].(map[string]any)
			if sel == nil || sel["outcome"] != "cancelled" {
				t.Errorf("expected cancelled outcome from handler, got %#v", out)
			}
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		},
	}
	c := newFakeClient(t, f)
	c.onPermission = func(json.RawMessage) string { return "" } // always refuse
	f.start()

	if _, err := c.Prompt(context.Background(), "read", func(CopilotStreamData) {}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
}

// A session_info_update whose enclosing session id differs from ours rotates and
// re-persists the UUID.
func TestACPSessionInfoRotatesPersistedUUID(t *testing.T) {
	var mu sync.Mutex
	persisted := "fake-session-1"
	f := &fakeHermes{}
	c := newFakeClient(t, f)
	c.writeSessionID = func(id string) { mu.Lock(); persisted = id; mu.Unlock() }
	c.readSessionID = func() string { mu.Lock(); defer mu.Unlock(); return persisted }
	f.start()

	// Bring the client up (session/new sets sessionID = fake-session-1).
	if _, err := c.ModelStatus(); err != nil {
		t.Fatalf("ModelStatus: %v", err)
	}

	c.dispatchUpdate(json.RawMessage(`{
		"sessionId": "rotated-session-2",
		"update": {"sessionUpdate": "session_info_update", "title": "New title"}
	}`))

	if got := c.SessionID(); got != "rotated-session-2" {
		t.Fatalf("in-memory session id = %q, want rotated-session-2", got)
	}
	mu.Lock()
	p := persisted
	mu.Unlock()
	if p != "rotated-session-2" {
		t.Fatalf("persisted session id = %q, want rotated-session-2", p)
	}
}

// A persisted UUID that Hermes no longer knows (session/load returns null) must
// fall back to a fresh session and forget the stale id — never silently "resume"
// a non-existent thread.
func TestACPStaleSessionFallsBackToFresh(t *testing.T) {
	var mu sync.Mutex
	persisted := "stale-uuid"
	f := &fakeHermes{loadNull: true, sessionID: "brand-new"}
	c := newFakeClient(t, f)
	c.writeSessionID = func(id string) { mu.Lock(); persisted = id; mu.Unlock() }
	c.readSessionID = func() string { mu.Lock(); defer mu.Unlock(); return persisted }
	c.clearSessionID = func() { mu.Lock(); persisted = ""; mu.Unlock() }
	f.start()

	if _, err := c.ModelStatus(); err != nil {
		t.Fatalf("ModelStatus: %v", err)
	}
	if got := c.SessionID(); got != "brand-new" {
		t.Fatalf("session id = %q, want fresh brand-new", got)
	}
	mu.Lock()
	p := persisted
	mu.Unlock()
	if p != "brand-new" {
		t.Fatalf("persisted id = %q, want brand-new (stale cleared then fresh persisted)", p)
	}
}

// usage_update and available_commands_update are consumed into client state and,
// for usage, surfaced as a `usage` stream chunk.
func TestACPConsumesUsageAndCommands(t *testing.T) {
	f := &fakeHermes{}
	c := newFakeClient(t, f)
	f.start()

	var got []CopilotStreamData
	id := c.setSink(func(evt CopilotStreamData) { got = append(got, evt) })
	defer c.clearSink(id)

	c.dispatchUpdate(json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"usage_update","size":200000,"used":50000}}`))
	c.dispatchUpdate(json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"steer","description":"steer the turn"},{"name":"compact","description":"compact"}]}}`))

	if u := c.Usage(); u == nil || u.Size != 200000 || u.Used != 50000 {
		t.Fatalf("usage = %#v", u)
	}
	if len(got) != 1 || got[0].Type != "usage" || !strings.Contains(got[0].Content, "50.0k") {
		t.Fatalf("usage chunk = %#v", got)
	}
	cmds := c.Commands()
	if len(cmds) != 2 || cmds[0].Name != "steer" || cmds[1].Name != "compact" {
		t.Fatalf("commands = %#v", cmds)
	}
}

// If the subprocess dies mid-prompt, the in-flight Prompt fails fast instead of
// hanging.
func TestACPPromptFailsFastOnDeath(t *testing.T) {
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			f.textDelta(f.sessionID, "partial")
			f.die() // never replies
		},
	}
	c := newFakeClient(t, f)
	f.start()

	errCh := make(chan error, 1)
	go func() {
		_, err := c.Prompt(context.Background(), "hi", func(CopilotStreamData) {})
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error when the subprocess dies mid-prompt")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prompt hung after subprocess death")
	}
}

// Cancelling a prompt's context returns a cancelled error (the epoch machinery in
// server_copilot.go then keeps the dying turn's events out of any newer turn).
func TestACPPromptCancel(t *testing.T) {
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			f.textDelta(f.sessionID, "thinking")
			<-f.cancelled // wait for the client's session/cancel
			f.reply(id, map[string]any{"stopReason": "cancelled"})
		},
	}
	c := newFakeClient(t, f)
	f.start()

	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan struct{})
	var once sync.Once
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Prompt(ctx, "hi", func(evt CopilotStreamData) {
			if evt.Type == "text_delta" {
				once.Do(func() { close(first) })
			}
		})
		errCh <- err
	}()

	<-first
	cancel()

	select {
	case err := <-errCh:
		if err == nil || err.Error() != "cancelled" {
			t.Fatalf("expected cancelled error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not unblock the prompt")
	}
}

// Permission gate + sensitive-path tests. Role note (W4): the live daemon policy
// is no longer "auto-deny" — decideCopilotPermission now FORWARDS every request to
// the human (see acp_permission_flow.go, TestPermission* in
// acp_permission_flow_test.go). decidePermission is retained as the documented
// fallback policy and the anchor for the sensitive-path detection helpers, which
// W4 reuses to FLAG (not deny) sensitive requests in the confirm UI. These tests
// keep that detection logic honest; TestParsePermissionFlagsSensitiveInsteadOfDenying
// covers the forwarding-role shift.

func TestDecidePermissionDeniesEditKind(t *testing.T) {
	params := json.RawMessage(`{
		"toolCall": {"kind": "edit", "locations": [{"path": "internal/app/main.go"}]},
		"options": [
			{"optionId": "allow", "kind": "allow_once"},
			{"optionId": "no", "kind": "reject_once"}
		]
	}`)
	optionID, denied, reason := decidePermission(params)
	if !denied {
		t.Fatalf("edit-kind request must be denied; reason=%q", reason)
	}
	if optionID != "no" {
		t.Fatalf("expected reject option id 'no', got %q", optionID)
	}
}

func TestDecidePermissionDeniesSensitivePath(t *testing.T) {
	params := json.RawMessage(`{
		"toolCall": {"kind": "execute", "rawInput": {"command": "cat ~/.ssh/id_rsa"}},
		"options": [
			{"optionId": "allow", "kind": "allow_always"},
			{"optionId": "deny", "kind": "deny"}
		]
	}`)
	optionID, denied, reason := decidePermission(params)
	if !denied {
		t.Fatal("request touching ~/.ssh/id_rsa must be denied")
	}
	if optionID != "deny" {
		t.Fatalf("expected deny option id 'deny', got %q (reason=%q)", optionID, reason)
	}
}

func TestDecidePermissionAllowsBenignNonEdit(t *testing.T) {
	params := json.RawMessage(`{
		"toolCall": {"kind": "read", "locations": [{"path": "README.md"}]},
		"options": [
			{"optionId": "once", "kind": "allow_once"},
			{"optionId": "always", "kind": "allow_always"}
		]
	}`)
	optionID, denied, _ := decidePermission(params)
	if denied {
		t.Fatal("benign non-edit read must not be denied")
	}
	if optionID != "always" {
		t.Fatalf("expected broadest allow option 'always', got %q", optionID)
	}
}

func TestDecidePermissionFailsClosedOnGarbage(t *testing.T) {
	if _, denied, _ := decidePermission(json.RawMessage(`not json`)); !denied {
		t.Fatal("unparseable permission request must fail closed (denied)")
	}
}

func TestIsSensitivePath(t *testing.T) {
	sensitive := []string{
		".git/config", "sub/.git/hooks/pre-commit", "~/.ssh/known_hosts",
		".env", ".env.production", "config/.env.local", "secrets/id_ed25519",
		"certs/server.pem", "deploy.key", ".npmrc", ".netrc",
	}
	for _, p := range sensitive {
		if !isSensitivePath(p) {
			t.Errorf("expected %q to be sensitive", p)
		}
	}
	benign := []string{"README.md", "internal/app/main.go", "environment.md", "git.txt"}
	for _, p := range benign {
		if isSensitivePath(p) {
			t.Errorf("expected %q to be benign", p)
		}
	}
}
