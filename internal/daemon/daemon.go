package daemon

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
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

// commitDoneEntry tracks a pending commit-and-done operation.
type commitDoneEntry struct {
	PaneID    string
	PID       int
	SawWorking bool // true once the session has transitioned to Working
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
	commitDonePanes map[string]commitDoneEntry // paneID → entry

	summarizingMu    sync.Mutex
	summarizingPanes map[string]bool // paneIDs with in-flight summarization

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
		summarizingPanes: make(map[string]bool),
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

	// Start polling goroutine
	pollStop := make(chan struct{})
	go d.pollLoop(pollStop)

	// Start usage polling goroutine
	go d.usageLoop(pollStop)

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
	d.nudge()
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

// patchSession applies a targeted status update from a hook, bypassing full discovery.
// Returns true if a matching session was found and updated.
func (d *Daemon) patchSession(paneID string, status claude.Status, lastUserMessage string) bool {
	now := time.Now()

	d.mu.Lock()
	found := false
	for i := range d.sessions {
		if d.sessions[i].PaneID == paneID {
			d.sessions[i].Status = status
			d.sessions[i].LastChanged = now
			if lastUserMessage != "" {
				d.sessions[i].LastUserMessage = lastUserMessage
			}
			if status == claude.StatusWorking {
				d.sessions[i].PermissionMode = claude.ReadPermissionMode(paneID)
			}
			found = true
			break
		}
	}
	if !found {
		d.mu.Unlock()
		return false
	}
	d.version++
	sessions := d.sessions
	d.mu.Unlock()

	// Notify subscribers
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
	return true
}

func (d *Daemon) poll() {
	sessions, err := claude.DiscoverSessions()
	if err != nil {
		return
	}

	// Resolve pending commit-and-done operations
	d.resolveCommitDone(sessions)

	// Annotate sessions with daemon-side pending states
	d.commitDoneMu.Lock()
	d.summarizingMu.Lock()
	for i := range sessions {
		paneID := sessions[i].PaneID
		if _, pending := d.commitDonePanes[paneID]; pending {
			sessions[i].CommitDonePending = true
		}
		if d.summarizingPanes[paneID] {
			sessions[i].SummarizePending = true
		}
	}
	d.summarizingMu.Unlock()
	d.commitDoneMu.Unlock()

	d.mu.Lock()
	if sessionsEqual(d.sessions, sessions) {
		d.mu.Unlock()
		return
	}
	d.sessions = sessions
	d.version++
	d.mu.Unlock()

	// Notify all subscribers
	d.subMu.Lock()
	for sub := range d.subscribers {
		// Non-blocking send — drop stale, send latest
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

// resolveCommitDone checks pending commit-done operations against current sessions.
// If a session is back to Done: if committed → kill pane, else → drop the pending entry.
func (d *Daemon) resolveCommitDone(sessions []claude.ClaudeSession) {
	d.commitDoneMu.Lock()
	defer d.commitDoneMu.Unlock()

	if len(d.commitDonePanes) == 0 {
		return
	}

	sessionByPane := make(map[string]*claude.ClaudeSession, len(sessions))
	for i := range sessions {
		sessionByPane[sessions[i].PaneID] = &sessions[i]
	}

	for paneID, entry := range d.commitDonePanes {
		s, exists := sessionByPane[paneID]
		if !exists {
			// Session disappeared — clean up
			log.Printf("commit-done: pane %s disappeared, removing", paneID)
			delete(d.commitDonePanes, paneID)
			continue
		}
		if s.Status == claude.StatusWorking {
			// Mark that we've seen the session start working
			if !entry.SawWorking {
				entry.SawWorking = true
				d.commitDonePanes[paneID] = entry
				log.Printf("commit-done: pane %s now working", paneID)
			}
			continue
		}
		if s.Status != claude.StatusDone {
			continue
		}
		// Session is Done — but only resolve if it went through Working first
		if !entry.SawWorking {
			continue // command hasn't been picked up yet, keep waiting
		}
		if s.LastActionCommit {
			log.Printf("commit-done: pane %s committed, killing", paneID)
			if entry.PID > 0 {
				syscall.Kill(entry.PID, syscall.SIGTERM) //nolint:errcheck
			}
			tmux.KillPane(paneID)   //nolint:errcheck
			claude.RemoveStatus(paneID)
		} else {
			log.Printf("commit-done: pane %s done but no commit detected, aborting", paneID)
		}
		delete(d.commitDonePanes, paneID)
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
			a[i].DeferUntil != b[i].DeferUntil ||
			a[i].Headline != b[i].Headline ||
			a[i].LastUserMessage != b[i].LastUserMessage ||
			a[i].PermissionMode != b[i].PermissionMode ||
			a[i].LastActionCommit != b[i].LastActionCommit ||
			a[i].CommitDonePending != b[i].CommitDonePending ||
			a[i].SummarizePending != b[i].SummarizePending {
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
