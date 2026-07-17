package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/agent"
)

type testInput struct{ prompts []string }

func (i *testInput) SendPrompt(_ context.Context, _ string, text string) error {
	i.prompts = append(i.prompts, text)
	return nil
}
func (i *testInput) SendCommand(ctx context.Context, paneID, text string) error {
	return i.SendPrompt(ctx, paneID, text)
}

type testLifecycle struct{}

func (testLifecycle) LaunchCommand(agent.Session, agent.LaunchOptions) (string, error) {
	return "test", nil
}
func (testLifecycle) ResumeCommand(agent.Session) (string, error)        { return "test", nil }
func (testLifecycle) RemoteControlCommand(agent.Session) (string, error) { return "/rc", nil }

type testProvider struct {
	input *testInput
	caps  agent.CapabilitySet
}

func (testProvider) ID() agent.ProviderID                             { return "test" }
func (p testProvider) Capabilities(agent.Session) agent.CapabilitySet { return p.caps }
func (p testProvider) Input(agent.Session) agent.InputDriver          { return p.input }
func (testProvider) Lifecycle(agent.Session) agent.LifecycleDriver    { return testLifecycle{} }
func (testProvider) Terminal(agent.Session) agent.TerminalProfile     { return agent.TerminalProfile{} }

func TestRelayUsesProviderGateAndInput(t *testing.T) {
	input := &testInput{}
	provider := testProvider{input: input, caps: agent.NewCapabilitySet(agent.CapabilityRelayPrompt).
		WithUnsupported(agent.CapabilityRelayBang, "test provider has no bang mode")}
	d := &Daemon{
		providers: agent.NewRegistry(provider),
		sessions:  []agent.Session{{Provider: provider.ID(), PaneID: "%1", SessionID: "s1"}},
	}

	payload, _ := json.Marshal(RelayData{PaneID: "%1", Message: "hello", Capability: agent.CapabilityRelayPrompt})
	if response := d.handleRelay(payload); response.Error != "" {
		t.Fatalf("prompt relay failed: %s", response.Error)
	}
	if len(input.prompts) != 1 || input.prompts[0] != "hello" {
		t.Fatalf("provider input got %#v", input.prompts)
	}

	payload, _ = json.Marshal(RelayData{PaneID: "%1", Message: "!", Capability: agent.CapabilityRelayBang})
	response := d.handleRelay(payload)
	if !strings.Contains(response.Error, "test provider has no bang mode") {
		t.Fatalf("unsupported reason = %q", response.Error)
	}
	if len(input.prompts) != 1 {
		t.Fatalf("unsupported relay reached input driver: %#v", input.prompts)
	}
}

func TestRemoteControlUsesProviderCapabilityAndCommand(t *testing.T) {
	input := &testInput{}
	provider := testProvider{
		input: input,
		caps:  agent.NewCapabilitySet(agent.CapabilityRemoteControl),
	}
	d := &Daemon{
		providers: agent.NewRegistry(provider),
		sessions: []agent.Session{{
			Provider: provider.ID(), PaneID: "%1", SessionID: "s1", Status: agent.StatusUserTurn,
		}},
	}

	payload, _ := json.Marshal(SessionIDData{SessionID: "s1"})
	if response := d.handleEnableRemoteControl(payload); response.Error != "" {
		t.Fatalf("enable remote control failed: %s", response.Error)
	}
	if len(input.prompts) != 1 || input.prompts[0] != "/rc" {
		t.Fatalf("provider input got %#v", input.prompts)
	}

	provider.caps = agent.NewCapabilitySet().WithUnsupported(
		agent.CapabilityRemoteControl,
		"test provider has no remote control",
	)
	d.providers = agent.NewRegistry(provider)
	response := d.handleEnableRemoteControl(payload)
	if !strings.Contains(response.Error, "test provider has no remote control") {
		t.Fatalf("unsupported reason = %q", response.Error)
	}
	if len(input.prompts) != 1 {
		t.Fatalf("unsupported remote control reached input driver: %#v", input.prompts)
	}
}

func TestRemoteControlRejectsWorkingSession(t *testing.T) {
	input := &testInput{}
	provider := testProvider{
		input: input,
		caps:  agent.NewCapabilitySet(agent.CapabilityRemoteControl),
	}
	d := &Daemon{
		providers: agent.NewRegistry(provider),
		sessions: []agent.Session{{
			Provider: provider.ID(), PaneID: "%1", SessionID: "s1", Status: agent.StatusAgentTurn,
		}},
	}

	payload, _ := json.Marshal(SessionIDData{SessionID: "s1"})
	response := d.handleEnableRemoteControl(payload)
	if !strings.Contains(response.Error, "must be idle") {
		t.Fatalf("busy-session error = %q", response.Error)
	}
	if len(input.prompts) != 0 {
		t.Fatalf("busy remote control reached input driver: %#v", input.prompts)
	}
}
