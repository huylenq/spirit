# Agent Provider Abstraction

## Motivation

Spirit now observes Claude Code and Codex sessions, but its core behavior is still
Claude-shaped. `ClaudeSession` is the shared model, `internal/claude` owns unrelated
state, and actions are either globally implemented or disabled with provider checks.
This does not scale: even a common-looking action such as prompt relay has different
terminal semantics. Today `>` always calls `tmux.SendKeysLiteral`, which writes the
text and immediately sends `Enter`; Codex may require a different paste/submit
sequence or timing, so text can appear without being submitted correctly.

The next step should make the agent provider—not the TUI handler—the authority on
how a capability works.

## Target Model

Introduce a provider-neutral `agent` package and migrate `ClaudeSession` to
`agent.Session`. A session retains shared orchestration state (pane, cwd, status,
project, queue, note, tags, display metadata) and carries a provider ID plus opaque
provider metadata.

```go
type ProviderID string

type Session struct {
    Provider ProviderID
    ID       string
    PaneID   string
    Status   Status
    Model    string
    // Shared Spirit fields follow.
}

type Provider interface {
    ID() ProviderID
    Capabilities(Session) CapabilitySet
    Discover(DiscoveryContext) ([]SessionCandidate, error)
    Transcript(Session) TranscriptReader
    Input(Session) InputDriver
    Lifecycle(Session) LifecycleDriver
}
```

Providers are registered once at daemon startup. UI and RPC code resolve the
provider from the selected session and never branch directly on `claude` or `codex`.

## Capabilities and Actions

Use named capabilities rather than accumulating provider booleans:

- `relay.prompt`, `relay.command`, `relay.bang`
- `queue`, `later`, `resume`, `spawn`, `kill`
- `rename.native`, `title.local`, `commit`
- `transcript.messages`, `transcript.tools`, `diff.attribution`
- `approval.observe`, `approval.respond`, `usage`, `remote_control`
- `worktree.native`, `worktree.git`

Every command has one centralized availability check. The command palette, key
handlers, daemon RPC, Lua API, and future external clients use the same result and
same unsupported reason. Provider-specific checks must not live in individual TUI
handlers.

Remote control is lifecycle policy, not a generic prompt macro. Spawn paths should
request it through `LaunchOptions.RemoteControl`; the Claude provider emits the native
`--remote-control` launch flag, while unsupported providers fail before a pane is
created. The post-spawn semantic action (`/rc` for Claude) exists only as a fallback
for sessions that are already running.

## Provider-Controlled Input

Relay should move behind an `InputDriver`:

```go
type InputDriver interface {
    SendPrompt(ctx context.Context, paneID, text string) error
    SendCommand(ctx context.Context, paneID, command string) error
}
```

The tmux transport exposes primitives, not policy:

- send literal bytes without submitting;
- send named keys;
- paste through a tmux buffer/bracketed-paste path;
- wait for a bounded duration or for a screen predicate;
- capture the pane for post-submit verification.

Claude's driver may keep literal text followed by `Enter`. Codex can independently
choose bracketed paste, a short render delay, its correct submit key sequence, and a
verification predicate. Queue delivery must call the same `SendPrompt` path as `>`;
it must not duplicate terminal behavior.

Native structured transports can later implement the same interface: Codex
app-server `turn/start`, for example, can replace tmux keystrokes without changing
the UI or queue logic.

## Migration Sequence

1. Extract provider-neutral session, status, metadata, notes, tags, queue, and Later
   persistence into `internal/agent`; retain type aliases so protocol/Lua consumers
   remain compatible during migration.
2. Add the provider registry and capability-based command gate. Move current Claude
   behavior into a Claude provider and current Codex discovery/transcript behavior
   into a Codex provider without changing UX.
3. Split tmux input into transport primitives and provider input drivers. Fix direct
   Codex relay first, then route queued prompts through the same driver.
4. Move launch/resume/rename/commit/worktree behavior behind lifecycle/action
   drivers and enable features provider-by-provider as tests become available.
5. Consider Codex app-server for structured turns, approvals, usage, and transcripts;
   keep hook/tmux discovery as the compatibility path for manually launched panes.

## Verification Contract

- Unit tests cover registry resolution, capability gating, and unsupported reasons.
- Provider contract tests run identical shared-action cases against Claude and Codex
  drivers.
- Tmux input tests use a controlled pane to verify exact text, submit sequence, and
  absence of duplicate submissions for multiline and Unicode prompts.
- Live smoke tests launch one Claude and one Codex session, relay with `>`, queue a
  second prompt, observe turn transitions, kill/reopen where supported, and verify
  the correct transcript remains attached.
- Existing daemon protocol and Lua field names stay compatible until a separately
  versioned protocol migration is introduced.

## Non-goals

The abstraction should not force all agents to offer feature parity, encode UI
styling in providers, or make Codex app-server mandatory. Providers define what they
support and how they perform it; Spirit owns presentation and orchestration.
