package daemon

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

type subscriber struct {
	ch   chan []claude.ClaudeSession
	done chan struct{}
}

// commitDoneEntry tracks a pending commit operation (commit-only or commit-and-done).
type commitDoneEntry struct {
	PaneID     string
	PID        int
	SawWorking bool      // true once the session has transitioned to agent-turn
	KillOnDone bool      // true for C (commit+done), false for c (commit only)
	CreatedAt  time.Time // when the entry was registered; used to expire stuck entries
}

// Daemon is the long-lived background process that polls sessions and serves clients.
type Daemon struct {
	mu       sync.RWMutex
	sessions []claude.ClaudeSession
	version  uint64

	subMu       sync.Mutex
	subscribers map[*subscriber]struct{}

	nudgeCh chan struct{} // hooks signal this to trigger immediate poll

	commitDoneMu    sync.Mutex
	commitDonePanes map[string]commitDoneEntry // sessionID → entry

	queueMu    sync.Mutex
	queuePanes map[string]string // sessionID → message

	synthesizingMu    sync.Mutex
	synthesizingPanes map[string]bool // paneIDs with in-flight synthesis

	orchestratorMu  sync.RWMutex
	orchestratorIDs map[string]bool // session IDs to exclude from eval sessions()

	usageMu    sync.RWMutex
	usageStats *claude.UsageStats

	listener   net.Listener
	lockFile   *os.File
	socketPath string
	pidPath    string
	lockPath   string

	lastClientDisconnect time.Time
	clientCount          int
}

// Run starts the daemon: acquires lock, cleans up stale socket, writes PID, listens, polls.
func Run(info DaemonInfo) error {
	d := &Daemon{
		subscribers:      make(map[*subscriber]struct{}),
		commitDonePanes:  make(map[string]commitDoneEntry),
		queuePanes:       make(map[string]string),
		synthesizingPanes: make(map[string]bool),
		orchestratorIDs:   make(map[string]bool),
		nudgeCh:         make(chan struct{}, 1),
		socketPath:  info.SocketPath,
		pidPath:     info.PIDPath,
		lockPath:    info.SocketPath + ".lock",
	}

	os.MkdirAll(claude.StatusDir(), 0o755)

	// Acquire exclusive flock — guarantees single instance
	lockFile, err := os.OpenFile(d.lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		return fmt.Errorf("daemon already running (flock held on %s)", d.lockPath)
	}
	d.lockFile = lockFile

	// Clean up stale socket from a previous crash
	if _, err := os.Stat(d.socketPath); err == nil {
		os.Remove(d.socketPath)
	}

	// Write PID file
	if err := os.WriteFile(d.pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		d.releaseLock()
		return fmt.Errorf("writing PID file: %w", err)
	}

	// Listen on Unix socket
	ln, err := net.Listen("unix", d.socketPath)
	if err != nil {
		os.Remove(d.pidPath)
		d.releaseLock()
		return fmt.Errorf("listen %s: %w", d.socketPath, err)
	}
	d.listener = ln

	// Signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Recover queued messages from disk
	d.recoverQueue()

	// Start polling goroutine
	pollStop := make(chan struct{})
	go d.pollLoop(pollStop)

	// Usage polling disabled — Claude Code v2.1.72 changed /usage dialog format,
	// causing FetchUsage to time out and bombard the API with rate-limited requests.
	// go d.usageLoop(pollStop)

	// Start idle timeout checker
	go d.idleWatcher(sigCh)

	// Accept connections (runs until listener is closed)
	go d.acceptLoop()

	log.Printf("daemon started pid=%d socket=%s", os.Getpid(), d.socketPath)

	// Block until signal
	sig := <-sigCh
	log.Printf("daemon shutting down on %v", sig)

	close(pollStop)
	d.listener.Close()
	os.Remove(d.socketPath)
	os.Remove(d.pidPath)
	d.releaseLock()

	// Notify all subscribers
	d.subMu.Lock()
	for sub := range d.subscribers {
		close(sub.done)
	}
	d.subMu.Unlock()

	return nil
}

func (d *Daemon) releaseLock() {
	if d.lockFile != nil {
		syscall.Flock(int(d.lockFile.Fd()), syscall.LOCK_UN)
		d.lockFile.Close()
		os.Remove(d.lockPath)
	}
}

func (d *Daemon) pollLoop(stop chan struct{}) {
	// Do one immediate poll before accepting clients
	d.poll()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			d.poll()
		case <-d.nudgeCh:
			d.poll()
		}
	}
}

