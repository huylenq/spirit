package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
)

// The demultiplexed ACP wire: one reader goroutine owns stdout and routes each
// inbound message to the right consumer — responses to per-request-id channels,
// agent-to-client requests to a handler that runs concurrently with a streaming
// prompt, and session/update notifications to the active stream sink. Writes are
// serialized by writeMu. When the reader exits (EOF / subprocess death) every
// pending call is failed eagerly so nothing hangs on a dead subprocess.

// --- JSON-RPC types ---

type acpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type acpNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type acpResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      int64     `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *acpError `json:"error,omitempty"`
}

// acpMessage is the generic inbound frame. A response carries an ID and no
// Method; a notification carries a Method and no ID; an agent-to-client request
// carries both.
type acpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpError       `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type acpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- reader goroutine ---

// readLoop owns the stdout stream for the lifetime of one subprocess. It exits
// on EOF or a read error and then fails every in-flight call.
func (c *acpClient) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg acpMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			log.Printf("acp: parse error: %v", err)
			continue
		}
		switch {
		case msg.ID != nil && msg.Method != "":
			// Agent-to-client request — serve concurrently so a permission
			// prompt can be answered while a session/prompt is still streaming.
			go c.serveAgentRequest(msg)
		case msg.ID != nil:
			c.deliverResponse(*msg.ID, &msg)
		case msg.Method == "session/update":
			c.dispatchUpdate(msg.Params)
		case msg.Method != "":
			// Wire-protocol tolerance: Hermes serves ACP with unstable methods on;
			// log and continue rather than break on an unrecognized notification.
			log.Printf("acp: ignoring notification %s", msg.Method)
		default:
			log.Printf("acp: unroutable message")
		}
	}

	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.readerExited(err)
}

// deliverResponse hands a response to the waiting call, or logs an orphan.
func (c *acpClient) deliverResponse(id int64, msg *acpMessage) {
	c.pendingMu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.pendingMu.Unlock()
	if ch != nil {
		ch <- msg
		return
	}
	log.Printf("acp: response for unknown id %d", id)
}

// readerExited marks the client dead and fails every in-flight call so callers
// return immediately instead of blocking on a subprocess that will never reply.
func (c *acpClient) readerExited(err error) {
	c.mu.Lock()
	c.alive = false
	c.readerErr = err
	c.sessionID = ""
	tr := c.transport
	c.transport = nil
	c.stdin = nil
	c.mu.Unlock()

	if tr != nil {
		tr.stop() // reap the subprocess; no-op if an external Stop already did
	}
	c.drainPending(err)
	c.clearSinkAll()
}

// drainPending fails all waiting calls with the reader's exit error.
func (c *acpClient) drainPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = map[int64]chan *acpMessage{}
	c.pendingMu.Unlock()
	for _, ch := range pending {
		ch <- &acpMessage{Error: &acpError{Message: fmt.Sprintf("acp: connection closed: %v", err)}}
	}
}

// --- request/response plumbing ---

// call sends a JSON-RPC request and waits for its response. It returns as soon
// as the response arrives, the context is cancelled, or the reader dies.
func (c *acpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan *acpMessage, 1)

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = map[int64]chan *acpMessage{}
	}
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.writeMessage(acpRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("send %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
		}
		return msg.Result, nil
	}
}

// notify writes a JSON-RPC notification (no id, no response expected).
func (c *acpClient) notify(method string, params any) error {
	return c.writeMessage(acpNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// writeMessage serializes a frame to stdin. Writes are mutually exclusive so
// concurrent calls never interleave bytes on the wire.
func (c *acpClient) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	w := c.stdin
	c.mu.Unlock()
	if w == nil {
		return fmt.Errorf("acp: not connected")
	}
	_, err = w.Write(data)
	return err
}
