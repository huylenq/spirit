package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/copilot"
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

// pendingPromptEntry tracks a prompt to deliver to a newly spawned session.
type pendingPromptEntry struct {
	Prompt    string
	PlanMode  bool
	CreatedAt time.Time
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
	queuePanes map[string][]string // sessionID → FIFO message queue

	pendingPromptMu    sync.Mutex
	pendingPromptPanes map[string]pendingPromptEntry // paneID → entry

	synthesizingMu    sync.Mutex
	synthesizingPanes map[string]bool // paneIDs with in-flight synthesis

	autoSynthMu       sync.Mutex
	lastAutoSynthTime map[string]time.Time // sessionID → last auto-synth time

	overlapMu    sync.RWMutex
	overlaps     []claude.FileOverlap
	overlapPanes map[string]bool // paneIDs involved in any file overlap

	digestMu       sync.Mutex
	lastDigestTime time.Time

	orchestratorMu  sync.RWMutex
	orchestratorIDs map[string]bool // session IDs to exclude from eval sessions()

	usageMu       sync.RWMutex
	usageStats    *claude.UsageStats
	usageFetching sync.Mutex // held for the duration of a fetch; TryLock prevents overlap

	copilotJournal   *copilot.Journal
	copilotWorkspace *copilot.Workspace
	copilotMemory    *copilot.Memory
	copilotCancel    context.CancelFunc // cancel in-flight copilot prompt
	copilotMu        sync.Mutex         // protects copilotCancel

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
		subscribers:        make(map[*subscriber]struct{}),
		commitDonePanes:    make(map[string]commitDoneEntry),
		queuePanes:         make(map[string][]string),
		synthesizingPanes:  make(map[string]bool),
		pendingPromptPanes: make(map[string]pendingPromptEntry),
		orchestratorIDs:    make(map[string]bool),
		lastAutoSynthTime:  make(map[string]time.Time),
		overlapPanes:      make(map[string]bool),
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

	// Initialize copilot subsystem
	d.copilotWorkspace = copilot.NewWorkspace()
	if err := d.copilotWorkspace.EnsureInitialized(); err != nil {
		log.Printf("copilot workspace init: %v", err)
	}
	d.copilotJournal = copilot.NewJournal(filepath.Join(d.copilotWorkspace.Dir, "events"))
	d.copilotMemory = copilot.NewMemory(d.copilotWorkspace.Dir)

	// Start polling goroutine
	pollStop := make(chan struct{})
	go d.pollLoop(pollStop)

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

// nudge triggers an immediate poll. Non-blocking; coalesces multiple nudges.
func (d *Daemon) nudge() {
	select {
	case d.nudgeCh <- struct{}{}:
	default: // already pending
	}
}

// notifySubscribers pushes the latest sidebar to all subscribers.
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
