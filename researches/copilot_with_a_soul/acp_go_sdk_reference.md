# ACP Go SDK Reference (`github.com/coder/acp-go-sdk`)

**Version**: v0.10.8 (latest as of research date)
**Install**: `go get github.com/coder/acp-go-sdk@v0.10.8`
**Protocol Version**: 1

## Architecture Overview

ACP uses **JSON-RPC 2.0 over line-delimited JSON** (newline-separated) over stdio pipes.

Two sides:
- **Client** (cmc) -- creates connection, sends prompts, receives streaming updates
- **Agent** (Claude Code) -- runs as subprocess, processes prompts, streams updates back

The client spawns the agent as a subprocess and connects via stdin/stdout pipes.

---

## Constants

```go
const ProtocolVersionNumber = 1

// Agent method names (client calls these ON the agent)
const (
    AgentMethodAuthenticate           = "authenticate"
    AgentMethodInitialize             = "initialize"
    AgentMethodSessionCancel          = "session/cancel"
    AgentMethodSessionFork            = "session/fork"
    AgentMethodSessionList            = "session/list"
    AgentMethodSessionLoad            = "session/load"
    AgentMethodSessionNew             = "session/new"
    AgentMethodSessionPrompt          = "session/prompt"
    AgentMethodSessionResume          = "session/resume"
    AgentMethodSessionSetConfigOption = "session/set_config_option"
    AgentMethodSessionSetMode         = "session/set_mode"
    AgentMethodSessionSetModel        = "session/set_model"
)

// Client method names (agent calls these ON the client)
const (
    ClientMethodFsReadTextFile           = "fs/read_text_file"
    ClientMethodFsWriteTextFile          = "fs/write_text_file"
    ClientMethodSessionRequestPermission = "session/request_permission"
    ClientMethodSessionUpdate            = "session/update"
    ClientMethodTerminalCreate           = "terminal/create"
    ClientMethodTerminalKill             = "terminal/kill"
    ClientMethodTerminalOutput           = "terminal/output"
    ClientMethodTerminalRelease          = "terminal/release"
    ClientMethodTerminalWaitForExit      = "terminal/wait_for_exit"
)
```

---

## Core Interfaces

### Client Interface (what cmc must implement)

```go
type Client interface {
    // File system operations (agent requests these from client)
    ReadTextFile(ctx context.Context, params ReadTextFileRequest) (ReadTextFileResponse, error)
    WriteTextFile(ctx context.Context, params WriteTextFileRequest) (WriteTextFileResponse, error)

    // Permission handling (agent asks client for permission before sensitive ops)
    RequestPermission(ctx context.Context, params RequestPermissionRequest) (RequestPermissionResponse, error)

    // STREAMING UPDATES -- this is the main callback for receiving agent output
    SessionUpdate(ctx context.Context, params SessionNotification) error

    // Terminal operations
    CreateTerminal(ctx context.Context, params CreateTerminalRequest) (CreateTerminalResponse, error)
    KillTerminalCommand(ctx context.Context, params KillTerminalCommandRequest) (KillTerminalCommandResponse, error)
    TerminalOutput(ctx context.Context, params TerminalOutputRequest) (TerminalOutputResponse, error)
    ReleaseTerminal(ctx context.Context, params ReleaseTerminalRequest) (ReleaseTerminalResponse, error)
    WaitForTerminalExit(ctx context.Context, params WaitForTerminalExitRequest) (WaitForTerminalExitResponse, error)
}
```

### Agent Interface (what the agent must implement -- NOT relevant for cmc client side)

```go
type Agent interface {
    Authenticate(ctx context.Context, params AuthenticateRequest) (AuthenticateResponse, error)
    Initialize(ctx context.Context, params InitializeRequest) (InitializeResponse, error)
    Cancel(ctx context.Context, params CancelNotification) error
    NewSession(ctx context.Context, params NewSessionRequest) (NewSessionResponse, error)
    Prompt(ctx context.Context, params PromptRequest) (PromptResponse, error)
    SetSessionConfigOption(ctx context.Context, params SetSessionConfigOptionRequest) (SetSessionConfigOptionResponse, error)
    SetSessionMode(ctx context.Context, params SetSessionModeRequest) (SetSessionModeResponse, error)
}

type AgentLoader interface {
    LoadSession(ctx context.Context, params LoadSessionRequest) (LoadSessionResponse, error)
}

type AgentExperimental interface {
    UnstableForkSession(ctx context.Context, params UnstableForkSessionRequest) (UnstableForkSessionResponse, error)
    UnstableListSessions(ctx context.Context, params UnstableListSessionsRequest) (UnstableListSessionsResponse, error)
    UnstableResumeSession(ctx context.Context, params UnstableResumeSessionRequest) (UnstableResumeSessionResponse, error)
    UnstableSetSessionModel(ctx context.Context, params UnstableSetSessionModelRequest) (UnstableSetSessionModelResponse, error)
}
```

