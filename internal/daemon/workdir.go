package daemon

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

// RepoRootForDir runs git rev-parse --show-toplevel from dir and returns
// the absolute repository root, or an error if dir is not inside a git repo.
func RepoRootForDir(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// WorkdirDaemonInfo returns DaemonInfo with socket/PID paths scoped to the
// given repo root, stored under /tmp to avoid Unix socket path length issues.
func WorkdirDaemonInfo(repoRoot string) DaemonInfo {
	h := sha256.Sum256([]byte(repoRoot))
	hash := fmt.Sprintf("%x", h[:6]) // 12 hex chars, collision-unlikely for local worktrees
	return DaemonInfo{
		SocketPath: fmt.Sprintf("/tmp/cmc-%s.sock", hash),
		PIDPath:    fmt.Sprintf("/tmp/cmc-%s.pid", hash),
	}
}

// DefaultDaemonInfo returns the DaemonInfo for this process.
// If the binary lives inside a git repository (e.g. a dev worktree build),
// the socket is scoped to that repo root so multiple worktrees can each run
// an independent daemon. Otherwise falls back to ~/.cache/cmc/daemon.sock.
// Result is cached — the binary location doesn't change within a process.
func DefaultDaemonInfo() DaemonInfo {
	cachedInfoOnce.Do(func() {
		cachedInfo = resolveDaemonInfo()
	})
	return cachedInfo
}

var (
	cachedInfo     DaemonInfo
	cachedInfoOnce sync.Once
)

func resolveDaemonInfo() DaemonInfo {
	sock := claude.DaemonSocketPath()
	return DaemonInfo{
		SocketPath: sock,
		PIDPath:    strings.TrimSuffix(sock, ".sock") + ".pid",
	}
}
