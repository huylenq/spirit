package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/huylenq/spirit/internal/claude"
)

// hermesSessionFile stores the Hermes ACP session UUID so the copilot conversation
// resumes across daemon restarts. Hermes has no stable --session key (unlike OpenClaw),
// so we persist the UUID returned by session/new and replay it via session/load.
func hermesSessionFile() string {
	return filepath.Join(claude.StatusDir(), "copilot", "hermes_session")
}

func secureHermesSessionFile() {
	path := hermesSessionFile()
	_ = os.Chmod(filepath.Dir(path), 0o700)
	_ = os.Chmod(path, 0o600)
}

func readHermesSessionID() string {
	path := hermesSessionFile()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	secureHermesSessionFile()
	return strings.TrimSpace(string(data))
}

func writeHermesSessionID(id string) {
	path := hermesSessionFile()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.Chmod(dir, 0o700)

	tmp, err := os.CreateTemp(dir, ".hermes_session-*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return
	}
	if _, err := tmp.WriteString(id); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmpPath, path)
}

func clearHermesSessionID() {
	os.Remove(hermesSessionFile()) //nolint:errcheck
}

// hermesModeFile stores the chosen ACP session mode (the coarse autonomy ceiling)
// so it survives /new and daemon restart and is re-applied on session open. Hermes
// persists the mode per-session too, but a fresh session (/new) resets to default;
// persisting here re-establishes the user's ceiling across fresh conversations.
func hermesModeFile() string {
	return filepath.Join(claude.StatusDir(), "copilot", "mode")
}