---

## Connection Types

### ClientSideConnection (what cmc uses)

```go
type ClientSideConnection struct {
    conn   *Connection
    client Client  // your implementation
}

func NewClientSideConnection(client Client, peerInput io.Writer, peerOutput io.Reader) *ClientSideConnection

func (c *ClientSideConnection) Done() <-chan struct{}          // closed when peer disconnects
func (c *ClientSideConnection) SetLogger(l *slog.Logger)

// Outbound methods (client -> agent)
func (c *ClientSideConnection) Authenticate(ctx context.Context, params AuthenticateRequest) (AuthenticateResponse, error)
func (c *ClientSideConnection) Initialize(ctx context.Context, params InitializeRequest) (InitializeResponse, error)
func (c *ClientSideConnection) Cancel(ctx context.Context, params CancelNotification) error  // notification, no response
func (c *ClientSideConnection) NewSession(ctx context.Context, params NewSessionRequest) (NewSessionResponse, error)
func (c *ClientSideConnection) LoadSession(ctx context.Context, params LoadSessionRequest) (LoadSessionResponse, error)
func (c *ClientSideConnection) Prompt(ctx context.Context, params PromptRequest) (PromptResponse, error)
func (c *ClientSideConnection) SetSessionMode(ctx context.Context, params SetSessionModeRequest) (SetSessionModeResponse, error)
func (c *ClientSideConnection) SetSessionConfigOption(ctx context.Context, params SetSessionConfigOptionRequest) (SetSessionConfigOptionResponse, error)

// Unstable methods
func (c *ClientSideConnection) UnstableForkSession(ctx context.Context, params UnstableForkSessionRequest) (UnstableForkSessionResponse, error)
func (c *ClientSideConnection) UnstableListSessions(ctx context.Context, params UnstableListSessionsRequest) (UnstableListSessionsResponse, error)
func (c *ClientSideConnection) UnstableResumeSession(ctx context.Context, params UnstableResumeSessionRequest) (UnstableResumeSessionResponse, error)
func (c *ClientSideConnection) UnstableSetSessionModel(ctx context.Context, params UnstableSetSessionModelRequest) (UnstableSetSessionModelResponse, error)

// Extension methods
func (c *ClientSideConnection) CallExtension(ctx context.Context, method string, params any) (json.RawMessage, error)
func (c *ClientSideConnection) NotifyExtension(ctx context.Context, method string, params any) error
```

**Key behavior of `Prompt()`**: If the context is cancelled while waiting for a response, it automatically sends a `Cancel` notification to the agent. The `Prompt` call blocks until the agent finishes the entire turn (all streaming updates are received via `SessionUpdate` callback before `Prompt` returns).

### Base Connection

```go
type Connection struct { /* internal */ }

func NewConnection(handler MethodHandler, peerInput io.Writer, peerOutput io.Reader) *Connection

func (c *Connection) Done() <-chan struct{}
func (c *Connection) SendNotification(ctx context.Context, method string, params any) error
func (c *Connection) SendRequestNoResult(ctx context.Context, method string, params any) error
func (c *Connection) SetLogger(l *slog.Logger)

func SendRequest[T any](c *Connection, ctx context.Context, method string, params any) (T, error)

type MethodHandler func(ctx context.Context, method string, params json.RawMessage) (any, *RequestError)
```

---

## Initialization Types

```go
type ProtocolVersion int

type InitializeRequest struct {
    Meta               map[string]any     `json:"_meta,omitempty"`
    ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
    ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
    ProtocolVersion    ProtocolVersion    `json:"protocolVersion"`
}

type InitializeResponse struct {
    Meta              map[string]any    `json:"_meta,omitempty"`
    AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
    AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
    AuthMethods       []AuthMethod      `json:"authMethods,omitempty"`
    ProtocolVersion   ProtocolVersion   `json:"protocolVersion"`
}

type ClientCapabilities struct {
    Meta     map[string]any       `json:"_meta,omitempty"`
    Fs       FileSystemCapability `json:"fs"`
    Terminal bool                 `json:"terminal"`
}

type FileSystemCapability struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    ReadTextFile  bool           `json:"readTextFile"`
    WriteTextFile bool           `json:"writeTextFile"`
}

type AgentCapabilities struct {
    Meta                map[string]any      `json:"_meta,omitempty"`
    LoadSession         bool                `json:"loadSession"`
    McpCapabilities     McpCapabilities     `json:"mcpCapabilities"`
    PromptCapabilities  PromptCapabilities  `json:"promptCapabilities"`
    SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
}

type McpCapabilities struct {
    Meta map[string]any `json:"_meta,omitempty"`
    Http bool           `json:"http"`
    Sse  bool           `json:"sse"`
}

type PromptCapabilities struct {
    Meta            map[string]any `json:"_meta,omitempty"`
    Audio           bool           `json:"audio"`
    EmbeddedContext bool           `json:"embeddedContext"`
    Image           bool           `json:"image"`
}

type SessionCapabilities struct {
    Meta   map[string]any            `json:"_meta,omitempty"`
    Fork   *SessionForkCapabilities  `json:"fork,omitempty"`
    List   *SessionListCapabilities  `json:"list,omitempty"`
    Resume *SessionResumeCapabilities `json:"resume,omitempty"`
}

type Implementation struct {
    Meta    map[string]any `json:"_meta,omitempty"`
    Name    string         `json:"name"`
    Title   *string        `json:"title,omitempty"`
    Version string         `json:"version"`
}
```

