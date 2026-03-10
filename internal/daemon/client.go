package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

// Client connects to the daemon over two Unix socket connections:
// one for the subscribe stream (push), one for request/response RPCs.
type Client struct {
	// Subscribe stream (dedicated connection)
	subConn    net.Conn
	subEnc     *json.Encoder
	subScanner *bufio.Scanner

	// RPC connection (serialized with mutex)
	rpcConn    net.Conn
	rpcEnc     *json.Encoder
	rpcScanner *bufio.Scanner
	rpcMu      sync.Mutex
}

// dialWithAutoStart connects to the daemon socket, auto-starting if needed.
func dialWithAutoStart() (net.Conn, error) {
	info := DefaultDaemonInfo()
	conn, err := net.DialTimeout("unix", info.SocketPath, 500*time.Millisecond)
	if err == nil {
		return conn, nil
	}
	if startErr := autoStart(info); startErr != nil {
		return nil, fmt.Errorf("connect failed and auto-start failed: dial=%w start=%w", err, startErr)
	}
	for i := range 5 {
		time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
		conn, err = net.DialTimeout("unix", info.SocketPath, 500*time.Millisecond)
		if err == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("connect failed after auto-start: %w", err)
}

func newScanner(conn net.Conn) *bufio.Scanner {
	s := bufio.NewScanner(conn)
	s.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)
	return s
}

// Connect dials the daemon twice (sub + rpc), auto-starting if needed.
func Connect() (*Client, error) {
	subConn, err := dialWithAutoStart()
	if err != nil {
		return nil, err
	}
	rpcConn, err := dialWithAutoStart()
	if err != nil {
		subConn.Close()
		return nil, fmt.Errorf("second connection failed: %w", err)
	}
	return &Client{
		subConn:    subConn,
		subEnc:     json.NewEncoder(subConn),
		subScanner: newScanner(subConn),
		rpcConn:    rpcConn,
		rpcEnc:     json.NewEncoder(rpcConn),
		rpcScanner: newScanner(rpcConn),
	}, nil
}

func autoStart(_ DaemonInfo) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	return cmd.Start()
}

// ConnectRPCOnly dials the daemon once (RPC only, no subscribe stream).
// Used by cmc eval and other non-TUI commands.
func ConnectRPCOnly() (*Client, error) {
	rpcConn, err := dialWithAutoStart()
	if err != nil {
		return nil, err
	}
	return &Client{
		rpcConn:    rpcConn,
		rpcEnc:     json.NewEncoder(rpcConn),
		rpcScanner: newScanner(rpcConn),
	}, nil
}

// Close shuts down both connections.
func (c *Client) Close() error {
	var e1 error
	if c.subConn != nil {
		e1 = c.subConn.Close()
	}
	e2 := c.rpcConn.Close()
	if e1 != nil {
		return e1
	}
	return e2
}

func readResponse(scanner *bufio.Scanner) (Response, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Response{}, err
		}
		return Response{}, fmt.Errorf("daemon disconnected")
	}
	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("bad response: %w", err)
	}
	return resp, nil
}

func (c *Client) rpc(req Request) (Response, error) {
	c.rpcMu.Lock()
	defer c.rpcMu.Unlock()

	if err := c.rpcEnc.Encode(req); err != nil {
		return Response{}, err
	}
	return readResponse(c.rpcScanner)
}