func (d *Daemon) usageLoop(stop chan struct{}) {
	// Fetch immediately on startup
	d.fetchUsage()

	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			d.fetchUsage()
		}
	}
}

func (d *Daemon) fetchUsage() {
	stats, err := claude.FetchUsage()
	if err != nil {
		log.Printf("usage fetch: %v", err)
		return
	}
	d.usageMu.Lock()
	d.usageStats = stats
	d.usageMu.Unlock()

	// Bump version and notify subscribers so they receive the new usage data,
	// even if sessions haven't changed.
	d.mu.Lock()
	d.version++
	sessions := d.sessions
	d.mu.Unlock()
	d.notifySubscribers(sessions)
}

func (d *Daemon) currentUsage() *claude.UsageStats {
	d.usageMu.RLock()
	defer d.usageMu.RUnlock()
	return d.usageStats
}

// nudge triggers an immediate poll. Non-blocking; coalesces multiple nudges.
func (d *Daemon) nudge() {
	select {
	case d.nudgeCh <- struct{}{}:
	default: // already pending
	}
}

// notifySubscribers pushes the latest session list to all subscribers.
// Non-blocking per subscriber: drops stale update, sends latest.
func (d *Daemon) notifySubscribers(sessions []claude.ClaudeSession) {
	d.subMu.Lock()
	for sub := range d.subscribers {
		select {
		case sub.ch <- sessions:
		default:
			select {
			case <-sub.ch:
			default:
			}
			sub.ch <- sessions
		}
	}
	d.subMu.Unlock()
}

type patchResult int

const (
	patchNotFound patchResult = iota
	patchApplied
	patchDeduped
)

// patchSession applies a targeted status update from a hook, bypassing full discovery.
// Matches by SessionID (primary) with PaneID fallback.
// Returns patchNotFound if the session isn't tracked, patchApplied if state changed,
// or patchDeduped if the nudge was redundant (no version bump, no subscriber notify).
func (d *Daemon) patchSession(nudge NudgeData) patchResult {
	d.mu.Lock()

	// Find session: match by SessionID first, then PaneID fallback
	idx := -1
	for i := range d.sessions {
		if nudge.SessionID != "" && d.sessions[i].SessionID == nudge.SessionID {
			idx = i
			break
		}
		if d.sessions[i].PaneID == nudge.PaneID {
			idx = i
			// Don't break — keep looking for a SessionID match
		}
	}

	// SessionEnd: remove session from memory
	if nudge.Remove {
		if idx < 0 {
			d.mu.Unlock()
			return patchNotFound
		}
		d.sessions = append(d.sessions[:idx], d.sessions[idx+1:]...)
		d.version++
		sessions := d.sessions
		d.mu.Unlock()
		d.notifySubscribers(sessions)
		return patchApplied
	}

	if idx < 0 {
		d.mu.Unlock()
		return patchNotFound
	}

	s := &d.sessions[idx]
	changed := false

	// Session moved panes (e.g. --resume in a new pane)
	if nudge.PaneID != "" && s.PaneID != nudge.PaneID {
		s.PaneID = nudge.PaneID
		changed = true
	}

	status := claude.ParseStatus(nudge.Status)

	if nudge.Status != "" && s.Status != status {
		s.Status = status
		changed = true
	}
	if nudge.LastUserMessage != "" && s.LastUserMessage != nudge.LastUserMessage {
		s.LastUserMessage = nudge.LastUserMessage
		changed = true
	}
	if status == claude.StatusAgentTurn {
		if nudge.PermissionMode != "" && s.PermissionMode != nudge.PermissionMode {
			s.PermissionMode = nudge.PermissionMode
			changed = true
		}
		if s.StopReason != "" {
			s.StopReason = ""
			changed = true
		}
		if s.IsWaiting {
			s.IsWaiting = false
			changed = true
		}
	}
	if nudge.StopReason != "" && s.StopReason != nudge.StopReason {
		s.StopReason = nudge.StopReason
		changed = true
	}
	if nudge.IsWaiting != nil && s.IsWaiting != *nudge.IsWaiting {
		s.IsWaiting = *nudge.IsWaiting
		changed = true
	}
	if nudge.IsGitCommit != nil && *nudge.IsGitCommit && !s.LastActionCommit {
		s.LastActionCommit = true
		changed = true
	}
	if nudge.IsFileEdit != nil && *nudge.IsFileEdit && s.LastActionCommit {
		s.LastActionCommit = false
		changed = true
	}
	if nudge.Compacted {
		s.CompactCount++
		changed = true
	}

	if !changed {
		d.mu.Unlock()
		return patchDeduped
	}

	s.LastChanged = time.Now()
	d.version++
	sessions := d.sessions
	d.mu.Unlock()
	d.notifySubscribers(sessions)
	return patchApplied
}

