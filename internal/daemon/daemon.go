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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/laxicon"
	"github.com/huylenq/spirit/internal/ledger"
)

type subscriber struct {
	clientID string                 // stable id from SubscribeData; scopes copilot delivery
	ch       chan []agent.Session
	copilot  chan CopilotStreamData // buffered; streaming events from copilot subprocess
	done     chan struct{}
}

// commitDoneEntry tracks a pending commit operation (commit-only or commit-and-done).
type commitDoneEntry struct {
	PaneID     string
	PID        int
	SawWorking bool      // true once the session has transitioned to agent-turn
	KillOnDone bool      // true for commit+done, false for commit-only
	CreatedAt  time.Time // when the entry was registered; used to expire stuck entries
	Persistent bool      // if true, survive working→idle cycles where no commit was detected
	//                  (used when the commit message is queued behind earlier work like /simplify;
	//                   the watcher waits for the *commit* cycle, not the prior cycle)
}

// pendingPromptEntry tracks a prompt to deliver to a newly spawned session.
type pendingPromptEntry struct {
	Prompt    string
	PlanMode  bool
	CreatedAt time.Time
}

// Daemon is the long-lived background process that polls sessions and serves clients.
type Daemon struct {
	providers *agent.Registry

	mu       sync.RWMutex
	sessions []agent.Session
	version  uint64

	subMu       sync.Mutex
	subscribers map[*subscriber]struct{}

	nudgeCh chan struct{} // hooks signal this to trigger immediate poll

	commitDoneMu    sync.Mutex
	commitDonePanes map[string]commitDoneEntry // sessionID → entry

	queueMu    sync.Mutex
	queuePanes map[string][]agent.QueueItem // sessionID → FIFO message queue (durable item ids, W8)

	// turnAttrib links a delivered queue item to the NEXT completed turn of its
	// session (queued message → delivery → the turn it caused, W8). Written by
	// the queue resolver on delivery, consumed once by signalTurnCompleted.
	turnAttribMu sync.Mutex
	turnAttrib   map[string]turnAttribution // sessionID → last delivered item

	pendingPromptMu    sync.Mutex
	pendingPromptPanes map[string]pendingPromptEntry // paneID → entry

	synthesizingMu    sync.Mutex
	synthesizingPanes map[string]bool // paneIDs with in-flight synthesis

	lastSynthMu       sync.Mutex
	lastSynthTime     map[string]time.Time // sessionID → last synth time (manual or auto)
	autoSynthDisabled bool                 // test override: suppress auto-synthesis goroutines

	overlapMu    sync.RWMutex
	overlaps     []claude.FileOverlap
	overlapPanes map[string]bool // paneIDs involved in any file overlap

	pulseMu       sync.Mutex
	lastPulseTime time.Time

	orchestratorMu  sync.RWMutex
	orchestratorIDs map[string]bool // session IDs to exclude from eval sessions()

	usageMu       sync.RWMutex
	usageStats    *claude.UsageStats
	usageFetching sync.Mutex // held for the duration of a fetch; TryLock prevents overlap

	// perception is the durable signal/attention ledger (W6). Ingest points
	// live in daemon_ingest.go; nil disables perception (never wedges the
	// daemon). ledgerBaselined and hadOverlaps are poll-goroutine-only state.
	perception      *ledger.Ledger
	ledgerBaselined bool
	hadOverlaps     bool

	// W7 reactive-attention state (daemon_reactive.go): the single-flight
	// recommend slot, the immediate-notification throttle, and the triage
	// digest batch.
	reactiveRunning     atomic.Bool
	reactiveMu          sync.Mutex // guards the three fields below
	lastImmediateNotify time.Time
	digestLines         []string
	digestOldest        time.Time

	copilotCancel      context.CancelFunc  // non-nil while a copilot prompt is in-flight
	copilotCancelEpoch uint64              // turn epoch that owns copilotCancel (guarded by copilotMu)
	copilotMu          sync.Mutex          // protects copilotCancel + copilotCancelEpoch
	copilotPreamble    atomic.Bool         // inject live sessions into copilot prompts
	acpClient          *acpClient          // long-lived ACP subprocess for copilot
	copilotHistory     []CopilotHistoryMsg // in-memory only (TUI display within daemon session)
	copilotHistoryMu   sync.RWMutex
	copilotStateMu     sync.RWMutex
	copilotActive      *copilotActiveState
	copilotEpoch       uint64 // monotonic turn counter (guarded by copilotStateMu)

	copilotFleetMu         sync.Mutex // guards the delta digest below
	copilotLastFleetDigest string     // material fleet state last injected into Lulu's persistent session

	laxiconReader laxicon.Reader // read-only, mtime-cached plan/spec parser (zero value ready)

	copilotPermMu     sync.Mutex                    // guards the pending-permission registry
	copilotPerms      map[string]*pendingPermission // permissionID → in-flight approval round-trip
	copilotPermTimeout time.Duration                // override for the auto-deny wait (tests); 0 → default

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
		providers:          agent.NewDefaultRegistry(),
		subscribers:        make(map[*subscriber]struct{}),
		commitDonePanes:    make(map[string]commitDoneEntry),
		queuePanes:         make(map[string][]agent.QueueItem),
		turnAttrib:         make(map[string]turnAttribution),
		synthesizingPanes:  make(map[string]bool),
		pendingPromptPanes: make(map[string]pendingPromptEntry),
		orchestratorIDs:    make(map[string]bool),
		lastSynthTime:      make(map[string]time.Time),
		overlapPanes:       make(map[string]bool),
		copilotPerms:       make(map[string]*pendingPermission),
		nudgeCh:            make(chan struct{}, 1),
		socketPath:         info.SocketPath,
		pidPath:            info.PIDPath,
		lockPath:           info.SocketPath + ".lock",
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

	// Persistent daemon: survive a controlling-terminal close (e.g. the tmux
	// pane/popup that launched it via `make`). Without this, SIGHUP's default
	// disposition would kill the daemon and take the hermes ACP child with it.
	// Intended shutdown stays on SIGTERM (`daemon --stop`) and the idle timeout.
	signal.Ignore(syscall.SIGHUP)

	// Recover queued messages from disk
	d.recoverQueue()

	// Initialize the perception ledger (durable signals + attention items).
	// A failed open logs and leaves perception nil — the daemon runs blind
	// rather than not at all.
	if led, err := ledger.Open(ledger.Dir(), ledger.DefaultWindow); err != nil {
		log.Printf("ledger: disabled: %v", err)
	} else {
		d.perception = led
	}

	// Initialize copilot subsystem
	d.copilotPreamble.Store(true)
	secureHermesSessionFile() // migrate legacy state permissions
	// Inject `spirit mcp` as an ACP mcp_server so Hermes registers Spirit's typed
	// operation tools at Lulu's session open. Command = this daemon's own binary.
	d.acpClient = &acpClient{onPermission: d.decideCopilotPermission} // lazy-started on first copilot prompt
	if exe, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		d.acpClient.mcpServers = []acpMCPServer{SpiritMCPServer(exe)}
	} else {
		log.Printf("acp: could not resolve spirit binary for mcp injection: %v", err)
	}
	d.copilotHistory = loadCopilotHistory() // restore display history from disk

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
	d.acpClient.Stop()
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
func (d *Daemon) notifySubscribers(sessions []agent.Session) {
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

func (d *Daemon) addSubscriber(clientID string) *subscriber {
	sub := &subscriber{
		clientID: clientID,
		ch:       make(chan []agent.Session, 1),
		copilot:  make(chan CopilotStreamData, 256),
		done:     make(chan struct{}),
	}
	d.subMu.Lock()
	d.subscribers[sub] = struct{}{}
	d.subMu.Unlock()
	return sub
}

// pushCopilotStream delivers a copilot event to the subscriber(s) that own the
// originating turn. Delivery is scoped to event.ClientID (Decision 6): in-flight
// stream chunks belong to the requester, so a second attached TUI does not see
// another client's live tokens — it picks up the completed exchange from shared
// history on its next subscribe/snapshot. An empty ClientID (older client, or a
// daemon-internal event) broadcasts to every subscriber for back-compat.
// Non-blocking per subscriber: a full buffer drops the event (the full text lands
// in history after completion).
func (d *Daemon) pushCopilotStream(event CopilotStreamData) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	for sub := range d.subscribers {
		if event.ClientID != "" && sub.clientID != "" && sub.clientID != event.ClientID {
			continue // scoped to a different client
		}
		select {
		case sub.copilot <- event:
		default:
			// buffer full — skip (subscriber too slow)
		}
	}
}

func (d *Daemon) removeSubscriber(sub *subscriber) {
	d.subMu.Lock()
	delete(d.subscribers, sub)
	d.subMu.Unlock()
	// A disconnecting client can no longer answer any permission prompt it owns —
	// deny those so the tool call isn't held open until Hermes's own timeout.
	d.denyPermissionsForClient(sub.clientID)
}

func (d *Daemon) currentSessions() []agent.Session {
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