---

## Session Types

```go
type SessionId string
type SessionModeId string
type ModelId string

type NewSessionRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    Cwd        string         `json:"cwd"`
    McpServers []McpServer    `json:"mcpServers,omitempty"`
}

type NewSessionResponse struct {
    Meta          map[string]any        `json:"_meta,omitempty"`
    ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
    Models        *SessionModelState    `json:"models,omitempty"`
    Modes         *SessionModeState     `json:"modes,omitempty"`
    SessionId     SessionId             `json:"sessionId"`
}

type LoadSessionRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    Cwd        string         `json:"cwd,omitempty"`
    McpServers []McpServer    `json:"mcpServers,omitempty"`
    SessionId  SessionId      `json:"sessionId"`
}

type LoadSessionResponse struct {
    Meta          map[string]any        `json:"_meta,omitempty"`
    ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
    Models        *SessionModelState    `json:"models,omitempty"`
    Modes         *SessionModeState     `json:"modes,omitempty"`
}

type CancelNotification struct {
    Meta      map[string]any `json:"_meta,omitempty"`
    SessionId SessionId      `json:"sessionId"`
}
```

---

## MCP Server Types (passed during session creation)

```go
// Union type -- exactly one field should be set
type McpServer struct {
    Http  *McpServerHttpInline `json:"-"`
    Sse   *McpServerSseInline  `json:"-"`
    Stdio *McpServerStdio      `json:"-"`
}
// Custom MarshalJSON/UnmarshalJSON handles discriminated union

type McpServerHttpInline struct {
    Meta    map[string]any `json:"_meta,omitempty"`
    Headers []HttpHeader   `json:"headers,omitempty"`
    Name    string         `json:"name"`
    Type    string         `json:"type"`  // discriminator
    Url     string         `json:"url"`
}

type McpServerSseInline struct {
    Meta    map[string]any `json:"_meta,omitempty"`
    Headers []HttpHeader   `json:"headers,omitempty"`
    Name    string         `json:"name"`
    Type    string         `json:"type"`  // discriminator
    Url     string         `json:"url"`
}

type McpServerStdio struct {
    Meta    map[string]any `json:"_meta,omitempty"`
    Args    []string       `json:"args,omitempty"`
    Command string         `json:"command"`
    Env     []EnvVariable  `json:"env,omitempty"`
    Name    string         `json:"name"`
}

type HttpHeader struct {
    Meta  map[string]any `json:"_meta,omitempty"`
    Name  string         `json:"name"`
    Value string         `json:"value"`
}

type EnvVariable struct {
    Meta  map[string]any `json:"_meta,omitempty"`
    Name  string         `json:"name"`
    Value string         `json:"value"`
}
```

---

## Prompt Types

```go
type PromptRequest struct {
    Meta      map[string]any `json:"_meta,omitempty"`
    Prompt    []ContentBlock `json:"prompt"`
    SessionId SessionId      `json:"sessionId"`
}

type PromptResponse struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    StopReason StopReason     `json:"stopReason"`
    Usage      *Usage         `json:"usage,omitempty"`
}

type StopReason string
const (
    StopReasonEndTurn         StopReason = "end_turn"
    StopReasonMaxTokens       StopReason = "max_tokens"
    StopReasonMaxTurnRequests StopReason = "max_turn_requests"
    StopReasonRefusal         StopReason = "refusal"
    StopReasonCancelled       StopReason = "cancelled"
)
```

---

## Content Block Types