func (d *Daemon) poll() {
	sessions, err := claude.DiscoverSessions()
	if err != nil {
		return
	}

	// Resolve pending commit-and-done operations
	d.resolveCommitDone(sessions)

	// Resolve pending queued messages
	d.resolveQueue(sessions)

	// Annotate sessions with daemon-side pending states
	d.commitDoneMu.Lock()
	d.queueMu.Lock()
	d.synthesizingMu.Lock()
	for i := range sessions {
		sid := sessions[i].SessionID
		if sid != "" {
			if _, pending := d.commitDonePanes[sid]; pending {
				sessions[i].CommitDonePending = true
			}
			if msg, pending := d.queuePanes[sid]; pending {
				sessions[i].QueuePending = msg
			}
		}
		if d.synthesizingPanes[sessions[i].PaneID] {
			sessions[i].SynthesizePending = true
		}
	}
	d.synthesizingMu.Unlock()
	d.queueMu.Unlock()
	d.commitDoneMu.Unlock()

	claude.AssignAvatars(sessions)

	d.mu.Lock()
	if sessionsEqual(d.sessions, sessions) {
		d.mu.Unlock()
		return
	}
	d.sessions = sessions
	d.version++
	d.mu.Unlock()
	d.notifySubscribers(sessions)
}

// resolveCommitDone checks pending commit-done operations against current sessions.
// If a session is back to Done: if committed → kill pane, else → drop the pending entry.
func (d *Daemon) resolveCommitDone(sessions []claude.ClaudeSession) {
	d.commitDoneMu.Lock()
	defer d.commitDoneMu.Unlock()

	if len(d.commitDonePanes) == 0 {
		return
	}

	sessionByID := make(map[string]*claude.ClaudeSession, len(sessions))
	for i := range sessions {
		if sessions[i].SessionID != "" {
			sessionByID[sessions[i].SessionID] = &sessions[i]
		}
	}

	for sessionID, entry := range d.commitDonePanes {
		s, exists := sessionByID[sessionID]
		if !exists {
			// Session disappeared — clean up
			log.Printf("commit-done: session %s disappeared, removing", sessionID)
			delete(d.commitDonePanes, sessionID)
			continue
		}
		if s.Status == claude.StatusAgentTurn {
			// Mark that we've seen the session enter agent-turn
			if !entry.SawWorking {
				entry.SawWorking = true
				d.commitDonePanes[sessionID] = entry
				log.Printf("commit-done: session %s now agent-turn", sessionID)
			}
			continue
		}
		if s.Status != claude.StatusUserTurn {
			continue
		}
		// Session is user-turn — but only resolve if it went through agent-turn first
		if !entry.SawWorking {
			// Expire if the session never reached agent-turn (e.g. user interrupted the prompt)
			if time.Since(entry.CreatedAt) > 30*time.Second {
				log.Printf("commit-done: session %s timed out waiting for agent-turn, removing", sessionID)
				delete(d.commitDonePanes, sessionID)
			}
			continue
		}
		if s.LastActionCommit && entry.KillOnDone {
			log.Printf("commit-done: session %s committed, killing pane %s", sessionID, s.PaneID)
			if entry.PID > 0 {
				syscall.Kill(entry.PID, syscall.SIGTERM) //nolint:errcheck
			}
			tmux.KillPane(s.PaneID) //nolint:errcheck
			claude.RemoveSessionFiles(sessionID)
			claude.RemovePaneMapping(s.PaneID)
		} else if s.LastActionCommit {
			log.Printf("commit: session %s committed", sessionID)
		} else {
			log.Printf("commit: session %s done but no commit detected", sessionID)
		}
		delete(d.commitDonePanes, sessionID)
	}
}

// recoverQueue scans *.queue files on startup to rebuild the in-memory map.
func (d *Daemon) recoverQueue() {
	dir := claude.StatusDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	d.queueMu.Lock()
	defer d.queueMu.Unlock()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".queue") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".queue")
		msg := claude.ReadQueueMessage(sessionID)
		if msg != "" {
			d.queuePanes[sessionID] = msg
			log.Printf("queue: recovered session %s", sessionID)
		}
	}
}

