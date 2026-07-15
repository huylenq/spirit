package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

// newPermTestDaemon builds a minimal daemon with just the maps the permission flow
// touches, plus a short auto-deny timeout so the expired-path test is fast.
func newPermTestDaemon() *Daemon {
	return &Daemon{
		subscribers:        make(map[*subscriber]struct{}),
		copilotPerms:       make(map[string]*pendingPermission),
		copilotPermTimeout: 100 * time.Millisecond,
	}
}

// setActiveTurn installs an active turn so permission routing has a request/client.
func (d *Daemon) setActiveTurn(requestID, clientID string) {
	d.copilotStateMu.Lock()
	d.copilotActive = &copilotActiveState{RequestID: requestID, ClientID: clientID, Streaming: true}
	d.copilotStateMu.Unlock()
}

// waitPermChunk reads the next permission-related chunk delivered to a subscriber.
func waitPermChunk(t *testing.T, sub *subscriber, typ string) CopilotStreamData {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-sub.copilot:
			if evt.Type == typ {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q chunk", typ)
		}
	}
}

const editPermParams = `{
	"sessionId": "s1",
	"toolCall": {
		"toolCallId": "edit-approval-1",
		"title": "Approve edit: internal/app/main.go",
		"kind": "edit",
		"content": [{"type": "diff", "path": "internal/app/main.go", "oldText": "a\nb\n", "newText": "a\nc\n"}],
		"rawInput": {"tool": "patch", "arguments": {}}
	},
	"options": [
		{"optionId": "allow_once", "kind": "allow_once", "name": "Allow edit"},
		{"optionId": "deny", "kind": "reject_once", "name": "Deny"}
	]
}`

const execPermParams = `{
	"sessionId": "s1",
	"toolCall": {
		"toolCallId": "perm-check-1",
		"title": "Run: rm -rf build",
		"kind": "execute",
		"content": [{"type": "content", "content": {"type": "text", "text": "$ rm -rf build"}}],
		"rawInput": {"command": "rm -rf build", "description": "clean"}
	},
	"options": [
		{"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"},
		{"optionId": "allow_session", "kind": "allow_always", "name": "Allow for session"},
		{"optionId": "allow_always", "kind": "allow_always", "name": "Allow always"},
		{"optionId": "deny", "kind": "reject_once", "name": "Deny"},
		{"optionId": "deny_always", "kind": "reject_always", "name": "Deny always"}
	]
}`

