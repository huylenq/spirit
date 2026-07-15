package daemon

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeHermes is an in-process ACP agent double speaking JSON-RPC 2.0 over io.Pipe
// pairs. It answers the handshake with configurable capabilities, streams
// session/update notifications mid-prompt, can issue a session/request_permission
// during a streaming prompt, rotate the session id via session_info_update, and
// die abruptly. Pipes are preferred over spawning a real subprocess so the
// state-machine tests are fast and deterministic.
type fakeHermes struct {
	t *testing.T

	inR  io.ReadCloser  // client -> fake (fake reads client requests here)
	outW io.WriteCloser // fake -> client (fake writes responses/notifications)

	writeMu sync.Mutex

	// config
	agentCaps      map[string]any // agentCapabilities value in the initialize response
	sessionID      string         // id returned by session/new (and echoed on updates)
	loadNull       bool           // session/load returns null (session not found)
	listedSessions []string       // ids returned by session/list; nil → [sessionID]

	// onPrompt, if set, fully handles a session/prompt request (it MUST eventually
	// reply). If nil, the fake replies end_turn immediately.
	onPrompt func(f *fakeHermes, id int64, text string)

	// modes, when set, is returned as the SessionModeState in session/new and
	// session/load responses so the client captures mode state.
	modes map[string]any

	// captured wire state, for assertions
	mu           sync.Mutex
	lastSetModel map[string]string
	lastSetMode  map[string]string
	cancelled    chan struct{}

	// pending agent-to-client requests the fake issued (permission), keyed by id.
	pmu     sync.Mutex
	pending map[int64]chan json.RawMessage
	nextID  atomic.Int64

	stopOnce sync.Once
	closers  []io.Closer
}

// newFakeClient wires an acpClient to a fresh fakeHermes over in-process pipes and
// returns both. Session persistence is redirected to an in-memory store so the
// test never touches the real ~/.spirit file. Call f.start() to run the fake.
func newFakeClient(t *testing.T, f *fakeHermes) *acpClient {
	t.Helper()
	clientToFakeR, clientToFakeW := io.Pipe()
	fakeToClientR, fakeToClientW := io.Pipe()

	f.t = t
	f.inR = clientToFakeR
	f.outW = fakeToClientW
	f.cancelled = make(chan struct{}, 1)
	f.pending = map[int64]chan json.RawMessage{}
	f.closers = []io.Closer{clientToFakeR, clientToFakeW, fakeToClientR, fakeToClientW}
	if f.sessionID == "" {
		f.sessionID = "fake-session-1"
	}
	if f.agentCaps == nil {
		f.agentCaps = map[string]any{
			"loadSession":         true,
			"sessionCapabilities": map[string]any{"fork": map[string]any{}, "list": map[string]any{}, "resume": map[string]any{}},
		}
	}

	var persisted struct {
		mu sync.Mutex
		id string
	}
	c := &acpClient{
		dial: func() (*acpTransport, error) {
			return &acpTransport{
				stdin:  clientToFakeW,
				stdout: fakeToClientR,
				stop:   f.stop,
			}, nil
		},
		writeSessionID: func(id string) { persisted.mu.Lock(); persisted.id = id; persisted.mu.Unlock() },
		readSessionID:  func() string { persisted.mu.Lock(); defer persisted.mu.Unlock(); return persisted.id },
		clearSessionID: func() { persisted.mu.Lock(); persisted.id = ""; persisted.mu.Unlock() },
	}
	t.Cleanup(func() { c.Stop(); f.stop() })
	return c
}

func (f *fakeHermes) start() {
	go f.run()
}

func (f *fakeHermes) stop() {
	f.stopOnce.Do(func() {
		for _, cl := range f.closers {
			cl.Close()
		}
	})
}

// die simulates an abrupt subprocess death mid-conversation.
func (f *fakeHermes) die() { f.stop() }

func (f *fakeHermes) run() {
	scanner := bufio.NewScanner(f.inR)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg acpMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch {
		case msg.Method != "" && msg.ID != nil:
			// Handle each request concurrently so a long-running prompt hook does
			// not stall the reader from routing a mid-prompt model/permission call.
			go f.handleRequest(*msg.ID, msg.Method, msg.Params)
		case msg.Method != "" && msg.ID == nil:
			if msg.Method == "session/cancel" {
				select {
				case f.cancelled <- struct{}{}:
				default:
				}
			}
		case msg.Method == "" && msg.ID != nil:
			// Response to an agent-to-client request the fake issued.
			f.pmu.Lock()
			ch := f.pending[*msg.ID]
			delete(f.pending, *msg.ID)
			f.pmu.Unlock()
			if ch != nil {
				ch <- msg.Result
			}
		}
	}
}