// resolveQueue delivers queued messages to sessions that have become Done.
func (d *Daemon) resolveQueue(sessions []claude.ClaudeSession) {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()

	if len(d.queuePanes) == 0 {
		return
	}

	sessionByID := make(map[string]*claude.ClaudeSession, len(sessions))
	for i := range sessions {
		if sessions[i].SessionID != "" {
			sessionByID[sessions[i].SessionID] = &sessions[i]
		}
	}

	for sessionID, msg := range d.queuePanes {
		s, exists := sessionByID[sessionID]
		if !exists {
			log.Printf("queue: session %s disappeared, removing", sessionID)
			delete(d.queuePanes, sessionID)
			claude.RemoveQueueMessage(sessionID)
			continue
		}
		if s.Status != claude.StatusUserTurn {
			continue
		}
		// Session is Done — deliver the message to its current pane
		if err := tmux.SendKeysLiteral(s.PaneID, msg); err != nil {
			log.Printf("queue: send to pane %s (session %s) failed: %v (will retry)", s.PaneID, sessionID, err)
			continue
		}
		log.Printf("queue: delivered to pane %s (session %s)", s.PaneID, sessionID)
		delete(d.queuePanes, sessionID)
		claude.RemoveQueueMessage(sessionID)
	}
}

func (d *Daemon) addSubscriber() *subscriber {
	sub := &subscriber{
		ch:   make(chan []claude.ClaudeSession, 1),
		done: make(chan struct{}),
	}
	d.subMu.Lock()
	d.subscribers[sub] = struct{}{}
	d.subMu.Unlock()
	return sub
}

func (d *Daemon) removeSubscriber(sub *subscriber) {
	d.subMu.Lock()
	delete(d.subscribers, sub)
	d.subMu.Unlock()
}

func (d *Daemon) currentSessions() []claude.ClaudeSession {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sessions
}

func (d *Daemon) currentVersion() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.version
}

func (d *Daemon) clientConnected() {
	d.mu.Lock()
	d.clientCount++
	d.mu.Unlock()
}

func (d *Daemon) clientDisconnected() {
	d.mu.Lock()
	d.clientCount--
	if d.clientCount <= 0 {
		d.clientCount = 0
		d.lastClientDisconnect = time.Now()
	}
	d.mu.Unlock()
}

func (d *Daemon) idleWatcher(sigCh chan<- os.Signal) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		d.mu.RLock()
		count := d.clientCount
		lastDisconnect := d.lastClientDisconnect
		d.mu.RUnlock()

		if count == 0 && !lastDisconnect.IsZero() && time.Since(lastDisconnect) > IdleTimeout {
			log.Printf("idle timeout (%v with no clients), shutting down", IdleTimeout)
			sigCh <- syscall.SIGTERM
			return
		}
	}
}

// sessionsEqual checks if two session slices are equivalent (same pane IDs, statuses, timestamps).
func sessionsEqual(a, b []claude.ClaudeSession) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].PaneID != b[i].PaneID ||
			a[i].Status != b[i].Status ||
			a[i].SessionID != b[i].SessionID ||
			a[i].LastChanged != b[i].LastChanged ||
			a[i].LaterBookmarkID != b[i].LaterBookmarkID ||
			a[i].IsPhantom != b[i].IsPhantom ||
			a[i].Headline != b[i].Headline ||
			a[i].LastUserMessage != b[i].LastUserMessage ||
			a[i].PermissionMode != b[i].PermissionMode ||
			a[i].LastActionCommit != b[i].LastActionCommit ||
			a[i].StopReason != b[i].StopReason ||
			a[i].IsWaiting != b[i].IsWaiting ||
			a[i].CompactCount != b[i].CompactCount ||
			a[i].CommitDonePending != b[i].CommitDonePending ||
			a[i].SynthesizePending != b[i].SynthesizePending ||
			a[i].QueuePending != b[i].QueuePending {
			return false
		}
	}
	return true
}

// CheckAlive tests whether the daemon is running by pinging it.
func CheckAlive(info DaemonInfo) bool {
	conn, err := net.DialTimeout("unix", info.SocketPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Stop sends SIGTERM to the daemon.
func Stop(info DaemonInfo) error {
	data, err := os.ReadFile(info.PIDPath)
	if err != nil {
		return fmt.Errorf("reading PID file: %w", err)
	}
	pid, err := strconv.Atoi(string(data[:len(data)-1])) // trim newline
	if err != nil {
		return fmt.Errorf("parsing PID: %w", err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}
	return proc.Signal(syscall.SIGTERM)
}