```go
// Union type -- exactly one field should be set
type ContentBlock struct {
    Text         *ContentBlockText         `json:"-"`
    Image        *ContentBlockImage        `json:"-"`
    Audio        *ContentBlockAudio        `json:"-"`
    ResourceLink *ContentBlockResourceLink `json:"-"`
    Resource     *ContentBlockResource     `json:"-"`
}

type ContentBlockText struct {
    Meta        map[string]any `json:"_meta,omitempty"`
    Annotations *Annotations   `json:"annotations,omitempty"`
    Text        string         `json:"text"`
    Type        string         `json:"type"`  // "text"
}

type ContentBlockImage struct {
    Meta        map[string]any `json:"_meta,omitempty"`
    Annotations *Annotations   `json:"annotations,omitempty"`
    Data        string         `json:"data"`      // base64
    MimeType    string         `json:"mimeType"`
    Type        string         `json:"type"`       // "image"
    Uri         *string        `json:"uri,omitempty"`
}

type ContentBlockAudio struct {
    Meta        map[string]any `json:"_meta,omitempty"`
    Annotations *Annotations   `json:"annotations,omitempty"`
    Data        string         `json:"data"`      // base64
    MimeType    string         `json:"mimeType"`
    Type        string         `json:"type"`       // "audio"
}

type ContentBlockResourceLink struct {
    Meta        map[string]any `json:"_meta,omitempty"`
    Annotations *Annotations   `json:"annotations,omitempty"`
    Description *string        `json:"description,omitempty"`
    MimeType    *string        `json:"mimeType,omitempty"`
    Name        string         `json:"name"`
    Size        *int           `json:"size,omitempty"`
    Title       *string        `json:"title,omitempty"`
    Type        string         `json:"type"`  // "resource_link"
    Uri         string         `json:"uri"`
}

type ContentBlockResource struct {
    Meta        map[string]any       `json:"_meta,omitempty"`
    Annotations *Annotations         `json:"annotations,omitempty"`
    Resource    EmbeddedResourceResource `json:"resource"`
    Type        string               `json:"type"`  // "resource"
}

// Helper constructors
func TextBlock(text string) ContentBlock
func ImageBlock(data string, mimeType string) ContentBlock
func AudioBlock(data string, mimeType string) ContentBlock
func ResourceLinkBlock(name string, uri string) ContentBlock
func ResourceBlock(res EmbeddedResourceResource) ContentBlock
```

---

## Session Update Types (the streaming callback data)

### SessionNotification (wrapper delivered to `Client.SessionUpdate`)

```go
type SessionNotification struct {
    Meta      map[string]any `json:"_meta,omitempty"`
    SessionId SessionId      `json:"sessionId"`
    Update    SessionUpdate  `json:"update"`
}
```

### SessionUpdate (discriminated union -- exactly one field is set)

```go
type SessionUpdate struct {
    UserMessageChunk        *SessionUpdateUserMessageChunk    `json:"-"`
    AgentMessageChunk       *SessionUpdateAgentMessageChunk   `json:"-"`
    AgentThoughtChunk       *SessionUpdateAgentThoughtChunk   `json:"-"`
    ToolCall                *SessionUpdateToolCall            `json:"-"`
    ToolCallUpdate          *SessionToolCallUpdate            `json:"-"`
    Plan                    *SessionUpdatePlan                `json:"-"`
    AvailableCommandsUpdate *SessionAvailableCommandsUpdate   `json:"-"`
    CurrentModeUpdate       *SessionCurrentModeUpdate         `json:"-"`
    ConfigOptionUpdate      *SessionConfigOptionUpdate        `json:"-"`
    SessionInfoUpdate       *SessionSessionInfoUpdate         `json:"-"`
    UsageUpdate             *SessionUsageUpdate               `json:"-"`
}
```

Discriminated by `"sessionUpdate"` JSON field:
- `"user_message_chunk"` -> UserMessageChunk
- `"agent_message_chunk"` -> AgentMessageChunk
- `"agent_thought_chunk"` -> AgentThoughtChunk
- `"tool_call"` -> ToolCall
- `"tool_call_update"` -> ToolCallUpdate
- `"plan"` -> Plan
- `"available_commands"` -> AvailableCommandsUpdate
- `"current_mode"` -> CurrentModeUpdate
- `"config_option"` -> ConfigOptionUpdate
- `"session_info"` -> SessionInfoUpdate
- `"usage"` -> UsageUpdate

### Update Variant Structs

