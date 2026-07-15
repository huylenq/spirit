package daemon

import (
	"sync"
	"testing"
)

func modesFixture() map[string]any {
	return map[string]any{
		"currentModeId": "default",
		"availableModes": []any{
			map[string]any{"id": "default", "name": "Default", "description": "Ask before edits."},
			map[string]any{"id": "accept_edits", "name": "Accept Edits", "description": "Auto-allow workspace edits."},
			map[string]any{"id": "dont_ask", "name": "Don't Ask", "description": "Auto-allow non-sensitive edits."},
		},
	}
}

// session/new carries the mode state; the client captures it and exposes it via
// ModeStatus.
func TestACPCapturesModesFromSessionNew(t *testing.T) {
	f := &fakeHermes{modes: modesFixture()}
	c := newFakeClient(t, f)
	f.start()

	state, err := c.ModeStatus()
	if err != nil {
		t.Fatalf("ModeStatus: %v", err)
	}
	if state.CurrentModeID != "default" || len(state.AvailableModes) != 3 {
		t.Fatalf("mode state = %+v", state)
	}
}

// SetMode puts the resolved mode id on the wire, updates state, and persists it.
func TestACPSetModePersists(t *testing.T) {
	var mu sync.Mutex
	persistedMode := ""
	f := &fakeHermes{modes: modesFixture()}
	c := newFakeClient(t, f)
	c.writeMode = func(id string) { mu.Lock(); persistedMode = id; mu.Unlock() }
	c.readMode = func() string { mu.Lock(); defer mu.Unlock(); return persistedMode }
	f.start()

	state, err := c.SetMode("dont_ask")
	if err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if state.CurrentModeID != "dont_ask" {
		t.Fatalf("current mode = %q", state.CurrentModeID)
	}
	f.mu.Lock()
	last := f.lastSetMode
	f.mu.Unlock()
	if last == nil || last["modeId"] != "dont_ask" {
		t.Fatalf("wire set_mode = %#v", last)
	}
	mu.Lock()
	pm := persistedMode
	mu.Unlock()
	if pm != "dont_ask" {
		t.Fatalf("persisted mode = %q, want dont_ask", pm)
	}
}

// SetMode resolves a friendly mode name to its canonical id.
func TestACPSetModeResolvesName(t *testing.T) {
	f := &fakeHermes{modes: modesFixture()}
	c := newFakeClient(t, f)
	c.writeMode = func(string) {}
	c.readMode = func() string { return "" }
	f.start()

	if _, err := c.SetMode("Accept Edits"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	f.mu.Lock()
	last := f.lastSetMode
	f.mu.Unlock()
	if last["modeId"] != "accept_edits" {
		t.Fatalf("resolved mode id = %q, want accept_edits", last["modeId"])
	}
}

// A persisted mode is re-applied on session open, so a fresh session honors the
// autonomy ceiling instead of Hermes's default.
func TestACPReappliesPersistedModeOnOpen(t *testing.T) {
	f := &fakeHermes{modes: modesFixture()}
	c := newFakeClient(t, f)
	c.writeMode = func(string) {}
	c.readMode = func() string { return "dont_ask" } // persisted ceiling
	f.start()

	if _, err := c.ModelStatus(); err != nil { // brings the session up (handshake)
		t.Fatalf("ModelStatus: %v", err)
	}
	f.mu.Lock()
	last := f.lastSetMode
	f.mu.Unlock()
	if last == nil || last["modeId"] != "dont_ask" {
		t.Fatalf("persisted mode not re-applied on open: %#v", last)
	}
	state, _ := c.ModeStatus()
	if state.CurrentModeID != "dont_ask" {
		t.Fatalf("current mode after reapply = %q, want dont_ask", state.CurrentModeID)
	}
}

// A current_mode_update stream event updates the client's mode state and surfaces a
// `mode` chunk.
func TestACPConsumesCurrentModeUpdate(t *testing.T) {
	f := &fakeHermes{modes: modesFixture()}
	c := newFakeClient(t, f)
	f.start()

	var got []CopilotStreamData
	id := c.setSink(func(evt CopilotStreamData) { got = append(got, evt) })
	defer c.clearSink(id)

	c.dispatchUpdate([]byte(`{"sessionId":"s","update":{"sessionUpdate":"current_mode_update","currentModeId":"accept_edits"}}`))

	if len(got) != 1 || got[0].Type != "mode" || got[0].Content != "accept_edits" {
		t.Fatalf("mode chunk = %#v", got)
	}
	c.mu.Lock()
	cur := c.modes.CurrentModeID
	c.mu.Unlock()
	if cur != "accept_edits" {
		t.Fatalf("client mode = %q, want accept_edits", cur)
	}
}