// A permission request is forwarded to the originating client as a typed payload,
// and the human's answer resolves the ACP request.
func TestPermissionForwardsAndAnswers(t *testing.T) {
	d := newPermTestDaemon()
	d.setActiveTurn("r1", "c1")
	sub := d.addSubscriber("c1")

	resultCh := make(chan string, 1)
	go func() { resultCh <- d.decideCopilotPermission(json.RawMessage(editPermParams)) }()

	req := waitPermChunk(t, sub, "permission_request")
	if req.ClientID != "c1" || req.RequestID != "r1" {
		t.Fatalf("permission not routed to originating client: %+v", req)
	}
	p := req.Permission
	if p == nil || p.Kind != "edit" || len(p.Diffs) != 1 || p.Diffs[0].NewText != "a\nc\n" {
		t.Fatalf("edit payload not forwarded faithfully: %+v", p)
	}
	if p.DeadlineUnix == 0 {
		t.Fatal("deadline not stamped")
	}
	// Options carry assigned keys.
	var yKey, nKey bool
	for _, o := range p.Options {
		if o.OptionID == "allow_once" && o.Key == "y" {
			yKey = true
		}
		if o.OptionID == "deny" && o.Key == "n" {
			nKey = true
		}
	}
	if !yKey || !nKey {
		t.Fatalf("option keys not assigned: %+v", p.Options)
	}

	if r := d.handleCopilotPermissionAnswer(marshalData(CopilotPermissionAnswerData{PermissionID: p.PermissionID, OptionID: "allow_once"})); r.Error != "" {
		t.Fatalf("answer failed: %s", r.Error)
	}

	select {
	case got := <-resultCh:
		if got != "allow_once" {
			t.Fatalf("decide returned %q, want allow_once", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("decideCopilotPermission did not return after answer")
	}

	resolved := waitPermChunk(t, sub, "permission_resolved")
	if resolved.Status != "approved" {
		t.Fatalf("resolved status = %q, want approved", resolved.Status)
	}
}

// The 5-option dangerous-command variant maps option ids to y/a/n/N keys and its
// command is surfaced.
func TestPermissionDangerousCommandKeysAndCommand(t *testing.T) {
	d := newPermTestDaemon()
	d.setActiveTurn("r1", "c1")
	sub := d.addSubscriber("c1")

	go d.decideCopilotPermission(json.RawMessage(execPermParams))
	req := waitPermChunk(t, sub, "permission_request")
	p := req.Permission
	if p.Command != "rm -rf build" {
		t.Fatalf("command not surfaced: %q", p.Command)
	}
	want := map[string]string{"allow_once": "y", "allow_session": "a", "allow_always": "a", "deny": "n", "deny_always": "N"}
	for _, o := range p.Options {
		if want[o.OptionID] != o.Key {
			t.Fatalf("option %q key = %q, want %q", o.OptionID, o.Key, want[o.OptionID])
		}
	}
	// Clean up the pending request.
	d.denyPermissionsForActiveTurn("cancelled")
}

// With no TUI client attached, a permission request fails safe (denied) rather than
// hanging until Hermes's own timeout.
func TestPermissionNoClientDenies(t *testing.T) {
	d := newPermTestDaemon()
	d.setActiveTurn("r1", "c1")
	got := d.decideCopilotPermission(json.RawMessage(editPermParams))
	if got != "" {
		t.Fatalf("no-client permission should deny (return \"\"), got %q", got)
	}
}

// When no answer arrives before the deadline, the request auto-denies.
func TestPermissionExpiresDenies(t *testing.T) {
	d := newPermTestDaemon()
	d.setActiveTurn("r1", "c1")
	sub := d.addSubscriber("c1")

	resultCh := make(chan string, 1)
	go func() { resultCh <- d.decideCopilotPermission(json.RawMessage(editPermParams)) }()
	_ = waitPermChunk(t, sub, "permission_request")

	select {
	case got := <-resultCh:
		if got != "" {
			t.Fatalf("expired permission should deny, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expired permission did not resolve")
	}
	resolved := waitPermChunk(t, sub, "permission_resolved")
	if resolved.Status != "expired" {
		t.Fatalf("resolved status = %q, want expired", resolved.Status)
	}
}

// Cancelling the owning turn denies its pending permission (Gate A: a dying turn's
// approval cannot leak into the next turn).
func TestPermissionCancelDenies(t *testing.T) {
	d := newPermTestDaemon()
	d.copilotPermTimeout = 5 * time.Second // long enough that cancel, not timeout, wins
	d.setActiveTurn("r1", "c1")
	sub := d.addSubscriber("c1")

	resultCh := make(chan string, 1)
	go func() { resultCh <- d.decideCopilotPermission(json.RawMessage(editPermParams)) }()
	_ = waitPermChunk(t, sub, "permission_request")

	d.denyPermissionsForActiveTurn("cancelled")

	select {
	case got := <-resultCh:
		if got != "" {
			t.Fatalf("cancelled permission should deny, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not resolve the pending permission")
	}
}

// A disconnecting owner client denies its pending permission.
func TestPermissionDisconnectDenies(t *testing.T) {
	d := newPermTestDaemon()
	d.copilotPermTimeout = 5 * time.Second
	d.setActiveTurn("r1", "c1")
	sub := d.addSubscriber("c1")

	resultCh := make(chan string, 1)
	go func() { resultCh <- d.decideCopilotPermission(json.RawMessage(editPermParams)) }()
	_ = waitPermChunk(t, sub, "permission_request")

	d.removeSubscriber(sub) // triggers denyPermissionsForClient

	select {
	case got := <-resultCh:
		if got != "" {
			t.Fatalf("disconnected client's permission should deny, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not resolve the pending permission")
	}
}

// A stale/duplicate answer (already resolved) is an informative no-op, not a panic
// or a second resolution.
func TestPermissionStaleAnswerNoOp(t *testing.T) {
	d := newPermTestDaemon()
	d.setActiveTurn("r1", "c1")
	sub := d.addSubscriber("c1")

	resultCh := make(chan string, 1)
	go func() { resultCh <- d.decideCopilotPermission(json.RawMessage(editPermParams)) }()
	req := waitPermChunk(t, sub, "permission_request")
	pid := req.Permission.PermissionID

	d.handleCopilotPermissionAnswer(marshalData(CopilotPermissionAnswerData{PermissionID: pid, OptionID: "allow_once"}))
	<-resultCh

	r := d.handleCopilotPermissionAnswer(marshalData(CopilotPermissionAnswerData{PermissionID: pid, OptionID: "allow_once"}))
	var out map[string]string
	json.Unmarshal(r.Data, &out) //nolint:errcheck
	if out["status"] != "already_resolved" {
		t.Fatalf("stale answer status = %q, want already_resolved", out["status"])
	}
}

// If the owning client has vanished but another client is attached, the request
// broadcasts (fallback) rather than denying.
func TestPermissionBroadcastFallbackWhenOwnerGone(t *testing.T) {
	d := newPermTestDaemon()
	d.setActiveTurn("r1", "c1") // owner c1 is NOT subscribed
	sub := d.addSubscriber("c2")

	resultCh := make(chan string, 1)
	go func() { resultCh <- d.decideCopilotPermission(json.RawMessage(editPermParams)) }()

	req := waitPermChunk(t, sub, "permission_request")
	if req.ClientID != "" {
		t.Fatalf("broadcast fallback should clear ClientID, got %q", req.ClientID)
	}
	d.handleCopilotPermissionAnswer(marshalData(CopilotPermissionAnswerData{PermissionID: req.Permission.PermissionID, OptionID: "deny"}))
	if got := <-resultCh; got != "deny" {
		t.Fatalf("decide returned %q, want deny", got)
	}
}

// parsePermissionRequest now FORWARDS (rather than auto-denying) a sensitive-path
// request, flagging it for the human — the W4 role change from the W0 stopgap.
func TestParsePermissionFlagsSensitiveInsteadOfDenying(t *testing.T) {
	params := json.RawMessage(`{
		"toolCall": {"kind": "execute", "title": "cat", "rawInput": {"command": "cat ~/.ssh/id_rsa"},
			"content": [{"type": "content", "content": {"type": "text", "text": "$ cat ~/.ssh/id_rsa"}}]},
		"options": [{"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"},
			{"optionId": "deny", "kind": "reject_once", "name": "Deny"}]
	}`)
	p, err := parsePermissionRequest(params)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !p.Sensitive || p.SensitiveHit == "" {
		t.Fatalf("sensitive path should be flagged: %+v", p)
	}
	if p.Command != "cat ~/.ssh/id_rsa" {
		t.Fatalf("command = %q", p.Command)
	}
	if len(p.Options) != 2 {
		t.Fatalf("options not forwarded: %+v", p.Options)
	}
}

// TestPermissionNonMainSessionAutoDenied: a permission request raised by any
// session other than the main Lulu session (i.e. a reactive fork, W7) is denied
// without ever reaching a human — reactive runs must not surprise the user with
// approval prompts, and must not be granted anything.
func TestPermissionNonMainSessionAutoDenied(t *testing.T) {
	d := newPermTestDaemon()
	// A connected client that would receive the prompt if it were forwarded.
	sub := d.addSubscriber("client-1")
	defer d.removeSubscriber(sub)
	d.setActiveTurn("req-1", "client-1")

	// Wire an ACP client whose main session id is known.
	f := &fakeHermes{sessionID: "main-1"}
	d.acpClient = newFakeClient(t, f)
	f.start()
	if err := d.acpClient.ensureReady(); err != nil {
		t.Fatalf("ensureReady: %v", err)
	}

	forkParams := `{
		"sessionId": "fork-7",
		"toolCall": {"toolCallId": "t1", "title": "Run: rm -rf /", "kind": "execute"},
		"options": [{"optionId": "allow_once", "kind": "allow_once", "name": "Allow"}]
	}`
	if got := d.decideCopilotPermission(json.RawMessage(forkParams)); got != "" {
		t.Fatalf("non-main permission granted option %q, want denial", got)
	}
	// Nothing must have been forwarded to the client.
	select {
	case evt := <-sub.copilot:
		t.Fatalf("unexpected chunk forwarded: %+v", evt)
	default:
	}

	// The main session's requests still flow to the human (sanity: forwarded).
	mainParams := `{
		"sessionId": "main-1",
		"toolCall": {"toolCallId": "t2", "title": "Run: ls", "kind": "execute"},
		"options": [{"optionId": "allow_once", "kind": "allow_once", "name": "Allow"}]
	}`
	done := make(chan string, 1)
	go func() { done <- d.decideCopilotPermission(json.RawMessage(mainParams)) }()
	evt := waitPermChunk(t, sub, "permission_request")
	if evt.Permission == nil || evt.Permission.Title != "Run: ls" {
		t.Fatalf("main permission not forwarded: %+v", evt)
	}
	d.handleCopilotPermissionAnswer(marshalData(CopilotPermissionAnswerData{
		PermissionID: evt.Permission.PermissionID,
		OptionID:     "allow_once",
	}))
	if got := <-done; got != "allow_once" {
		t.Fatalf("main permission answer = %q", got)
	}
}
