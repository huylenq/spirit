package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForkSessionCapabilityGate(t *testing.T) {
	f := &fakeHermes{agentCaps: map[string]any{
		"loadSession":         true,
		"sessionCapabilities": map[string]any{"list": map[string]any{}},
	}}
	c := newFakeClient(t, f)
	f.start()

	if err := c.ensureReady(); err != nil {
		t.Fatalf("ensureReady: %v", err)
	}
	if c.ForkCapable() {
		t.Fatalf("ForkCapable = true without advertised fork capability")
	}
	if _, err := c.ForkSession(); err != errACPForkUnsupported {
		t.Fatalf("ForkSession err = %v, want errACPForkUnsupported", err)
	}
}

func TestForkSessionForksMainSession(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-9"}
	c := newFakeClient(t, f)
	f.start()

	forkID, err := c.ForkSession()
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if forkID != "fork-9" {
		t.Fatalf("forkID = %q", forkID)
	}
	f.mu.Lock()
	from := f.lastForkFrom
	f.mu.Unlock()
	if from != "main-1" {
		t.Fatalf("forked from %q, want main-1", from)
	}
}

// TestForkStreamIsolation runs a main prompt and a fork prompt concurrently over
// the same wire and asserts that neither session's text reaches the other's
// consumer — the property that lets a reactive run coexist with a user turn.
func TestForkStreamIsolation(t *testing.T) {
	mainStarted := make(chan struct{})
	releaseMain := make(chan struct{})
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-1"}
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		switch sessionID {
		case "main-1":
			f.textDelta("main-1", "MAIN-TEXT ")
			close(mainStarted)
			<-releaseMain // hold the main turn open while the fork runs
			f.textDelta("main-1", "MAIN-END")
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		default:
			f.textDelta(sessionID, "FORK-RECOMMENDATION")
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		}
	}
	c := newFakeClient(t, f)
	f.start()

	var mu sync.Mutex
	var mainEvents []string
	mainDone := make(chan string, 1)
	go func() {
		out, err := c.Prompt(context.Background(), "user turn", func(evt CopilotStreamData) {
			if evt.Type == "text_delta" {
				mu.Lock()
				mainEvents = append(mainEvents, evt.Content)
				mu.Unlock()
			}
		})
		if err != nil {
			t.Errorf("main prompt: %v", err)
		}
		mainDone <- out
	}()

	<-mainStarted

	// Reactive run mid-user-turn: fork + prompt on the fork session.
	forkID, err := c.ForkSession()
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rec, err := c.PromptSession(ctx, forkID, "recommend something")
	if err != nil {
		t.Fatalf("PromptSession: %v", err)
	}
	if rec != "FORK-RECOMMENDATION" {
		t.Fatalf("fork text = %q", rec)
	}

	close(releaseMain)
	mainOut := <-mainDone
	if mainOut != "MAIN-TEXT MAIN-END" {
		t.Fatalf("main text = %q (fork leaked into main?)", mainOut)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, ev := range mainEvents {
		if strings.Contains(ev, "FORK") {
			t.Fatalf("fork chunk leaked into main sink: %q", ev)
		}
	}
}

func TestPromptSessionTimeout(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1"}
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		if sessionID == "main-1" {
			f.reply(id, map[string]any{"stopReason": "end_turn"})
			return
		}
		// Never reply for the fork: force the caller's deadline.
	}
	c := newFakeClient(t, f)
	f.start()
	if err := c.ensureReady(); err != nil {
		t.Fatalf("ensureReady: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.PromptSession(ctx, "fork-x", "hi"); err == nil {
		t.Fatalf("expected timeout error")
	}
	// The bounded run must have asked Hermes to cancel the turn.
	select {
	case <-f.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatalf("no session/cancel after fork prompt timeout")
	}
}
