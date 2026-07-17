package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/tmux"
)

type builtinProvider struct {
	id           ProviderID
	capabilities CapabilitySet
	input        InputDriver
	lifecycle    LifecycleDriver
	terminal     TerminalProfile
}

func (p builtinProvider) ID() ProviderID                     { return p.id }
func (p builtinProvider) Capabilities(Session) CapabilitySet { return p.capabilities }
func (p builtinProvider) Input(Session) InputDriver          { return p.input }
func (p builtinProvider) Lifecycle(Session) LifecycleDriver  { return p.lifecycle }
func (p builtinProvider) Terminal(Session) TerminalProfile   { return p.terminal }

type terminalTransport interface {
	SendLiteral(string, string) error
	SendKeys(string, ...string) error
	Paste(context.Context, string, string) error
}

type tmuxTransport struct{}

func (tmuxTransport) SendLiteral(paneID, text string) error { return tmux.SendLiteral(paneID, text) }
func (tmuxTransport) SendKeys(paneID string, keys ...string) error {
	return tmux.SendNamedKeys(paneID, keys...)
}
func (tmuxTransport) Paste(ctx context.Context, paneID, text string) error {
	return tmux.PasteText(ctx, paneID, text)
}

type claudeInput struct{ terminal terminalTransport }

func (d claudeInput) SendPrompt(_ context.Context, paneID, text string) error {
	if strings.HasPrefix(text, "!") {
		if err := d.terminal.SendKeys(paneID, "!"); err != nil {
			return fmt.Errorf("send bang key: %w", err)
		}
		text = strings.TrimPrefix(text, "!")
		if text == "" {
			return nil
		}
	}
	if err := d.terminal.SendLiteral(paneID, text); err != nil {
		return err
	}
	return d.terminal.SendKeys(paneID, "Enter")
}

func (d claudeInput) SendCommand(ctx context.Context, paneID, command string) error {
	return d.SendPrompt(ctx, paneID, command)
}

type codexInput struct {
	renderDelay time.Duration
	terminal    terminalTransport
}

func (d codexInput) SendPrompt(ctx context.Context, paneID, text string) error {
	if err := d.terminal.Paste(ctx, paneID, text); err != nil {
		return err
	}
	delay := d.renderDelay
	if delay <= 0 {
		delay = 80 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if err := d.terminal.SendKeys(paneID, "Enter"); err != nil {
		return fmt.Errorf("submit Codex prompt: %w", err)
	}
	return nil
}

func (d codexInput) SendCommand(ctx context.Context, paneID, command string) error {
	return d.SendPrompt(ctx, paneID, command)
}

type builtinLifecycle struct{ id ProviderID }

func (d builtinLifecycle) LaunchCommand(_ Session, options LaunchOptions) (string, error) {
	switch d.id {
	case ProviderClaude:
		command := "claude --dangerously-skip-permissions"
		if options.Model != "" {
			command += " --model " + shellQuote(options.Model)
		}
		if options.Worktree != "" {
			command += " --worktree " + shellQuote(options.Worktree)
		}
		if options.Message != "" {
			command += " " + shellQuote(options.Message)
		}
		// --remote-control accepts an optional name. Put the flag after the positional
		// prompt so Claude's parser cannot consume the prompt as that optional name.
		if options.RemoteControl {
			command += " --remote-control"
		}
		return command, nil
	case ProviderCodex:
		if options.RemoteControl {
			return "", fmt.Errorf("remote control is only available for Claude sessions")
		}
		if options.Worktree != "" {
			return "", fmt.Errorf("Codex does not support Claude worktree launch options")
		}
		command := "codex --dangerously-bypass-approvals-and-sandbox"
		if options.Model != "" {
			command += " --model " + shellQuote(options.Model)
		}
		if options.Message != "" {
			command += " " + shellQuote(options.Message)
		}
		return command, nil
	default:
		return "", fmt.Errorf("unknown provider %q", d.id)
	}
}

func (d builtinLifecycle) ResumeCommand(session Session) (string, error) {
	if session.SessionID == "" {
		return "", fmt.Errorf("session ID is required to resume")
	}
	switch d.id {
	case ProviderClaude:
		return "claude --dangerously-skip-permissions --resume " + shellQuote(session.SessionID), nil
	case ProviderCodex:
		return "codex resume " + shellQuote(session.SessionID), nil
	default:
		return "", fmt.Errorf("unknown provider %q", d.id)
	}
}

func (d builtinLifecycle) RemoteControlCommand(Session) (string, error) {
	switch d.id {
	case ProviderClaude:
		return "/rc", nil
	case ProviderCodex:
		return "", fmt.Errorf("remote control is only available for Claude sessions")
	default:
		return "", fmt.Errorf("unknown provider %q", d.id)
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func NewDefaultRegistry() *Registry {
	terminal := tmuxTransport{}
	shared := []Capability{
		CapabilityRelayPrompt, CapabilityRelayCommand, CapabilityQueue,
		CapabilityKill, CapabilityTitleLocal, CapabilityTranscriptMessage,
		CapabilityTranscriptTools, CapabilityDiffAttribution,
	}
	claudeCaps := NewCapabilitySet(append(shared,
		CapabilityRelayBang, CapabilityLater, CapabilityResume, CapabilitySpawn,
		CapabilityRenameNative, CapabilityCommit, CapabilityApprovalObserve,
		CapabilityUsage, CapabilityRemoteControl, CapabilityWorktreeNative, CapabilityWorktreeGit,
	)...)
	codexCaps := NewCapabilitySet(append(shared, CapabilityResume, CapabilitySpawn)...)
	codexCaps.WithUnsupported(CapabilityRelayBang, "bang mode is only available for Claude sessions")
	codexCaps.WithUnsupported(CapabilityLater, "Later is not yet available for Codex sessions")
	codexCaps.WithUnsupported(CapabilityRenameNative, "native rename is not available for Codex sessions")
	codexCaps.WithUnsupported(CapabilityCommit, "commit automation is not available for Codex sessions")
	codexCaps.WithUnsupported(CapabilityRemoteControl, "remote control is only available for Claude sessions")

	return NewRegistry(
		builtinProvider{
			id: ProviderClaude, capabilities: claudeCaps,
			input: claudeInput{terminal: terminal}, lifecycle: builtinLifecycle{id: ProviderClaude},
			terminal: TerminalProfile{RelayPrompt: "❯ ", PromptMarkers: []string{"❯"}},
		},
		builtinProvider{
			id: ProviderCodex, capabilities: codexCaps,
			input: codexInput{terminal: terminal}, lifecycle: builtinLifecycle{id: ProviderCodex},
			terminal: TerminalProfile{RelayPrompt: "› ", PromptMarkers: []string{"›"}},
		},
	)
}