```go
type SessionUpdateUserMessageChunk struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    Content       ContentBlock   `json:"content"`
    SessionUpdate string         `json:"sessionUpdate"`  // "user_message_chunk"
}

type SessionUpdateAgentMessageChunk struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    Content       ContentBlock   `json:"content"`
    SessionUpdate string         `json:"sessionUpdate"`  // "agent_message_chunk"
}

type SessionUpdateAgentThoughtChunk struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    Content       ContentBlock   `json:"content"`
    SessionUpdate string         `json:"sessionUpdate"`  // "agent_thought_chunk"
}

type SessionUpdateToolCall struct {
    Meta          map[string]any   `json:"_meta,omitempty"`
    Content       []ToolCallContent `json:"content,omitempty"`
    Kind          ToolKind          `json:"kind,omitempty"`
    Locations     []ToolCallLocation `json:"locations,omitempty"`
    RawInput      any               `json:"rawInput,omitempty"`
    RawOutput     any               `json:"rawOutput,omitempty"`
    SessionUpdate string            `json:"sessionUpdate"`  // "tool_call"
    Status        ToolCallStatus    `json:"status,omitempty"`
    Title         string            `json:"title"`
    ToolCallId    ToolCallId        `json:"toolCallId"`
}

type SessionToolCallUpdate struct {
    Meta          map[string]any    `json:"_meta,omitempty"`
    Content       []ToolCallContent `json:"content,omitempty"`
    Kind          *ToolKind         `json:"kind,omitempty"`
    Locations     []ToolCallLocation `json:"locations,omitempty"`
    RawInput      any               `json:"rawInput,omitempty"`
    RawOutput     any               `json:"rawOutput,omitempty"`
    SessionUpdate string            `json:"sessionUpdate"`  // "tool_call_update"
    Status        *ToolCallStatus   `json:"status,omitempty"`
    Title         *string           `json:"title,omitempty"`
    ToolCallId    ToolCallId        `json:"toolCallId"`
}

type SessionUpdatePlan struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    Entries       []PlanEntry    `json:"entries"`
    SessionUpdate string         `json:"sessionUpdate"`  // "plan"
}

type SessionAvailableCommandsUpdate struct {
    Meta              map[string]any     `json:"_meta,omitempty"`
    AvailableCommands []AvailableCommand `json:"availableCommands"`
    SessionUpdate     string             `json:"sessionUpdate"`
}

type SessionCurrentModeUpdate struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    CurrentModeId SessionModeId  `json:"currentModeId"`
    SessionUpdate string         `json:"sessionUpdate"`
}

type SessionConfigOptionUpdate struct {
    Meta          map[string]any        `json:"_meta,omitempty"`
    ConfigOptions []SessionConfigOption `json:"configOptions"`
    SessionUpdate string                `json:"sessionUpdate"`
}

type SessionSessionInfoUpdate struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    SessionUpdate string         `json:"sessionUpdate"`
    Title         *string        `json:"title,omitempty"`
    UpdatedAt     *string        `json:"updatedAt,omitempty"`
}

type SessionUsageUpdate struct {
    Meta          map[string]any `json:"_meta,omitempty"`
    Cost          *Cost          `json:"cost,omitempty"`
    SessionUpdate string         `json:"sessionUpdate"`
    Size          int            `json:"size"`  // total context window tokens
    Used          int            `json:"used"`  // tokens currently used
}
```

### Helper constructors for SessionUpdate

```go
func UpdateUserMessageText(text string) SessionUpdate
func UpdateUserMessage(content ContentBlock) SessionUpdate
func UpdateAgentMessageText(text string) SessionUpdate
func UpdateAgentMessage(content ContentBlock) SessionUpdate
func UpdateAgentThoughtText(text string) SessionUpdate
func UpdateAgentThought(content ContentBlock) SessionUpdate
func UpdatePlan(entries ...PlanEntry) SessionUpdate

func StartToolCall(id ToolCallId, title string, opts ...ToolCallStartOpt) SessionUpdate
func StartReadToolCall(id ToolCallId, title string, path string, opts ...ToolCallStartOpt) SessionUpdate
func StartEditToolCall(id ToolCallId, title string, path string, content any, opts ...ToolCallStartOpt) SessionUpdate
func UpdateToolCall(id ToolCallId, opts ...ToolCallUpdateOpt) SessionUpdate
```

---

## Tool Call Types