// rpcInto sends an RPC and unmarshals the result into v.
func (c *Client) rpcInto(req Request, v any) error {
	resp, err := c.rpc(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	if v != nil {
		if err := json.Unmarshal(resp.Data, v); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

// Subscribe sends the subscribe request and returns the initial sessions + usage.
// Call ReadNext() afterwards to get subsequent pushes.
func (c *Client) Subscribe() ([]claude.ClaudeSession, *claude.UsageStats, error) {
	if err := c.subEnc.Encode(Request{Type: ReqSubscribe}); err != nil {
		return nil, nil, err
	}
	resp, err := readResponse(c.subScanner)
	if err != nil {
		return nil, nil, err
	}
	if resp.Error != "" {
		return nil, nil, fmt.Errorf("subscribe: %s", resp.Error)
	}
	var data SessionsData
	json.Unmarshal(resp.Data, &data)
	return data.Sessions, data.Usage, nil
}

// ReadNext blocks until the daemon pushes the next session update.
func (c *Client) ReadNext() ([]claude.ClaudeSession, *claude.UsageStats, error) {
	resp, err := readResponse(c.subScanner)
	if err != nil {
		return nil, nil, err
	}
	if resp.Error != "" {
		return nil, nil, fmt.Errorf("subscribe: %s", resp.Error)
	}
	var data SessionsData
	json.Unmarshal(resp.Data, &data)
	return data.Sessions, data.Usage, nil
}

// Transcript fetches user messages for a session.
func (c *Client) Transcript(sessionID string) ([]string, error) {
	var data TranscriptData
	err := c.rpcInto(Request{Type: ReqTranscript, Data: marshalData(SessionIDData{SessionID: sessionID})}, &data)
	return data.Messages, err
}

// DiffStats fetches file diff statistics for a session.
func (c *Client) DiffStats(sessionID string) (map[string]claude.FileDiffStat, error) {
	var data DiffStatsData
	err := c.rpcInto(Request{Type: ReqDiffStats, Data: marshalData(SessionIDData{SessionID: sessionID})}, &data)
	return data.Stats, err
}

// Summary fetches the cached summary for a session.
func (c *Client) Summary(sessionID string) (*claude.SessionSummary, error) {
	var data SummaryData
	err := c.rpcInto(Request{Type: ReqSummary, Data: marshalData(SessionIDData{SessionID: sessionID})}, &data)
	return data.Summary, err
}

// Synthesize triggers haiku synthesis. Daemon handles /rename side-effect.
func (c *Client) Synthesize(paneID, sessionID string) (*claude.SessionSummary, bool, error) {
	var data SynthesizeResultData
	err := c.rpcInto(Request{Type: ReqSynthesize, Data: marshalData(PaneSessionData{PaneID: paneID, SessionID: sessionID})}, &data)
	return data.Summary, data.FromCache, err
}

// SynthesizeAll triggers synthesis for all sessions except the most recently active.
func (c *Client) SynthesizeAll(skipPaneID string) ([]SynthesizeResultData, error) {
	var data SynthesizeAllResultData
	err := c.rpcInto(Request{Type: ReqSynthesizeAll, Data: marshalData(SkipPaneData{SkipPaneID: skipPaneID})}, &data)
	return data.Results, err
}

// TranscriptEntries fetches parsed transcript entries for a session.
func (c *Client) TranscriptEntries(sessionID string) ([]claude.TranscriptEntry, error) {
	var data TranscriptEntriesData
	err := c.rpcInto(Request{Type: ReqRawTranscript, Data: marshalData(SessionIDData{SessionID: sessionID})}, &data)
	return data.Entries, err
}

// DiffHunks fetches file diff hunks (actual content) for a session.
func (c *Client) DiffHunks(sessionID string) ([]claude.FileDiffHunk, error) {
	var data DiffHunksData
	err := c.rpcInto(Request{Type: ReqDiffHunks, Data: marshalData(SessionIDData{SessionID: sessionID})}, &data)
	return data.Hunks, err
}

// HookEvents fetches debug hook events for a session.
func (c *Client) HookEvents(sessionID string) ([]claude.HookEvent, error) {
	var data HookEventsData
	err := c.rpcInto(Request{Type: ReqHookEvents, Data: marshalData(SessionIDData{SessionID: sessionID})}, &data)
	return data.Events, err
}

// AllHookEffects fetches handled hook effects across all sessions (newest first, max 25).
func (c *Client) AllHookEffects() ([]claude.GlobalHookEffect, error) {
	var data AllHookEffectsData
	err := c.rpcInto(Request{Type: ReqAllHookEffects}, &data)
	return data.Effects, err
}

// PaneGeometry fetches pane layout for the minimap.
func (c *Client) PaneGeometry(sessionName string) ([]tmux.PaneGeometry, error) {
	var data PaneGeometryData
	err := c.rpcInto(Request{Type: ReqPaneGeometry, Data: marshalData(SessionNameData{SessionName: sessionName})}, &data)
	return data.Panes, err
}

// Later bookmarks a session for later (keeps pane alive).
func (c *Client) Later(paneID, sessionID string) error {
	return c.rpcInto(Request{Type: ReqLater, Data: marshalData(LaterData{PaneID: paneID, SessionID: sessionID})}, nil)
}

// LaterKill bookmarks a session and kills the pane.
func (c *Client) LaterKill(paneID string, pid int, sessionID string) error {
	return c.rpcInto(Request{Type: ReqLaterKill, Data: marshalData(LaterKillData{PaneID: paneID, PID: pid, SessionID: sessionID})}, nil)
}

// Unlater removes a Later bookmark.
func (c *Client) Unlater(bookmarkID string) error {
	return c.rpcInto(Request{Type: ReqUnlater, Data: marshalData(UnlaterData{BookmarkID: bookmarkID})}, nil)
}

// OpenLater creates a new tmux window from a dead Later bookmark.
func (c *Client) OpenLater(bookmarkID, cwd, tmuxSession string) error {
	return c.rpcInto(Request{Type: ReqOpenLater, Data: marshalData(OpenLaterData{BookmarkID: bookmarkID, CWD: cwd, TmuxSession: tmuxSession})}, nil)
}

// RenameWindow asks the daemon to generate and apply a window name.
func (c *Client) RenameWindow(sessionName string, windowIndex int) (string, error) {
	var data RenameResultData
	err := c.rpcInto(Request{Type: ReqRenameWindow, Data: marshalData(RenameWindowData{SessionName: sessionName, WindowIndex: windowIndex})}, &data)
	return data.Name, err
}

// CommitOnly sends /commit-commands:commit to the pane (no auto-kill on completion).
func (c *Client) CommitOnly(paneID, sessionID string, pid int) error {
	return c.rpcInto(Request{Type: ReqCommitOnly, Data: marshalData(CommitDoneData{PaneID: paneID, SessionID: sessionID, PID: pid})}, nil)
}

// CommitAndDone sends /commit-commands:commit to the pane and registers it for auto-kill on commit.
func (c *Client) CommitAndDone(paneID, sessionID string, pid int) error {
	return c.rpcInto(Request{Type: ReqCommitDone, Data: marshalData(CommitDoneData{PaneID: paneID, SessionID: sessionID, PID: pid})}, nil)
}

// CancelCommitDone removes the pending commit-and-done registration for a session.
func (c *Client) CancelCommitDone(sessionID string) error {
	return c.rpcInto(Request{Type: ReqCancelCommitDone, Data: marshalData(SessionIDData{SessionID: sessionID})}, nil)
}

// Queue registers a message for delivery when the session becomes Done.
func (c *Client) Queue(paneID, sessionID, message string) error {
	return c.rpcInto(Request{Type: ReqQueue, Data: marshalData(QueueData{PaneID: paneID, SessionID: sessionID, Message: message})}, nil)
}

// CancelQueueItem removes a single queued message by index for a session.
func (c *Client) CancelQueueItem(sessionID string, index int) error {
	return c.rpcInto(Request{Type: ReqCancelQueueItem, Data: marshalData(CancelQueueItemData{SessionID: sessionID, Index: index})}, nil)
}

// Sessions fetches all sessions filtered by orchestrator exclusion and optional status.
func (c *Client) Sessions(statusFilter string) ([]claude.ClaudeSession, error) {
	var data SessionsData
	err := c.rpcInto(Request{Type: ReqSessions, Data: marshalData(SessionsFilterData{Status: statusFilter})}, &data)
	return data.Sessions, err
}

// Send delivers a message to a session's tmux pane.
func (c *Client) Send(sessionID, message string) error {
	return c.rpcInto(Request{Type: ReqSend, Data: marshalData(SendData{SessionID: sessionID, Message: message})}, nil)
}

// Spawn creates a new tmux window, launches claude, and waits for session registration.
func (c *Client) Spawn(cwd, tmuxSession, message string) (SpawnResultData, error) {
	var data SpawnResultData
	err := c.rpcInto(Request{Type: ReqSpawn, Data: marshalData(SpawnData{CWD: cwd, TmuxSession: tmuxSession, Message: message})}, &data)
	return data, err
}

// Kill terminates a session (SIGTERM + kill pane + cleanup).
func (c *Client) Kill(sessionID string) error {
	return c.rpcInto(Request{Type: ReqKill, Data: marshalData(SessionIDData{SessionID: sessionID})}, nil)
}

// PendingPrompt registers a prompt to be delivered to a pane once its claude session is ready.
func (c *Client) PendingPrompt(paneID, prompt string) error {
	return c.rpcInto(Request{Type: ReqPendingPrompt, Data: marshalData(PendingPromptData{PaneID: paneID, Prompt: prompt})}, nil)
}

// RegisterOrchestrator marks a session ID for exclusion from eval sessions().
func (c *Client) RegisterOrchestrator(sessionID string) error {
	return c.rpcInto(Request{Type: ReqRegisterOrchestrator, Data: marshalData(SessionIDData{SessionID: sessionID})}, nil)
}

// UnregisterOrchestrator removes a session ID from the exclusion set.
func (c *Client) UnregisterOrchestrator(sessionID string) error {
	return c.rpcInto(Request{Type: ReqUnregisterOrchestrator, Data: marshalData(SessionIDData{SessionID: sessionID})}, nil)
}


