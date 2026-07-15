package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
)

// Durable reactivity (W9). `spirit reactive` is the separately-supervised
// sidecar + control surface for explicit, revocable durable reactivity:
//
//	run      the launchd-supervised loop: hold the daemon's durable lease
//	enable   pref reactive=on + install & load the launchd agent (Darwin only)
//	disable  pref reactive=off + unload & remove the launchd agent
//	pause    pref reactive=paused + push pause over a control RPC
//	resume   pref reactive=on + push resume over a control RPC
//	status   print the read-only reactive status report
//
// The autonomy switch is human-only: there is deliberately no MCP enable/disable.

const (
	reactiveLaunchdLabel = "com.spirit.reactive"
	reactiveBackoffMin   = time.Second
	reactiveBackoffMax   = 30 * time.Second
)

func runReactive() {
	if len(os.Args) < 3 {
		reactiveUsage()
		os.Exit(1)
	}
	switch os.Args[2] {
	case "run":
		reactiveRun()
	case "enable":
		reactiveEnable(reactiveHasFlag("--with-cron"))
	case "disable":
		reactiveDisable()
	case "pause":
		reactiveControl("pause", "paused")
	case "resume":
		reactiveControl("resume", "on")
	case "status":
		reactiveStatusCmd()
	case "-h", "--help", "help":
		reactiveUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown reactive subcommand: %s\n", os.Args[2])
		reactiveUsage()
		os.Exit(1)
	}
}

func reactiveHasFlag(flag string) bool {
	for _, a := range os.Args[3:] {
		if a == flag {
			return true
		}
	}
	return false
}

func reactiveUsage() {
	fmt.Print(`spirit reactive — durable, explicitly enabled reactivity (W9)

Usage:
  spirit reactive enable [--with-cron]  Turn on durable reactivity (installs a launchd agent; macOS only)
  spirit reactive disable               Turn off durable reactivity (removes the launchd agent)
  spirit reactive pause                 Pause processing (lease kept; nothing dispatched)
  spirit reactive resume                Resume processing
  spirit reactive status                Print the reactive status report
  spirit reactive run                   The supervised loop (invoked by launchd; holds the lease)

Durable reactivity lets watches fire with no TUI open. It is separately
supervised by launchd, holds a revocable daemon lease, and is a human-only
switch (no MCP enable/disable). Disable returns the system to normal 10-minute
idle behavior.
`)
}

// --- launchd supervision -----------------------------------------------------

// reactiveSupervisionAvailable reports whether durable reactivity can be
// supervised on this OS. launchd (macOS) is the only supported supervisor; the
// fleet is macOS and cross-platform supervision is a documented non-goal.
func reactiveSupervisionAvailable(goos string) bool {
	return goos == "darwin"
}

func reactivePlistPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", reactiveLaunchdLabel+".plist")
}

// renderReactivePlist builds the LaunchAgent plist for `spirit reactive run`.
// KeepAlive respawns the worker on crash; RunAtLoad starts it immediately.
func renderReactivePlist(binPath, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>reactive</string>
		<string>run</string>
	</array>
	<key>KeepAlive</key>
	<true/>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, reactiveLaunchdLabel, binPath, logPath, logPath)
}

func reactiveEnable(withCron bool) {
	if !reactiveSupervisionAvailable(runtime.GOOS) {
		fmt.Fprintf(os.Stderr, "durable reactivity requires launchd (macOS); this host is %s.\n", runtime.GOOS)
		fmt.Fprintln(os.Stderr, "cross-platform supervision is a documented non-goal — refusing rather than half-supporting it.")
		os.Exit(1)
	}
	if err := claude.WritePref("reactive", "on"); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting pref: %v\n", err)
		os.Exit(1)
	}

	bin := resolveSelfBinary()
	logPath := filepath.Join(claude.StatusDir(), "reactive.log")
	plistPath := reactivePlistPath()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating LaunchAgents dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(plistPath, []byte(renderReactivePlist(bin, logPath)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing plist: %v\n", err)
		os.Exit(1)
	}
	// Idempotent load: unload a prior instance first (ignore errors), then load.
	launchctl("unload", plistPath)
	if out, err := launchctlOut("load", "-w", plistPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading launchd agent: %v\n%s\n", err, out)
		os.Exit(1)
	}
	if withCron {
		reactiveRegisterCron() // item 5, best-effort
	}
	fmt.Printf("durable reactivity enabled (launchd agent %s loaded).\n", reactiveLaunchdLabel)
}