```go
type ToolCallId string

type ToolKind string
const (
    ToolKindRead       ToolKind = "read"
    ToolKindEdit       ToolKind = "edit"
    ToolKindDelete     ToolKind = "delete"
    ToolKindMove       ToolKind = "move"
    ToolKindSearch     ToolKind = "search"
    ToolKindExecute    ToolKind = "execute"
    ToolKindThink      ToolKind = "think"
    ToolKindFetch      ToolKind = "fetch"
    ToolKindSwitchMode ToolKind = "switch_mode"
    ToolKindOther      ToolKind = "other"
)

type ToolCallStatus string
const (
    ToolCallStatusPending    ToolCallStatus = "pending"
    ToolCallStatusInProgress ToolCallStatus = "in_progress"
    ToolCallStatusCompleted  ToolCallStatus = "completed"
    ToolCallStatusFailed     ToolCallStatus = "failed"
)

type ToolCallLocation struct {
    Path string `json:"path"`
}

// Union type -- exactly one field set
type ToolCallContent struct {
    Content  *ToolCallContentContent  `json:"-"`
    Diff     *ToolCallContentDiff     `json:"-"`
    Terminal *ToolCallContentTerminal `json:"-"`
}

type ToolCallContentContent struct {
    // wraps a ContentBlock
}

type ToolCallContentDiff struct {
    Path    string  `json:"path"`
    NewText string  `json:"newText"`
    OldText *string `json:"oldText,omitempty"`
}

type ToolCallContentTerminal struct {
    TerminalId string `json:"terminalId"`
}

// Helper constructors
func ToolContent(block ContentBlock) ToolCallContent
func ToolDiffContent(path string, newText string, oldText ...string) ToolCallContent
func ToolTerminalRef(terminalID string) ToolCallContent

// Functional options for StartToolCall
type ToolCallStartOpt func(*SessionUpdateToolCall)
func WithStartKind(k ToolKind) ToolCallStartOpt
func WithStartStatus(s ToolCallStatus) ToolCallStartOpt
func WithStartLocations(l []ToolCallLocation) ToolCallStartOpt
func WithStartContent(c []ToolCallContent) ToolCallStartOpt
func WithStartRawInput(v any) ToolCallStartOpt
func WithStartRawOutput(v any) ToolCallStartOpt

// Functional options for UpdateToolCall
type ToolCallUpdateOpt func(*SessionToolCallUpdate)
func WithUpdateTitle(t string) ToolCallUpdateOpt
func WithUpdateKind(k ToolKind) ToolCallUpdateOpt
func WithUpdateStatus(s ToolCallStatus) ToolCallUpdateOpt
func WithUpdateLocations(l []ToolCallLocation) ToolCallUpdateOpt
func WithUpdateContent(c []ToolCallContent) ToolCallUpdateOpt
func WithUpdateRawInput(v any) ToolCallUpdateOpt
func WithUpdateRawOutput(v any) ToolCallUpdateOpt

// ToolCallUpdate (used in RequestPermission)
type ToolCallUpdate struct {
    Meta      map[string]any    `json:"_meta,omitempty"`
    Content   []ToolCallContent `json:"content,omitempty"`
    Kind      *ToolKind         `json:"kind,omitempty"`
    Locations []ToolCallLocation `json:"locations,omitempty"`
    RawInput  any               `json:"rawInput,omitempty"`
    RawOutput any               `json:"rawOutput,omitempty"`
    Status    *ToolCallStatus   `json:"status,omitempty"`
    Title     *string           `json:"title,omitempty"`
    ToolCallId ToolCallId       `json:"toolCallId"`
}
```

---

## Permission Types

```go
type PermissionOptionId string

type PermissionOptionKind string
const (
    PermissionOptionKindAllowOnce    PermissionOptionKind = "allow_once"
    PermissionOptionKindAllowAlways  PermissionOptionKind = "allow_always"
    PermissionOptionKindRejectOnce   PermissionOptionKind = "reject_once"
    PermissionOptionKindRejectAlways PermissionOptionKind = "reject_always"
)

type RequestPermissionRequest struct {
    Meta      map[string]any   `json:"_meta,omitempty"`
    Options   []PermissionOption `json:"options"`
    SessionId SessionId          `json:"sessionId"`
    ToolCall  ToolCallUpdate     `json:"toolCall"`
}

type PermissionOption struct {
    Meta     map[string]any       `json:"_meta,omitempty"`
    Kind     PermissionOptionKind `json:"kind"`
    Name     string               `json:"name"`
    OptionId PermissionOptionId   `json:"optionId"`
}

type RequestPermissionResponse struct {
    Meta    map[string]any          `json:"_meta,omitempty"`
    Outcome RequestPermissionOutcome `json:"outcome"`
}

// Union type
type RequestPermissionOutcome struct {
    Cancelled *RequestPermissionOutcomeCancelled `json:"-"`
    Selected  *RequestPermissionOutcomeSelected  `json:"-"`
}

type RequestPermissionOutcomeCancelled struct {
    Outcome string `json:"outcome"` // "cancelled"
}

type RequestPermissionOutcomeSelected struct {
    Meta     map[string]any     `json:"_meta,omitempty"`
    OptionId PermissionOptionId `json:"optionId"`
    Outcome  string             `json:"outcome"` // "selected"
}

// Constructors
func NewRequestPermissionOutcomeCancelled() RequestPermissionOutcome
func NewRequestPermissionOutcomeSelected() RequestPermissionOutcome
```

