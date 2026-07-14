package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type Capability string

const (
	CapabilityRelayPrompt       Capability = "relay.prompt"
	CapabilityRelayCommand      Capability = "relay.command"
	CapabilityRelayBang         Capability = "relay.bang"
	CapabilityQueue             Capability = "queue"
	CapabilityLater             Capability = "later"
	CapabilityResume            Capability = "resume"
	CapabilitySpawn             Capability = "spawn"
	CapabilityKill              Capability = "kill"
	CapabilityRenameNative      Capability = "rename.native"
	CapabilityTitleLocal        Capability = "title.local"
	CapabilityCommit            Capability = "commit"
	CapabilityTranscriptMessage Capability = "transcript.messages"
	CapabilityTranscriptTools   Capability = "transcript.tools"
	CapabilityDiffAttribution   Capability = "diff.attribution"
	CapabilityApprovalObserve   Capability = "approval.observe"
	CapabilityApprovalRespond   Capability = "approval.respond"
	CapabilityUsage             Capability = "usage"
	CapabilityWorktreeNative    Capability = "worktree.native"
	CapabilityWorktreeGit       Capability = "worktree.git"
)

type CapabilitySet map[Capability]string

func NewCapabilitySet(capabilities ...Capability) CapabilitySet {
	set := make(CapabilitySet, len(capabilities))
	for _, capability := range capabilities {
		set[capability] = ""
	}
	return set
}

func (s CapabilitySet) WithUnsupported(capability Capability, reason string) CapabilitySet {
	s[capability] = reason
	return s
}

func (s CapabilitySet) Supports(capability Capability) bool {
	reason, exists := s[capability]
	return exists && reason == ""
}

func (s CapabilitySet) Reason(capability Capability) string {
	if reason, exists := s[capability]; exists && reason != "" {
		return reason
	}
	return fmt.Sprintf("%s is not supported", capability)
}

type InputDriver interface {
	SendPrompt(context.Context, string, string) error
	SendCommand(context.Context, string, string) error
}

// LifecycleDriver is deliberately small for now. Capabilities are the public
// contract; lifecycle implementations can grow without leaking into the UI.
type LifecycleDriver interface {
	LaunchCommand(Session, LaunchOptions) (string, error)
	ResumeCommand(Session) (string, error)
}

type LaunchOptions struct {
	Message  string
	Model    string
	Worktree string
}

// TerminalProfile describes provider-specific prompt rendering in a captured
// terminal. RelayPrompt is shown by Spirit while editing; PromptMarkers locate
// the provider's prompt line in captured pane content.
type TerminalProfile struct {
	RelayPrompt   string
	PromptMarkers []string
}

type Provider interface {
	ID() ProviderID
	Capabilities(Session) CapabilitySet
	Input(Session) InputDriver
	Lifecycle(Session) LifecycleDriver
	Terminal(Session) TerminalProfile
}

type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: make(map[ProviderID]Provider, len(providers))}
	for _, provider := range providers {
		r.Register(provider)
	}
	return r
}

func (r *Registry) Register(provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[provider.ID()] = provider
}

func (r *Registry) Resolve(id ProviderID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("unknown agent provider %q", id)
	}
	return provider, nil
}

func (r *Registry) Terminal(session Session) (TerminalProfile, error) {
	provider, err := r.Resolve(session.Provider)
	if err != nil {
		return TerminalProfile{}, err
	}
	return provider.Terminal(session), nil
}

func (r *Registry) IDs() []ProviderID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]ProviderID, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

type Availability struct {
	Supported bool
	Reason    string
}

func (r *Registry) Availability(session Session, capability Capability) Availability {
	provider, err := r.Resolve(session.Provider)
	if err != nil {
		return Availability{Reason: err.Error()}
	}
	set := provider.Capabilities(session)
	if set.Supports(capability) {
		return Availability{Supported: true}
	}
	return Availability{Reason: set.Reason(capability)}
}

func (r *Registry) Require(session Session, capability Capability) error {
	availability := r.Availability(session, capability)
	if availability.Supported {
		return nil
	}
	return fmt.Errorf("%s", availability.Reason)
}
