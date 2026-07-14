package tmux

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type PaneInfo struct {
	PaneID      string
	PanePID     int
	CurrentPath string
	SessionName string
	WindowIndex int
	PaneIndex   int
	PaneCreated time.Time
}

func ListAllPanes() ([]PaneInfo, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{pane_id}:#{pane_pid}:#{pane_current_path}:#{session_name}:#{window_index}:#{pane_index}:#{pane_created}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}

	var panes []PaneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 7)
		if len(parts) != 7 {
			continue
		}
		pid, _ := strconv.Atoi(parts[1])
		winIdx, _ := strconv.Atoi(parts[4])
		paneIdx, _ := strconv.Atoi(parts[5])
		created, _ := strconv.ParseInt(parts[6], 10, 64)
		paneCreated := time.Unix(created, 0)
		if created == 0 {
			// pane_created unavailable (e.g. tmux 3.6+): use pane ID
			// sequence number (%N) as a monotonic creation-order proxy.
			if seq, err := strconv.Atoi(strings.TrimPrefix(parts[0], "%")); err == nil {
				paneCreated = time.Unix(int64(seq), 0)
			}
		}
		panes = append(panes, PaneInfo{
			PaneID:      parts[0],
			PanePID:     pid,
			CurrentPath: parts[2],
			SessionName: parts[3],
			WindowIndex: winIdx,
			PaneIndex:   paneIdx,
			PaneCreated: paneCreated,
		})
	}
	return panes, nil
}

func CapturePaneContent(paneID string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-pJe", "-S", "-", "-t", paneID).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane %s: %w", paneID, err)
	}
	return string(out), nil
}

type PaneGeometry struct {
	PaneID                    string
	SessionName               string
	WindowIndex               int
	WindowName                string
	PaneTitle                 string
	PaneIndex                 int
	Left, Top                 int
	Width, Height             int
	WindowWidth, WindowHeight int
}

func ListPaneGeometry(sessionName string) ([]PaneGeometry, error) {
	format := strings.Join([]string{
		"#{pane_id}",
		"#{window_index}",
		"#{window_name}",
		"#{pane_title}",
		"#{pane_index}",
		"#{pane_left}",
		"#{pane_top}",
		"#{pane_width}",
		"#{pane_height}",
		"#{window_width}",
		"#{window_height}",
	}, "\x1f")
	out, err := exec.Command("tmux", "list-panes", "-s", "-t", sessionName, "-F", format).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes -s -t %s: %w", sessionName, err)
	}

	var panes []PaneGeometry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) != 11 {
			continue
		}
		winIdx, _ := strconv.Atoi(parts[1])
		paneIdx, _ := strconv.Atoi(parts[4])
		left, _ := strconv.Atoi(parts[5])
		top, _ := strconv.Atoi(parts[6])
		w, _ := strconv.Atoi(parts[7])
		h, _ := strconv.Atoi(parts[8])
		ww, _ := strconv.Atoi(parts[9])
		wh, _ := strconv.Atoi(parts[10])
		panes = append(panes, PaneGeometry{
			PaneID:       parts[0],
			SessionName:  sessionName,
			WindowIndex:  winIdx,
			WindowName:   parts[2],
			PaneTitle:    parts[3],
			PaneIndex:    paneIdx,
			Left:         left,
			Top:          top,
			Width:        w,
			Height:       h,
			WindowWidth:  ww,
			WindowHeight: wh,
		})
	}
	return panes, nil
}

// RenameWindow renames a tmux window and disables automatic-rename so the name persists.
func RenameWindow(sessionName string, windowIndex int, name string) error {
	target := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	if err := exec.Command("tmux", "set-option", "-w", "-t", target, "automatic-rename", "off").Run(); err != nil {
		return fmt.Errorf("set automatic-rename off for %s: %w", target, err)
	}
	if err := exec.Command("tmux", "rename-window", "-t", target, name).Run(); err != nil {
		return fmt.Errorf("rename-window %s to %q: %w", target, name, err)
	}
	return nil
}

