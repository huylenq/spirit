package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
)

func TestRenderReactivePlist(t *testing.T) {
	p := renderReactivePlist("/opt/spirit/bin/spirit", "/home/u/.spirit/reactive.log")
	for _, want := range []string{
		"<string>com.spirit.reactive</string>",
		"<string>/opt/spirit/bin/spirit</string>",
		"<string>reactive</string>",
		"<string>run</string>",
		"<key>KeepAlive</key>",
		"<true/>",
		"<string>/home/u/.spirit/reactive.log</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q\n%s", want, p)
		}
	}
}

func TestReactiveSupervisionAvailable(t *testing.T) {
	if !reactiveSupervisionAvailable("darwin") {
		t.Error("darwin should be supervisable")
	}
	if reactiveSupervisionAvailable("linux") {
		t.Error("linux must be refused (launchd-only)")
	}
	if reactiveSupervisionAvailable("windows") {
		t.Error("windows must be refused")
	}
}

func TestReactivePrefTransitions(t *testing.T) {
	defer claude.OverrideStatusDirForTest(t.TempDir())()
	if got := claude.ReadPref("reactive"); got != "" {
		t.Fatalf("fresh pref = %q", got)
	}
	for _, v := range []string{"on", "paused", "on", "off"} {
		if err := claude.WritePref("reactive", v); err != nil {
			t.Fatalf("WritePref %q: %v", v, err)
		}
		if got := claude.ReadPref("reactive"); got != v {
			t.Fatalf("pref = %q, want %q", got, v)
		}
	}
	// WritePref preserves other keys.
	claude.WritePref("fullscreen", "true")
	claude.WritePref("reactive", "on")
	if claude.ReadPref("fullscreen") != "true" {
		t.Fatal("WritePref clobbered an unrelated key")
	}
}

func TestNextBackoff(t *testing.T) {
	d := reactiveBackoffMin
	for i := 0; i < 20; i++ {
		d = nextBackoff(d)
		if d > reactiveBackoffMax {
			t.Fatalf("backoff %v exceeded max %v", d, reactiveBackoffMax)
		}
	}
	if d != reactiveBackoffMax {
		t.Fatalf("backoff did not saturate to max: %v", d)
	}
}

// fakeLease simulates a daemon lease connection. It drops the lease (a
// restart) only after the scheduler has signaled it started, so the reconnect
// test is deterministic under -race.
type fakeLease struct {
	failAcquire bool
	scheduled   chan struct{}
}

func (f *fakeLease) ReactiveLease() (daemon.ReactiveStatusData, error) {
	if f.failAcquire {
		return daemon.ReactiveStatusData{}, fmt.Errorf("lease refused")
	}
	return daemon.ReactiveStatusData{Leased: true}, nil
}
func (f *fakeLease) WaitLeaseClosed() error {
	if f.scheduled != nil {
		<-f.scheduled // hold the lease until the scheduler has run
	}
	return fmt.Errorf("daemon restarted")
}
func (f *fakeLease) Close() error { return nil }

// TestReactiveSuperviseReconnects asserts the loop re-leases after the lease
// drops (simulated daemon restart) and stops cleanly once disabled.
func TestReactiveSuperviseReconnects(t *testing.T) {
	var mu sync.Mutex
	connects := 0
	leaseRuns := 0
	// enabled() returns true for the first 3 connect attempts, then false.
	enabled := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connects < 3
	}
	scheduled := make(chan struct{}, 8)
	connect := func() (leaseSession, error) {
		mu.Lock()
		connects++
		mu.Unlock()
		return &fakeLease{scheduled: scheduled}, nil
	}
	onLease := func(stop <-chan struct{}) {
		mu.Lock()
		leaseRuns++
		mu.Unlock()
		scheduled <- struct{}{} // signal the scheduler started (lets the lease drop)
		<-stop
	}

	done := make(chan struct{})
	go func() {
		reactiveSupervise(connect, enabled, onLease, func(time.Duration) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reactiveSupervise did not terminate")
	}

	mu.Lock()
	defer mu.Unlock()
	if connects < 3 {
		t.Fatalf("expected >=3 reconnects, got %d", connects)
	}
	if leaseRuns == 0 {
		t.Fatal("scheduler was never started under a held lease")
	}
}

// TestReactiveSuperviseBacksOffOnAcquireFailure asserts a failed lease acquire
// closes the client and retries (does not spin the scheduler).
func TestReactiveSuperviseBacksOffOnAcquireFailure(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	scheduled := 0
	connect := func() (leaseSession, error) {
		mu.Lock()
		attempts++
		mu.Unlock()
		return &fakeLease{failAcquire: true}, nil
	}
	enabled := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts < 3
	}
	onLease := func(stop <-chan struct{}) {
		mu.Lock()
		scheduled++
		mu.Unlock()
		<-stop
	}
	reactiveSupervise(connect, enabled, onLease, func(time.Duration) {})

	mu.Lock()
	defer mu.Unlock()
	if scheduled != 0 {
		t.Fatalf("scheduler ran despite failed lease acquire (%d)", scheduled)
	}
}