func readHermesMode() string {
	data, err := os.ReadFile(hermesModeFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeHermesMode(id string) {
	path := hermesModeFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	os.WriteFile(path, []byte(id), 0o600) //nolint:errcheck
}

// errACPSessionNotFound signals that a persisted UUID no longer exists in Hermes
// (session/load returned a null result), so the client should start fresh.
var errACPSessionNotFound = fmt.Errorf("acp: session not found")

// acpCapabilities is what the agent advertised in its initialize response. Lulu
// gates optional behavior (session resume) on these instead of assuming.
type acpCapabilities struct {
	ProtocolVersion int
	AgentName       string
	AgentVersion    string
	LoadSession     bool
	ForkSessions    bool
	ListSessions    bool
	ResumeSessions  bool
}

// acpUsage is the latest context-pressure snapshot from a usage_update.
type acpUsage struct {
	Size int64
	Used int64
}

// copilotCommand is one advertised Hermes slash command.
type copilotCommand struct {
	Name        string
	Description string
}

// acpMCPServer is one stdio MCP server entry injected via the ACP mcp_servers array
// at session open (session/new and session/load). Hermes registers it and exposes its
// tools to Lulu dynamically. The wire shape matches ACP's McpServerStdio: name,
// command, args, env (all required by Hermes's schema — env may be empty). See
// ~/.hermes/hermes-agent/acp_adapter/server.py _register_session_mcp_servers.
type acpMCPServer struct {
	Name    string      `json:"name"`
	Command string      `json:"command"`
	Args    []string    `json:"args"`
	Env     []acpEnvVar `json:"env"`
}

type acpEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SpiritMCPServer builds the mcp_servers entry that points Hermes at `spirit mcp`
// using the given spirit binary path, so Lulu gets Spirit's typed operation tools.
func SpiritMCPServer(spiritBinary string) acpMCPServer {
	return acpMCPServer{
		Name:    "spirit",
		Command: spiritBinary,
		Args:    []string{"mcp"},
		Env:     []acpEnvVar{},
	}
}

// acpTransport is one live subprocess (or in-process fake) connection.
type acpTransport struct {
	stdin  io.WriteCloser
	stdout io.Reader
	stop   func() // terminate and reap; must be idempotent
}

// acpClient manages a long-lived ACP subprocess (hermes acp) for copilot
// communication. It speaks JSON-RPC 2.0 over newline-delimited stdio through a
// demultiplexed wire: one reader goroutine (readLoop) owns stdout and routes
// responses, notifications, and agent-to-client requests independently, so model
// and mode calls and permission prompts are served while a prompt streams.
//
// The client is lazy: the subprocess starts on the first operation.
type acpClient struct {
	startMu sync.Mutex // serializes subprocess startup (one dial at a time)
	writeMu sync.Mutex // serializes stdin writes
	mu      sync.Mutex // protects the mutable fields below

	transport *acpTransport
	stdin     io.WriteCloser
	nextID    atomic.Int64
	sessionID string
	models    CopilotModelState
	modes     CopilotModeState
	caps      acpCapabilities
	usage     *acpUsage
	commands  []copilotCommand
	alive     bool
	readerErr error

	// pending correlates request ids to the goroutine waiting on the response.
	pendingMu sync.Mutex
	pending   map[int64]chan *acpMessage

	// sink is the active prompt's stream consumer. Guarded by sinkMu; sinkID
	// lets a superseding prompt's clearSink avoid clobbering the newer sink.
	// sessionSinks are per-session overrides (reactive forks, W7): updates for
	// a registered session id route there and never touch the main sink.
	sinkMu       sync.Mutex
	sink         func(CopilotStreamData)
	sinkID       uint64
	sessionSinks map[string]func(CopilotStreamData)

	// onPermission, when set by the daemon, decides session/request_permission
	// requests (returns the option id to select, or "" to refuse). When nil the
	// client applies the W0 policy inline.
	onPermission func(params json.RawMessage) string

	// mcpServers is injected into session/new and session/load so Hermes registers
	// Spirit's operation tools (spirit mcp) at session open. Empty → no injection.
	mcpServers []acpMCPServer

	// dial opens a transport; nil means spawn a real `hermes acp` subprocess.
	// Tests inject an in-process fake here.
	dial func() (*acpTransport, error)

	// Session-UUID persistence indirection; nil fields use the on-disk helpers.
	// Tests inject in-memory stores so they never touch the real ~/.spirit file.
	writeSessionID func(string)
	readSessionID  func() string
	clearSessionID func()

	// Session-mode persistence indirection (the coarse autonomy ceiling). nil
	// fields use the on-disk ~/.spirit/copilot/mode helpers; tests inject.
	writeMode func(string)
	readMode  func() string
}

func (c *acpClient) persistMode(id string) {
	if c.writeMode != nil {
		c.writeMode(id)
		return
	}
	writeHermesMode(id)
}

func (c *acpClient) persistedMode() string {
	if c.readMode != nil {
		return c.readMode()
	}
	return readHermesMode()
}

func (c *acpClient) persistSessionID(id string) {
	if c.writeSessionID != nil {
		c.writeSessionID(id)
		return
	}
	writeHermesSessionID(id)
}

func (c *acpClient) persistedSessionID() string {
	if c.readSessionID != nil {
		return c.readSessionID()
	}
	return readHermesSessionID()
}

// mcpServersParam returns the value for the ACP mcpServers parameter: the injected
// spirit MCP server list, or an empty array when none is configured (the prior
// behavior). Hermes registers whatever is passed at session open.
func (c *acpClient) mcpServersParam() any {
	c.mu.Lock()
	servers := c.mcpServers
	c.mu.Unlock()
	if len(servers) == 0 {
		return []any{}
	}
	return servers
}

func (c *acpClient) forgetSessionID() {
	if c.clearSessionID != nil {
		c.clearSessionID()
		return
	}
	clearHermesSessionID()
}

// SessionID returns the active Hermes ACP session UUID, or the persisted UUID
// before the lazy ACP subprocess has been started. Nil-safe (tests build
// daemons without an ACP client).
func (c *acpClient) SessionID() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != "" {
		return c.sessionID
	}
	return c.persistedSessionID()
}

// Commands returns Hermes's advertised slash commands (from
// available_commands_update), for input suggestions on surfaces that want them.
func (c *acpClient) Commands() []copilotCommand {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]copilotCommand(nil), c.commands...)
}

// Usage returns the latest context-pressure snapshot, or nil if none received.
func (c *acpClient) Usage() *acpUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.usage == nil {
		return nil
	}
	u := *c.usage
	return &u
}

type acpSessionResult struct {
	SessionID string             `json:"sessionId"`
	Models    *CopilotModelState `json:"models,omitempty"`
	Modes     *CopilotModeState  `json:"modes,omitempty"`
}

// --- lifecycle ---

