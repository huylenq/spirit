package daemon

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/huylenq/spirit/internal/claude"
)

func (d *Daemon) releaseLock() {
	if d.lockFile != nil {
		syscall.Flock(int(d.lockFile.Fd()), syscall.LOCK_UN)
		d.lockFile.Close()
		os.Remove(d.lockPath)
	}
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

// readPref reads a single preference value from the prefs file.
func (d *Daemon) readPref(key string) string {
	return claude.ReadPref(key)
}