---

## Plan Types

```go
type PlanEntry struct {
    Meta     map[string]any    `json:"_meta,omitempty"`
    Content  string            `json:"content"`
    Priority PlanEntryPriority `json:"priority"`
    Status   PlanEntryStatus   `json:"status"`
}

type PlanEntryPriority string
const (
    PlanEntryPriorityHigh   PlanEntryPriority = "high"
    PlanEntryPriorityMedium PlanEntryPriority = "medium"
    PlanEntryPriorityLow    PlanEntryPriority = "low"
)

type PlanEntryStatus string
const (
    PlanEntryStatusPending    PlanEntryStatus = "pending"
    PlanEntryStatusInProgress PlanEntryStatus = "in_progress"
    PlanEntryStatusCompleted  PlanEntryStatus = "completed"
)

type Role string
const (
    RoleAssistant Role = "assistant"
    RoleUser      Role = "user"
)
```

---

## File System Types

```go
type ReadTextFileRequest struct {
    Meta      map[string]any `json:"_meta,omitempty"`
    Limit     *int           `json:"limit,omitempty"`
    Line      *int           `json:"line,omitempty"`
    Path      string         `json:"path"`
    SessionId SessionId      `json:"sessionId"`
}

type ReadTextFileResponse struct {
    Meta    map[string]any `json:"_meta,omitempty"`
    Content string         `json:"content"`
}

type WriteTextFileRequest struct {
    Meta      map[string]any `json:"_meta,omitempty"`
    Content   string         `json:"content"`
    Path      string         `json:"path"`
    SessionId SessionId      `json:"sessionId"`
}

type WriteTextFileResponse struct {
    Meta map[string]any `json:"_meta,omitempty"`
}
```

---

## Terminal Types

```go
type CreateTerminalRequest struct {
    Meta            map[string]any `json:"_meta,omitempty"`
    Args            []string       `json:"args,omitempty"`
    Command         string         `json:"command"`
    Cwd             *string        `json:"cwd,omitempty"`
    Env             []EnvVariable  `json:"env,omitempty"`
    OutputByteLimit *int           `json:"outputByteLimit,omitempty"`
    SessionId       SessionId      `json:"sessionId"`
}

type CreateTerminalResponse struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    TerminalId string         `json:"terminalId"`
}

type TerminalOutputRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    SessionId  SessionId      `json:"sessionId"`
    TerminalId string         `json:"terminalId"`
}

type TerminalOutputResponse struct {
    Meta      map[string]any `json:"_meta,omitempty"`
    ExitCode  *int           `json:"exitCode,omitempty"`
    Output    string         `json:"output"`
    Truncated bool           `json:"truncated"`
}

type KillTerminalCommandRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    SessionId  SessionId      `json:"sessionId"`
    TerminalId string         `json:"terminalId"`
}

type KillTerminalCommandResponse struct {
    Meta map[string]any `json:"_meta,omitempty"`
}

type ReleaseTerminalRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    SessionId  SessionId      `json:"sessionId"`
    TerminalId string         `json:"terminalId"`
}

type ReleaseTerminalResponse struct {
    Meta map[string]any `json:"_meta,omitempty"`
}

type WaitForTerminalExitRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    SessionId  SessionId      `json:"sessionId"`
    TerminalId string         `json:"terminalId"`
}

type WaitForTerminalExitResponse struct {
    Meta       map[string]any     `json:"_meta,omitempty"`
    ExitStatus TerminalExitStatus `json:"exitStatus"`
}
```

---

## Error Types

```go
type RequestError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

func (e *RequestError) Error() string

func NewParseError(data any) *RequestError
func NewInvalidRequest(data any) *RequestError
func NewMethodNotFound(method string) *RequestError
func NewInvalidParams(data any) *RequestError
func NewInternalError(data any) *RequestError
func NewAuthRequired(data any) *RequestError
```

---

## Extension Methods

```go
// Implement this interface on your Client or Agent to handle custom methods
type ExtensionMethodHandler interface {
    HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error)
}

// Extension method names must start with "_"
```

---

## Utility

```go
func Ptr[T any](v T) *T  // generic helper to get pointer to value
```

---

## Usage Patterns

### Complete Client-Side Flow (from `example/client/main.go` and `example/claude-code/main.go`)

