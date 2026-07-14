package ui

import "testing"

func TestRelayActivateWithPrompt(t *testing.T) {
	relay := NewRelayModel()
	relay.ActivateWithPrompt("$ ")
	if got := relay.input.Prompt; got != "$ " {
		t.Fatalf("relay prompt = %q, want %q", got, "$ ")
	}
}
