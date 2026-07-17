package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	id       ProviderID
	caps     CapabilitySet
	terminal TerminalProfile
}

func (p fakeProvider) ID() ProviderID                     { return p.id }
func (p fakeProvider) Capabilities(Session) CapabilitySet { return p.caps }
func (p fakeProvider) Input(Session) InputDriver          { return nil }
func (p fakeProvider) Lifecycle(Session) LifecycleDriver  { return nil }
func (p fakeProvider) Terminal(Session) TerminalProfile   { return p.terminal }

func TestRegistryResolutionAndCapabilityReason(t *testing.T) {
	registry := NewRegistry(fakeProvider{
		id: ProviderID("test"),
		caps: NewCapabilitySet(CapabilityRelayPrompt).
			WithUnsupported(CapabilityCommit, "test provider cannot commit"),
	})
	session := Session{Provider: ProviderID("test")}
	if got, err := registry.Resolve(session.Provider); err != nil || got.ID() != session.Provider {
		t.Fatalf("Resolve() = %v, %v", got, err)
	}
	if got := registry.Availability(session, CapabilityRelayPrompt); !got.Supported {
		t.Fatalf("relay should be supported: %+v", got)
	}
	if got := registry.Availability(session, CapabilityCommit); got.Supported || got.Reason != "test provider cannot commit" {
		t.Fatalf("unexpected commit availability: %+v", got)
	}
	if got := registry.Availability(Session{Provider: "missing"}, CapabilityRelayPrompt); got.Supported || got.Reason == "" {
		t.Fatalf("missing provider should have an unsupported reason: %+v", got)
	}
}

func TestDefaultProviderTerminalProfiles(t *testing.T) {
	tests := []struct {
		provider ProviderID
		prompt   string
		marker   string
	}{
		{provider: ProviderClaude, prompt: "❯ ", marker: "❯"},
		{provider: ProviderCodex, prompt: "› ", marker: "›"},
	}
	registry := NewDefaultRegistry()
	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			profile, err := registry.Terminal(Session{Provider: test.provider})
			if err != nil {
				t.Fatal(err)
			}
			if profile.RelayPrompt != test.prompt {
				t.Fatalf("RelayPrompt = %q, want %q", profile.RelayPrompt, test.prompt)
			}
			if !reflect.DeepEqual(profile.PromptMarkers, []string{test.marker}) {
				t.Fatalf("PromptMarkers = %#v, want %#v", profile.PromptMarkers, []string{test.marker})
			}
		})
	}
}

func TestDefaultProviderRemoteControlContracts(t *testing.T) {
	registry := NewDefaultRegistry()
	claudeSession := Session{Provider: ProviderClaude}
	claudeProvider, err := registry.Resolve(ProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Availability(claudeSession, CapabilityRemoteControl); !got.Supported {
		t.Fatalf("Claude remote control should be supported: %+v", got)
	}
	command, err := claudeProvider.Lifecycle(claudeSession).RemoteControlCommand(claudeSession)
	if err != nil || command != "/rc" {
		t.Fatalf("Claude remote control command = %q, %v; want /rc", command, err)
	}

	codexSession := Session{Provider: ProviderCodex}
	if got := registry.Availability(codexSession, CapabilityRemoteControl); got.Supported || got.Reason == "" {
		t.Fatalf("Codex remote control should be explicitly unsupported: %+v", got)
	}
}

func TestDefaultProviderLaunchRemoteControlContracts(t *testing.T) {
	registry := NewDefaultRegistry()
	claudeSession := Session{Provider: ProviderClaude}
	claudeProvider, err := registry.Resolve(ProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	command, err := claudeProvider.Lifecycle(claudeSession).LaunchCommand(
		claudeSession,
		LaunchOptions{Message: "investigate", RemoteControl: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "claude --dangerously-skip-permissions 'investigate' --remote-control"
	if command != want {
		t.Fatalf("Claude launch command = %q, want %q", command, want)
	}

	codexSession := Session{Provider: ProviderCodex}
	codexProvider, err := registry.Resolve(ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codexProvider.Lifecycle(codexSession).LaunchCommand(
		codexSession,
		LaunchOptions{RemoteControl: true},
	); err == nil || !strings.Contains(err.Error(), "only available for Claude") {
		t.Fatalf("Codex remote-control launch error = %v", err)
	}
}

type recordingTerminal struct{ operations []string }

func (r *recordingTerminal) SendLiteral(_ string, text string) error {
	r.operations = append(r.operations, "literal:"+text)
	return nil
}
func (r *recordingTerminal) SendKeys(_ string, keys ...string) error {
	r.operations = append(r.operations, "keys:"+keys[0])
	return nil
}
func (r *recordingTerminal) Paste(_ context.Context, _ string, text string) error {
	r.operations = append(r.operations, "paste:"+text)
	return nil
}

func TestProviderInputContracts(t *testing.T) {
	tests := []struct {
		name   string
		driver InputDriver
		text   string
		want   []string
	}{
		{"claude prompt", claudeInput{terminal: &recordingTerminal{}}, "hello 世界", []string{"literal:hello 世界", "keys:Enter"}},
		{"claude bang", claudeInput{terminal: &recordingTerminal{}}, "!git status", []string{"keys:!", "literal:git status", "keys:Enter"}},
		{"codex multiline", codexInput{terminal: &recordingTerminal{}, renderDelay: time.Nanosecond}, "one\ntwo 世界", []string{"paste:one\ntwo 世界", "keys:Enter"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingTerminal{}
			switch driver := test.driver.(type) {
			case claudeInput:
				driver.terminal = recorder
				test.driver = driver
			case codexInput:
				driver.terminal = recorder
				test.driver = driver
			}
			if err := test.driver.SendPrompt(context.Background(), "%1", test.text); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(recorder.operations, test.want) {
				t.Fatalf("operations = %#v, want %#v", recorder.operations, test.want)
			}
		})
	}
}