```go
// 1. Spawn agent subprocess
cmd := exec.CommandContext(ctx, "npx", "-y", "@zed-industries/claude-code-acp@latest")
cmd.Stderr = os.Stderr
stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
cmd.Start()

// 2. Create client implementation
client := &myClient{}  // implements acp.Client

// 3. Create connection
conn := acp.NewClientSideConnection(client, stdin, stdout)
conn.SetLogger(slog.Default())

// 4. Initialize
initResp, err := conn.Initialize(ctx, acp.InitializeRequest{
    ProtocolVersion: acp.ProtocolVersionNumber,
    ClientCapabilities: acp.ClientCapabilities{
        Fs:       acp.FileSystemCapability{ReadTextFile: true, WriteTextFile: true},
        Terminal: true,
    },
})

// 5. Create new session (with optional MCP servers)
newSess, err := conn.NewSession(ctx, acp.NewSessionRequest{
    Cwd:        "/path/to/working/dir",
    McpServers: []acp.McpServer{},
})
// newSess.SessionId is the session ID for subsequent calls

// 6. Send prompt (BLOCKS until agent completes the turn)
// During execution, Client.SessionUpdate() is called with each streaming update
resp, err := conn.Prompt(ctx, acp.PromptRequest{
    SessionId: newSess.SessionId,
    Prompt:    []acp.ContentBlock{acp.TextBlock("Hello, agent!")},
})
// resp.StopReason tells you why the turn ended

// 7. Cancel (if needed, from another goroutine)
conn.Cancel(ctx, acp.CancelNotification{SessionId: newSess.SessionId})

// 8. Detect disconnect
<-conn.Done()
```

### Handling SessionUpdate in Client Implementation

```go
func (c *myClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
    u := params.Update
    switch {
    case u.AgentMessageChunk != nil:
        // Text streaming from the agent
        if u.AgentMessageChunk.Content.Text != nil {
            fmt.Print(u.AgentMessageChunk.Content.Text.Text)
        }
    case u.AgentThoughtChunk != nil:
        // Agent's internal reasoning
        if u.AgentThoughtChunk.Content.Text != nil {
            fmt.Printf("[thought] %s", u.AgentThoughtChunk.Content.Text.Text)
        }
    case u.ToolCall != nil:
        // New tool call started
        fmt.Printf("Tool: %s (status: %s, kind: %s)\n",
            u.ToolCall.Title, u.ToolCall.Status, u.ToolCall.Kind)
    case u.ToolCallUpdate != nil:
        // Tool call status changed
        fmt.Printf("Tool %s updated: status=%v\n",
            u.ToolCallUpdate.ToolCallId, u.ToolCallUpdate.Status)
    case u.Plan != nil:
        // Plan entries updated
        for _, e := range u.Plan.Entries {
            fmt.Printf("  [%s] %s\n", e.Status, e.Content)
        }
    case u.UserMessageChunk != nil:
        // Echo of user message
    case u.CurrentModeUpdate != nil:
        // Mode changed
    case u.SessionInfoUpdate != nil:
        // Title/metadata changed
    case u.UsageUpdate != nil:
        // Token usage: u.UsageUpdate.Used / u.UsageUpdate.Size
    }
    return nil
}
```

### Handling Permission Requests

```go
func (c *myClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
    // Auto-approve strategy:
    for _, o := range params.Options {
        if o.Kind == acp.PermissionOptionKindAllowOnce ||
           o.Kind == acp.PermissionOptionKindAllowAlways {
            return acp.RequestPermissionResponse{
                Outcome: acp.RequestPermissionOutcome{
                    Selected: &acp.RequestPermissionOutcomeSelected{
                        OptionId: o.OptionId,
                    },
                },
            }, nil
        }
    }
    // Or cancel:
    return acp.RequestPermissionResponse{
        Outcome: acp.RequestPermissionOutcome{
            Cancelled: &acp.RequestPermissionOutcomeCancelled{},
        },
    }, nil
}
```

---

## Key Design Notes

1. **Prompt() is blocking**: It sends the prompt and blocks until the agent completes the entire turn. All streaming updates arrive via `Client.SessionUpdate()` callbacks BEFORE `Prompt()` returns. The SDK guarantees notification ordering.

2. **Notifications are sequential**: The SDK processes notifications in order via an internal queue. All notifications received before a response are guaranteed to be fully processed before `SendRequest` returns.

3. **Cancel is a notification**: `Cancel()` sends a fire-and-forget notification. It does not wait for acknowledgment. If `Prompt()`'s context is cancelled, the SDK automatically sends a Cancel.

4. **Union types use discriminators**: `SessionUpdate` uses `"sessionUpdate"` field, `ContentBlock` uses `"type"` field, `McpServer` uses `"type"` field. Custom `MarshalJSON`/`UnmarshalJSON` handle serialization.

5. **Meta fields**: Every request/response/notification has an optional `_meta` field for extensibility. Implementations must not make assumptions about values at these keys.

6. **Claude Code ACP bridge**: Spawned via `npx -y @zed-industries/claude-code-acp@latest`. This wraps Claude Code CLI in ACP protocol mode.