// dialHermes spawns the real `hermes acp` subprocess.
func dialHermes() (*acpTransport, error) {
	cmd := exec.Command("hermes", "acp")
	cmd.Stderr = log.Writer() // route ACP bridge errors to daemon log

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("acp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("start hermes acp: %w", err)
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			stdin.Close()
			if cmd.Process != nil && cmd.ProcessState == nil {
				cmd.Process.Kill() //nolint:errcheck
			}
			cmd.Wait() //nolint:errcheck
		})
	}
	return &acpTransport{stdin: stdin, stdout: stdout, stop: stop}, nil
}

// ensureReady starts the ACP subprocess and performs the handshake if not already
// running. Startup is serialized so concurrent callers share one subprocess.
func (c *acpClient) ensureReady() error {
	c.startMu.Lock()
	defer c.startMu.Unlock()

	c.mu.Lock()
	if c.alive && c.sessionID != "" {
		c.mu.Unlock()
		return nil
	}
	c.stopLocked() // clean up any stale process

	dial := c.dial
	if dial == nil {
		dial = dialHermes
	}
	c.mu.Unlock()

	tr, err := dial()
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.transport = tr
	c.stdin = tr.stdin
	c.alive = true
	c.readerErr = nil
	if c.pending == nil {
		c.pending = map[int64]chan *acpMessage{}
	}
	c.mu.Unlock()

	go c.readLoop(tr.stdout)

	if err := c.handshake(); err != nil {
		c.mu.Lock()
		c.stopLocked()
		c.mu.Unlock()
		return fmt.Errorf("acp handshake: %w", err)
	}

	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	log.Printf("acp: connected (session=%s)", sid)
	return nil
}

// callTimeout is call() with a bound so a wedged-but-alive subprocess can't hang
// a control operation forever. The prompt path deliberately uses no timeout.
func (c *acpClient) callTimeout(method string, params any, d time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return c.call(ctx, method, params)
}

// handshake performs initialize, then resumes the persisted session or starts a
// fresh one. The reader goroutine is already running, so responses arrive over
// the demux and replayed session/load transcript notifications are discarded
// (no sink is attached during the handshake).
func (c *acpClient) handshake() error {
	res, err := c.callTimeout("initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]bool{"readTextFile": false, "writeTextFile": false},
		},
		"clientInfo": map[string]any{
			"name":    "spirit-copilot",
			"title":   "Spirit Copilot",
			"version": "1.0.0",
		},
	}, 30*time.Second)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	caps := parseCapabilities(res)
	if caps.ProtocolVersion <= 0 {
		return fmt.Errorf("initialize: agent advertised no protocol version")
	}
	c.mu.Lock()
	c.caps = caps
	c.mu.Unlock()

	// Resume the previous conversation if we have a persisted UUID that the agent
	// still knows; otherwise start fresh. Validation goes through session/list
	// because a not-found session/load is indistinguishable from a successful one
	// by its response body (Hermes returns an empty `{}` result for both), so
	// loading a stale id would silently "resume" a thread that no longer exists.
	if sid := c.persistedSessionID(); sid != "" {
		switch {
		case !caps.LoadSession:
			log.Printf("acp: agent does not advertise loadSession; starting fresh (previous %s not resumed)", sid)
		case !c.sessionExists(sid):
			log.Printf("acp: persisted session %s is not in the agent's session list; starting fresh", sid)
			c.forgetSessionID()
		default:
			if err := c.loadSession(sid); err == nil {
				c.applyPersistedMode()
				return nil
			} else {
				log.Printf("acp: resume %s failed (%v); starting fresh", sid, err)
			}
		}
	}
	if err := c.newSession(); err != nil {
		return err
	}
	c.applyPersistedMode()
	return nil
}

// applyPersistedMode re-applies the user's chosen session mode (the autonomy
// ceiling) after a session opens, so a fresh session honors the persisted ceiling
// instead of Hermes's default. A no-op when nothing is persisted or the persisted
// mode already matches the session's current mode.
func (c *acpClient) applyPersistedMode() {
	want := c.persistedMode()
	if want == "" {
		return
	}
	c.mu.Lock()
	current := c.modes.CurrentModeID
	sessionID := c.sessionID
	c.mu.Unlock()
	if want == current || sessionID == "" {
		return
	}
	if _, err := c.callTimeout("session/set_mode", map[string]any{
		"sessionId": sessionID,
		"modeId":    want,
	}, 30*time.Second); err != nil {
		log.Printf("acp: could not re-apply persisted mode %q: %v", want, err)
		return
	}
	c.mu.Lock()
	c.modes.CurrentModeID = want
	c.mu.Unlock()
	log.Printf("acp: re-applied session mode %q", want)
}