func (f *fakeHermes) handleRequest(id int64, method string, params json.RawMessage) {
	switch method {
	case "initialize":
		f.reply(id, map[string]any{
			"protocolVersion":   1,
			"agentInfo":         map[string]any{"name": "fake-hermes", "version": "0.0.1"},
			"agentCapabilities": f.agentCaps,
		})
	case "session/new":
		res := map[string]any{"sessionId": f.sessionID}
		if f.modes != nil {
			res["modes"] = f.modes
		}
		f.reply(id, res)
	case "session/list":
		ids := f.listedSessions
		if ids == nil {
			ids = []string{f.sessionID}
		}
		sessions := make([]any, 0, len(ids))
		for _, sid := range ids {
			sessions = append(sessions, map[string]any{"sessionId": sid})
		}
		f.reply(id, map[string]any{"sessions": sessions})
	case "session/load":
		if f.loadNull {
			f.replyRaw(id, json.RawMessage("null"))
			return
		}
		res := map[string]any{}
		if f.modes != nil {
			res["modes"] = f.modes
		}
		f.reply(id, res)
	case "session/set_model":
		var p struct {
			SessionID string `json:"sessionId"`
			ModelID   string `json:"modelId"`
		}
		json.Unmarshal(params, &p) //nolint:errcheck
		f.mu.Lock()
		f.lastSetModel = map[string]string{"sessionId": p.SessionID, "modelId": p.ModelID}
		f.mu.Unlock()
		f.reply(id, map[string]any{})
	case "session/set_mode":
		var p struct {
			SessionID string `json:"sessionId"`
			ModeID    string `json:"modeId"`
		}
		json.Unmarshal(params, &p) //nolint:errcheck
		f.mu.Lock()
		f.lastSetMode = map[string]string{"sessionId": p.SessionID, "modeId": p.ModeID}
		f.mu.Unlock()
		f.reply(id, map[string]any{})
	case "session/prompt":
		var p struct {
			Prompt []struct {
				Text string `json:"text"`
			} `json:"prompt"`
		}
		json.Unmarshal(params, &p) //nolint:errcheck
		text := ""
		if len(p.Prompt) > 0 {
			text = p.Prompt[0].Text
		}
		if f.onPrompt != nil {
			f.onPrompt(f, id, text)
			return
		}
		f.reply(id, map[string]any{"stopReason": "end_turn"})
	default:
		f.replyErr(id, -32601, "not supported: "+method)
	}
}

// --- fake helpers used by onPrompt hooks and tests ---

func (f *fakeHermes) reply(id int64, result any) {
	f.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fakeHermes) replyRaw(id int64, result json.RawMessage) {
	f.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fakeHermes) replyErr(id int64, code int, message string) {
	f.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

// update sends a session/update notification for the given sessionId.
func (f *fakeHermes) update(sessionID string, update any) {
	f.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params":  map[string]any{"sessionId": sessionID, "update": update},
	})
}

func (f *fakeHermes) textDelta(sessionID, text string) {
	f.update(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": text},
	})
}

// requestPermission issues an agent-to-client session/request_permission and
// returns the client's outcome. Runs from an onPrompt hook (i.e. mid-prompt).
func (f *fakeHermes) requestPermission(sessionID string, toolCall, options any) map[string]any {
	id := f.nextID.Add(1) + 1_000_000 // keep well clear of client-issued ids
	ch := make(chan json.RawMessage, 1)
	f.pmu.Lock()
	f.pending[id] = ch
	f.pmu.Unlock()

	f.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/request_permission",
		"params":  map[string]any{"sessionId": sessionID, "toolCall": toolCall, "options": options},
	})

	raw := <-ch
	var out map[string]any
	json.Unmarshal(raw, &out) //nolint:errcheck
	return out
}

func (f *fakeHermes) writeJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	data = append(data, '\n')
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	f.outW.Write(data) //nolint:errcheck
}
