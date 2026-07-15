package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
)

// scopedChat marshals a CopilotChatData for the handler-level tests.
func scopedChat(t *testing.T, msg, selectedID string) []byte {
	t.Helper()
	return marshalData(CopilotChatData{
		Message:   msg,
		RequestID: "req-1",
		ClientID:  "client-A",
		Scope:     &CopilotScope{SelectedSessionID: selectedID},
	})
}

// A scope naming a session that is not in the fleet must fail the request eagerly
// rather than silently retargeting a title-match sibling — the ambiguity Gate A
// guards against.
func TestCopilotChatScopeValidationFailsEagerly(t *testing.T) {
	d := &Daemon{}
	d.mu.Lock()
	d.sessions = []agent.Session{{SessionID: "sess-real", FirstMessage: "real"}}
	d.mu.Unlock()

	resp := d.handleCopilotChat(scopedChat(t, "review this", "ghost-session"))
	if resp == nil || resp.Error == "" {
		t.Fatalf("vanished scope did not fail eagerly: %+v", resp)
	}
	if !strings.Contains(resp.Error, "ghost-session") {
		t.Fatalf("error should name the vanished session, got %q", resp.Error)
	}
}

// The assembled prompt must (1) carry the selected session's identity as a
// dossier so a same-name sibling can never be confused for it, and (2) inject the
// fleet snapshot only when the fleet materially changed — a persistent Hermes
// session must not accumulate a fresh snapshot every turn.
func TestBuildCopilotPromptDossierAndFleetDelta(t *testing.T) {
	d := &Daemon{}
	d.copilotPreamble.Store(true)

	selected := agent.Session{SessionID: "sess-1", Provider: agent.ProviderClaude, Project: "spirit", GitBranch: "main", SynthesizedTitle: "fix the parser", LastUserMessage: "please review the parser change"}
	sibling := agent.Session{SessionID: "sess-2", Provider: agent.ProviderClaude, Project: "spirit", GitBranch: "wip", SynthesizedTitle: "fix the parser"} // identical display name
	sessions := []agent.Session{selected, sibling}

	req := CopilotChatData{Message: "review this", Scope: &CopilotScope{SelectedSessionID: "sess-1"}}

	p1 := d.buildCopilotPrompt(req, &selected, sessions)
	if !strings.Contains(p1, `<selected-session id="sess-1">`) {
		t.Fatalf("prompt missing scoped dossier for sess-1:\n%s", p1)
	}
	if strings.Contains(p1, `<selected-session id="sess-2">`) {
		t.Fatalf("prompt misattributed the dossier to the title-match sibling:\n%s", p1)
	}
	if !strings.Contains(p1, "<live-sessions") {
		t.Fatalf("first prompt should include the fleet snapshot once:\n%s", p1)
	}
	if !strings.Contains(p1, "review this") {
		t.Fatalf("prompt dropped the user message:\n%s", p1)
	}

	// Second turn, identical fleet: the dossier is still present, but the fleet
	// snapshot must be suppressed (delta unchanged) — no accumulation.
	p2 := d.buildCopilotPrompt(req, &selected, sessions)
	if !strings.Contains(p2, `<selected-session id="sess-1">`) {
		t.Fatalf("second prompt lost the scoped dossier:\n%s", p2)
	}
	if strings.Contains(p2, "<live-sessions") {
		t.Fatalf("identical fleet re-injected the snapshot (accumulation):\n%s", p2)
	}

	// A material fleet change (new session) re-injects the snapshot exactly once.
	sessions = append(sessions, agent.Session{SessionID: "sess-3", FirstMessage: "new work"})
	p3 := d.buildCopilotPrompt(req, &selected, sessions)
	if !strings.Contains(p3, "<live-sessions") {
		t.Fatalf("changed fleet should re-inject the snapshot:\n%s", p3)
	}
	p4 := d.buildCopilotPrompt(req, &selected, sessions)
	if strings.Contains(p4, "<live-sessions") {
		t.Fatalf("unchanged fleet re-injected the snapshot again:\n%s", p4)
	}
}