func reactiveDisable() {
	if err := claude.WritePref("reactive", "off"); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting pref: %v\n", err)
		os.Exit(1)
	}
	plistPath := reactivePlistPath()
	launchctl("unload", plistPath) // ignore error (may not be loaded)
	_ = os.Remove(plistPath)       // ignore error (may not exist)
	reactiveDeregisterCron()       // item 5, best-effort
	fmt.Println("durable reactivity disabled (launchd agent removed; daemon reverts to idle-exit).")
}

func launchctl(args ...string) {
	_, _ = launchctlOut(args...)
}

func launchctlOut(args ...string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", nil // no-op off Darwin (disable cleanup still runs harmlessly)
	}
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// resolveSelfBinary returns the absolute, symlink-resolved path to this binary.
func resolveSelfBinary() string {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving executable: %v\n", err)
		os.Exit(1)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return exe
}

// --- control + status --------------------------------------------------------

func reactiveControl(action, prefValue string) {
	if err := claude.WritePref("reactive", prefValue); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting pref: %v\n", err)
		os.Exit(1)
	}
	// Push the change to a running daemon so it takes effect on the next tick
	// without waiting for a pref re-read. Best-effort: if no daemon/worker is up,
	// the pref alone carries the intent (adopted on the next lease acquire).
	client, err := daemon.ConnectRPCOnly()
	if err == nil {
		defer client.Close()
		if _, err := client.ReactiveControl(action); err != nil {
			fmt.Fprintf(os.Stderr, "note: could not push %s to daemon: %v (pref set)\n", action, err)
		}
	}
	fmt.Printf("durable reactivity: %sd.\n", action)
}

func reactiveStatusCmd() {
	client, err := daemon.ConnectRPCOnly()
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	st, err := client.ReactiveStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(st, "", "  ")
	fmt.Println(string(out))
}

// --- the supervised loop -----------------------------------------------------

// leaseSession is the subset of daemon.Client the supervision loop needs (a test
// seam so the reconnect/backoff logic is exercised without a real socket).
type leaseSession interface {
	ReactiveLease() (daemon.ReactiveStatusData, error)
	WaitLeaseClosed() error
	Close() error
}

func reactiveRun() {
	reactiveSupervise(
		func() (leaseSession, error) { return daemon.ConnectRPCOnly() },
		func() bool {
			// disable unloads the agent, but guard against a stale relaunch: exit
			// cleanly if the operator has turned durable reactivity off.
			p := claude.ReadPref("reactive")
			return p == "on" || p == "paused"
		},
		reactiveScheduler,
		time.Sleep,
	)
}

// reactiveSupervise is the core keep-alive/re-lease loop. It acquires the daemon
// lease, runs the Spirit-owned scheduler while leased, and reconnects with
// bounded backoff when the lease drops (daemon restart via `make`, crash, etc.).
func reactiveSupervise(
	connect func() (leaseSession, error),
	enabled func() bool,
	onLease func(stop <-chan struct{}),
	sleep func(time.Duration),
) {
	backoff := reactiveBackoffMin
	for enabled() {
		client, err := connect()
		if err != nil {
			sleep(backoff)
			backoff = nextBackoff(backoff)
			continue
		}
		if _, err := client.ReactiveLease(); err != nil {
			client.Close()
			sleep(backoff)
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = reactiveBackoffMin // acquired — reset

		stop := make(chan struct{})
		if onLease != nil {
			go onLease(stop)
		}
		client.WaitLeaseClosed() // blocks until the daemon drops the lease
		close(stop)
		client.Close()

		if !enabled() {
			return
		}
		sleep(backoff) // brief pause before re-leasing the (restarting) daemon
	}
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > reactiveBackoffMax {
		d = reactiveBackoffMax
	}
	return d
}