// parseCapabilities extracts the advertised capabilities from an initialize
// response. Presence of the fork/list/resume capability objects signals support.
func parseCapabilities(res json.RawMessage) acpCapabilities {
	var r struct {
		ProtocolVersion int `json:"protocolVersion"`
		AgentInfo       struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"agentInfo"`
		AgentCapabilities struct {
			LoadSession         bool `json:"loadSession"`
			SessionCapabilities struct {
				Fork   *json.RawMessage `json:"fork"`
				List   *json.RawMessage `json:"list"`
				Resume *json.RawMessage `json:"resume"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		log.Printf("acp: could not parse initialize capabilities: %v", err)
		return acpCapabilities{}
	}
	return acpCapabilities{
		ProtocolVersion: r.ProtocolVersion,
		AgentName:       r.AgentInfo.Name,
		AgentVersion:    r.AgentInfo.Version,
		LoadSession:     r.AgentCapabilities.LoadSession,
		ForkSessions:    r.AgentCapabilities.SessionCapabilities.Fork != nil,
		ListSessions:    r.AgentCapabilities.SessionCapabilities.List != nil,
		ResumeSessions:  r.AgentCapabilities.SessionCapabilities.Resume != nil,
	}
}

// sessionExists reports whether sid is among the agent's known sessions. When the
// agent doesn't advertise session/list (or the call fails) it returns true so the
// caller falls through to an optimistic load rather than discarding a live thread.
func (c *acpClient) sessionExists(sid string) bool {
	c.mu.Lock()
	canList := c.caps.ListSessions
	c.mu.Unlock()
	if !canList {
		return true
	}
	res, err := c.callTimeout("session/list", map[string]any{}, 30*time.Second)
	if err != nil {
		log.Printf("acp: session/list failed (%v); attempting load anyway", err)
		return true
	}
	var r struct {
		Sessions []struct {
			SessionID string `json:"sessionId"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		log.Printf("acp: could not parse session/list (%v); attempting load anyway", err)
		return true
	}
	for _, s := range r.Sessions {
		if s.SessionID == sid {
			return true
		}
	}
	return false
}

// loadSession resumes an existing Hermes session by UUID. Hermes replays the prior
// transcript as session/update notifications, which are discarded here (no sink is
// attached) — display history is served from the daemon's persisted
// chat_history.json, the single source of conversation-display truth. A null load
// result means the session no longer exists → errACPSessionNotFound.
func (c *acpClient) loadSession(sid string) error {
	res, err := c.callTimeout("session/load", map[string]any{
		"sessionId":  sid,
		"cwd":        os.Getenv("HOME"),
		"mcpServers": c.mcpServersParam(),
	}, 120*time.Second)
	if err != nil {
		return err
	}
	if len(res) == 0 || string(bytes.TrimSpace(res)) == "null" {
		return errACPSessionNotFound
	}

	// The load response carries models/modes but no sessionId; the id we loaded
	// by remains the handle.
	var result acpSessionResult
	json.Unmarshal(res, &result) //nolint:errcheck
	c.mu.Lock()
	c.sessionID = sid
	if result.Models != nil {
		c.models = *result.Models
	}
	if result.Modes != nil {
		c.modes = *result.Modes
	}
	c.mu.Unlock()
	log.Printf("acp: resumed session=%s", sid)
	return nil
}

// newSession creates a fresh Hermes session and persists its UUID so the next
// daemon start can resume it via session/load.
func (c *acpClient) newSession() error {
	res, err := c.callTimeout("session/new", map[string]any{
		"cwd":        os.Getenv("HOME"),
		"mcpServers": c.mcpServersParam(),
	}, 60*time.Second)
	if err != nil {
		return err
	}
	var result acpSessionResult
	if err := json.Unmarshal(res, &result); err != nil {
		return fmt.Errorf("parse session/new: %w", err)
	}
	if result.SessionID == "" {
		return fmt.Errorf("session/new returned an empty session id")
	}
	c.mu.Lock()
	c.sessionID = result.SessionID
	if result.Models != nil {
		c.models = *result.Models
	}
	if result.Modes != nil {
		c.modes = *result.Modes
	}
	c.mu.Unlock()
	c.persistSessionID(result.SessionID)
	return nil
}

// --- prompt ---

// Prompt sends a message and streams CopilotStreamData events via onUpdate.
// Blocks until the prompt turn completes, the context is cancelled, or the
// subprocess dies. Returns the full accumulated text for history persistence.
func (c *acpClient) Prompt(ctx context.Context, text string, onUpdate func(CopilotStreamData)) (string, error) {
	if err := c.ensureReady(); err != nil {
		return "", err
	}

	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	onUpdate(CopilotStreamData{Type: "session", Content: sessionID})

	// Install a stream sink that accumulates assistant text and forwards every
	// event to the caller. dispatchUpdate routes session/update chunks here.
	var fullText strings.Builder
	sinkID := c.setSink(func(evt CopilotStreamData) {
		if evt.Type == "text_delta" {
			fullText.WriteString(evt.Content)
		}
		onUpdate(evt)
	})
	defer c.clearSink(sinkID)

	// Register the prompt response channel manually (rather than via call) so the
	// cancel watcher can send session/cancel while we keep waiting for Hermes's
	// terminal response.
	promptID := c.nextID.Add(1)
	ch := make(chan *acpMessage, 1)
	c.pendingMu.Lock()
	c.pending[promptID] = ch
	c.pendingMu.Unlock()

	if err := c.writeMessage(acpRequest{
		JSONRPC: "2.0",
		ID:      promptID,
		Method:  "session/prompt",
		Params: map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]string{{"type": "text", "text": text}},
		},
	}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, promptID)
		c.pendingMu.Unlock()
		return "", fmt.Errorf("send prompt: %w", err)
	}

	// Cancel watcher: on ctx cancel, ask Hermes to cancel the turn (it returns a
	// normal response with stop_reason cancelled); force-kill if it goes silent.
	done := make(chan struct{})
	defer close(done)
	go c.watchCancel(ctx, sessionID, done)

	msg := <-ch // fails fast with an error message if the reader dies
	if ctx.Err() != nil {
		return "", fmt.Errorf("cancelled")
	}
	if msg.Error != nil {
		return "", fmt.Errorf("prompt: %s", msg.Error.Message)
	}
	return strings.TrimSpace(fullText.String()), nil
}

// watchCancel sends session/cancel when ctx is cancelled and force-kills the
// subprocess if it does not wind down within the grace period.
func (c *acpClient) watchCancel(ctx context.Context, sessionID string, done <-chan struct{}) {
	select {
	case <-done:
		return
	case <-ctx.Done():
	}

	c.notify("session/cancel", map[string]any{"sessionId": sessionID}) //nolint:errcheck

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		log.Printf("acp: force-killing after cancel timeout")
		c.mu.Lock()
		c.stopLocked()
		c.mu.Unlock()
	}
}

// --- sink management ---

func (c *acpClient) setSink(f func(CopilotStreamData)) uint64 {
	c.sinkMu.Lock()
	defer c.sinkMu.Unlock()
	c.sinkID++
	c.sink = f
	return c.sinkID
}

func (c *acpClient) clearSink(id uint64) {
	c.sinkMu.Lock()
	defer c.sinkMu.Unlock()
	if c.sinkID == id {
		c.sink = nil
	}
}

func (c *acpClient) clearSinkAll() {
	c.sinkMu.Lock()
	c.sink = nil
	c.sessionSinks = nil
	c.sinkMu.Unlock()
}

// emit forwards a stream event to the active sink, if any.
func (c *acpClient) emit(evt CopilotStreamData) {
	c.sinkMu.Lock()
	f := c.sink
	c.sinkMu.Unlock()
	if f != nil {
		f(evt)
	}
}

// --- model control ---

// ModelStatus starts or resumes the ACP session if needed and returns the
// effective model selector state reported by Hermes.
func (c *acpClient) ModelStatus() (CopilotModelState, error) {
	c.mu.Lock()
	if c.alive && c.sessionID != "" {
		state := cloneCopilotModelState(c.models)
		c.mu.Unlock()
		return state, nil
	}
	c.mu.Unlock()

	if err := c.ensureReady(); err != nil {
		return CopilotModelState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneCopilotModelState(c.models), nil
}

// SetModel switches the active ACP session model. It runs concurrently with a
// streaming prompt — the demux routes its response independently.
func (c *acpClient) SetModel(modelID string) (CopilotModelState, error) {
	if err := c.ensureReady(); err != nil {
		return CopilotModelState{}, err
	}

	c.mu.Lock()
	resolved := resolveCopilotModelID(modelID, c.models.AvailableModels)
	sessionID := c.sessionID
	c.mu.Unlock()
	if resolved == "" {
		return CopilotModelState{}, fmt.Errorf("model id is required")
	}

	if _, err := c.callTimeout("session/set_model", map[string]any{
		"sessionId": sessionID,
		"modelId":   resolved,
	}, 30*time.Second); err != nil {
		return CopilotModelState{}, err
	}

	c.mu.Lock()
	c.models.CurrentModelID = resolved
	state := cloneCopilotModelState(c.models)
	c.mu.Unlock()
	return state, nil
}

// ModeStatus returns the current session-mode selector state (autonomy ceiling),
// starting the session if needed.
func (c *acpClient) ModeStatus() (CopilotModeState, error) {
	c.mu.Lock()
	if c.alive && c.sessionID != "" {
		state := cloneCopilotModeState(c.modes)
		c.mu.Unlock()
		return state, nil
	}
	c.mu.Unlock()
	if err := c.ensureReady(); err != nil {
		return CopilotModeState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneCopilotModeState(c.modes), nil
}

// SetMode switches the ACP session mode via session/set_mode and persists it as the
// autonomy ceiling. modeID must be one of the advertised mode ids.
func (c *acpClient) SetMode(modeID string) (CopilotModeState, error) {
	if err := c.ensureReady(); err != nil {
		return CopilotModeState{}, err
	}
	c.mu.Lock()
	resolved := resolveCopilotModeID(modeID, c.modes.AvailableModes)
	sessionID := c.sessionID
	c.mu.Unlock()
	if resolved == "" {
		return CopilotModeState{}, fmt.Errorf("mode id is required")
	}
	if _, err := c.callTimeout("session/set_mode", map[string]any{
		"sessionId": sessionID,
		"modeId":    resolved,
	}, 30*time.Second); err != nil {
		return CopilotModeState{}, err
	}
	c.mu.Lock()
	c.modes.CurrentModeID = resolved
	state := cloneCopilotModeState(c.modes)
	c.mu.Unlock()
	c.persistMode(resolved)
	return state, nil
}

func resolveCopilotModeID(input string, available []CopilotModeInfo) string {
	input = strings.TrimSpace(input)
	for _, m := range available {
		if strings.EqualFold(input, m.ID) || strings.EqualFold(input, m.Name) {
			return m.ID
		}
	}
	return input
}

func cloneCopilotModeState(state CopilotModeState) CopilotModeState {
	state.AvailableModes = append([]CopilotModeInfo(nil), state.AvailableModes...)
	return state
}

func resolveCopilotModelID(input string, available []CopilotModelInfo) string {
	input = strings.TrimSpace(input)
	for _, model := range available {
		if strings.EqualFold(input, model.ModelID) || strings.EqualFold(input, model.Name) {
			return model.ModelID
		}
	}
	return input
}

func cloneCopilotModelState(state CopilotModelState) CopilotModelState {
	state.AvailableModels = append([]CopilotModelInfo(nil), state.AvailableModels...)
	return state
}

// --- shutdown ---

// stopLocked kills the ACP subprocess. Caller must hold c.mu.
func (c *acpClient) stopLocked() {
	if !c.alive && c.transport == nil {
		return
	}
	c.alive = false
	c.sessionID = ""
	c.models = CopilotModelState{}
	c.modes = CopilotModeState{}
	tr := c.transport
	c.transport = nil
	c.stdin = nil
	if tr != nil {
		tr.stop()
	}
}

// Stop kills the ACP subprocess (exported for daemon shutdown).
func (c *acpClient) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

// ResetSession kills the subprocess and forgets the persisted Hermes session UUID
// so the next Prompt starts a fresh conversation (triggered by /new in the TUI).
func (c *acpClient) ResetSession() {
	c.mu.Lock()
	c.stopLocked()
	c.mu.Unlock()
	c.forgetSessionID()
}