// resetFleetDelta (invoked on /new) must let the next prompt re-inject the fleet.
func TestResetFleetDeltaReinjects(t *testing.T) {
	d := &Daemon{}
	d.copilotPreamble.Store(true)
	sessions := []agent.Session{{SessionID: "sess-1", FirstMessage: "x"}}
	req := CopilotChatData{Message: "hi"}

	if !strings.Contains(d.buildCopilotPrompt(req, nil, sessions), "<live-sessions") {
		t.Fatal("first prompt should inject the fleet")
	}
	if strings.Contains(d.buildCopilotPrompt(req, nil, sessions), "<live-sessions") {
		t.Fatal("unchanged fleet should be suppressed")
	}
	d.resetFleetDelta()
	if !strings.Contains(d.buildCopilotPrompt(req, nil, sessions), "<live-sessions") {
		t.Fatal("after reset the fleet must be re-injected")
	}
}

// Stream delivery is scoped to the originating client: a second attached TUI does
// not see another client's in-flight chunks. Every delivered event carries the
// originating request/client ids.
func TestPushCopilotStreamScopedToOriginatingClient(t *testing.T) {
	d := &Daemon{subscribers: map[*subscriber]struct{}{}}
	subA := d.addSubscriber("client-A")
	subB := d.addSubscriber("client-B")

	d.pushCopilotStream(CopilotStreamData{Type: "text_delta", Content: "hi", RequestID: "req-1", ClientID: "client-A"})

	select {
	case evt := <-subA.copilot:
		if evt.RequestID != "req-1" || evt.ClientID != "client-A" {
			t.Fatalf("event to A lost correlation ids: %+v", evt)
		}
	default:
		t.Fatal("originating client A did not receive its own chunk")
	}

	select {
	case evt := <-subB.copilot:
		t.Fatalf("second client B received A's in-flight chunk: %+v", evt)
	default:
		// correct: B is isolated from A's stream
	}
}

// An event with no ClientID (older client / daemon-internal) still broadcasts to
// every subscriber, preserving back-compat.
func TestPushCopilotStreamBroadcastsWhenClientUnset(t *testing.T) {
	d := &Daemon{subscribers: map[*subscriber]struct{}{}}
	subA := d.addSubscriber("client-A")
	subB := d.addSubscriber("client-B")

	d.pushCopilotStream(CopilotStreamData{Type: "done"})

	for name, sub := range map[string]*subscriber{"A": subA, "B": subB} {
		select {
		case <-sub.copilot:
		default:
			t.Fatalf("unscoped event was not broadcast to client %s", name)
		}
	}
}

// End-to-end over the fake ACP agent: every streamed event (session marker, text
// deltas, terminal done) reaches the originating subscriber stamped with the
// turn's request/client ids.
func TestRunCopilotPromptStreamStampsRequestID(t *testing.T) {
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			f.textDelta(f.sessionID, "hello ")
			f.textDelta(f.sessionID, "world")
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		},
	}
	client := newFakeClient(t, f)
	f.start()

	d := &Daemon{subscribers: map[*subscriber]struct{}{}, acpClient: client}
	sub := d.addSubscriber("client-A")
	epoch := d.beginCopilotTurn("hi", "req-1", "client-A")

	done := make(chan struct{})
	go func() {
		d.runCopilotPromptStreaming(context.Background(), epoch, "req-1", "client-A", "hi")
		close(done)
	}()

	deadline := time.After(5 * time.Second)
	sawText, sawDone := false, false
	for !sawDone {
		select {
		case evt := <-sub.copilot:
			if evt.RequestID != "req-1" || evt.ClientID != "client-A" {
				t.Fatalf("streamed event lost correlation ids: %+v", evt)
			}
			switch evt.Type {
			case "text_delta":
				sawText = true
			case "done":
				sawDone = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for stamped stream events")
		}
	}
	if !sawText {
		t.Fatal("never saw a stamped text_delta")
	}
	<-done
}