// NewWindow creates a new tmux window in the given session, starting in cwd.
// Returns the new pane's ID.
func NewWindow(sessionName, cwd string) (string, error) {
	out, err := exec.Command("tmux", "new-window", "-t", sessionName,
		"-c", cwd, "-P", "-F", "#{pane_id}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SplitWindow splits the window containing originPane and creates a new pane
// next to it (horizontal split, side-by-side), starting in cwd. Returns the
// new pane's ID.
func SplitWindow(originPane, cwd string) (string, error) {
	out, err := exec.Command("tmux", "split-window", "-h", "-t", originPane,
		"-c", cwd, "-P", "-F", "#{pane_id}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux split-window -t %s: %w", originPane, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// KillPane closes a tmux pane by ID.
func KillPane(paneID string) error {
	return exec.Command("tmux", "kill-pane", "-t", paneID).Run()
}

// SendKeys sends text to a tmux pane via send-keys.
func SendKeys(paneID string, keys ...string) error {
	args := append([]string{"send-keys", "-t", paneID}, keys...)
	return exec.Command("tmux", args...).Run()
}

// SendLiteral writes text without submitting it. Callers decide which key, if
// any, completes the input.
func SendLiteral(paneID, text string) error {
	if err := exec.Command("tmux", "send-keys", "-t", paneID, "-l", text).Run(); err != nil {
		return fmt.Errorf("send-keys -l: %w", err)
	}
	return nil
}

// SendNamedKeys sends tmux key names such as Enter, C-m, or Escape.
func SendNamedKeys(paneID string, keys ...string) error { return SendKeys(paneID, keys...) }

// PasteText loads text through a tmux buffer and pastes it into the pane. This
// preserves multiline and Unicode input and lets terminal applications observe
// bracketed-paste semantics when they have enabled it.
func PasteText(ctx context.Context, paneID, text string) error {
	bufferName := fmt.Sprintf("spirit-%d", time.Now().UnixNano())
	load := exec.CommandContext(ctx, "tmux", "load-buffer", "-b", bufferName, "-")
	stdin, err := load.StdinPipe()
	if err != nil {
		return fmt.Errorf("tmux load-buffer stdin: %w", err)
	}
	if err := load.Start(); err != nil {
		return fmt.Errorf("tmux load-buffer: %w", err)
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		stdin.Close()
		return fmt.Errorf("write tmux buffer: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("close tmux buffer: %w", err)
	}
	if err := load.Wait(); err != nil {
		return fmt.Errorf("tmux load-buffer: %w", err)
	}
	if err := exec.CommandContext(ctx, "tmux", "paste-buffer", "-d", "-b", bufferName, "-t", paneID).Run(); err != nil {
		exec.Command("tmux", "delete-buffer", "-b", bufferName).Run() //nolint:errcheck
		return fmt.Errorf("tmux paste-buffer: %w", err)
	}
	return nil
}

// WaitForPane polls captured pane content until predicate succeeds or ctx ends.
func WaitForPane(ctx context.Context, paneID string, interval time.Duration, predicate func(string) bool) error {
	if interval <= 0 {
		interval = 25 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		content, err := CapturePaneContent(paneID)
		if err != nil {
			return err
		}
		if predicate(content) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// SendKeysLiteral sends text literally to a tmux pane (using -l flag to prevent
// tmux from interpreting special sequences), then sends Enter separately.
func SendKeysLiteral(paneID string, text string) error {
	if err := SendLiteral(paneID, text); err != nil {
		return err
	}
	if err := SendNamedKeys(paneID, "Enter"); err != nil {
		return fmt.Errorf("send-keys Enter: %w", err)
	}
	return nil
}

// GetClientSession returns the active session/window/pane for the current tmux client.
func GetClientSession() (sessionName string, windowIndex int, paneIndex int, paneID string, err error) {
	out, err := exec.Command("tmux", "display-message", "-p",
		"#{session_name}\x1f#{window_index}\x1f#{pane_index}\x1f#{pane_id}").Output()
	if err != nil {
		return "", 0, 0, "", fmt.Errorf("tmux display-message: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\x1f")
	if len(parts) != 4 {
		return "", 0, 0, "", fmt.Errorf("unexpected format: %q", string(out))
	}
	windowIndex, _ = strconv.Atoi(parts[1])
	paneIndex, _ = strconv.Atoi(parts[2])
	return parts[0], windowIndex, paneIndex, parts[3], nil
}

// switchToPane is the shared implementation for pane switching.
// Uses a single tmux command with chained actions to minimize subprocess overhead.
func switchToPane(sessionName string, windowIndex, paneIndex int) error {
	target := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	paneTarget := fmt.Sprintf("%s:%d.%d", sessionName, windowIndex, paneIndex)
	return exec.Command("tmux",
		"select-window", "-t", target, ";",
		"select-pane", "-t", paneTarget, ";",
		"switch-client", "-t", sessionName,
	).Run()
}

// SwitchToPaneQuiet switches to a pane without the flash highlight effect.
func SwitchToPaneQuiet(sessionName string, windowIndex, paneIndex int) error {
	return switchToPane(sessionName, windowIndex, paneIndex)
}

func SwitchToPane(sessionName string, windowIndex, paneIndex int, paneID string) error {
	if err := switchToPane(sessionName, windowIndex, paneIndex); err != nil {
		return err
	}
	// tmux run-shell -b runs in background within tmux, survives our exit
	exec.Command("tmux", "run-shell", "-b", fmt.Sprintf(
		"sleep 0.2; tmux select-pane -t %s -P bg=colour237; sleep 0.15; tmux select-pane -t %s -P default",
		paneID, paneID,
	)).Run()
	return nil
}
