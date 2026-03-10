package tmux

import (
	"fmt"
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
		panes = append(panes, PaneInfo{
			PaneID:      parts[0],
			PanePID:     pid,
			CurrentPath: parts[2],
			SessionName: parts[3],
			WindowIndex: winIdx,
			PaneIndex:   paneIdx,
			PaneCreated: time.Unix(created, 0),
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
	PaneID                            string
	SessionName                       string
	WindowIndex                       int
	WindowName                        string
	PaneTitle                         string
	PaneIndex                         int
	Left, Top                         int
	Width, Height                     int
	WindowWidth, WindowHeight         int
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

// ListWindowPanes returns all panes in a specific tmux window.
func ListWindowPanes(sessionName string, windowIndex int) ([]PaneInfo, error) {
	target := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F",
		"#{pane_id}:#{pane_pid}:#{pane_current_path}:#{session_name}:#{window_index}:#{pane_index}:#{pane_created}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes -t %s: %w", target, err)
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
		panes = append(panes, PaneInfo{
			PaneID:      parts[0],
			PanePID:     pid,
			CurrentPath: parts[2],
			SessionName: parts[3],
			WindowIndex: winIdx,
			PaneIndex:   paneIdx,
			PaneCreated: time.Unix(created, 0),
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

// KillPane closes a tmux pane by ID.
func KillPane(paneID string) error {
	return exec.Command("tmux", "kill-pane", "-t", paneID).Run()
}

// SendKeys sends text to a tmux pane via send-keys.
func SendKeys(paneID string, keys ...string) error {
	args := append([]string{"send-keys", "-t", paneID}, keys...)
	return exec.Command("tmux", args...).Run()
}

// SendKeysLiteral sends text literally to a tmux pane (using -l flag to prevent
// tmux from interpreting special sequences), then sends Enter separately.
func SendKeysLiteral(paneID string, text string) error {
	if err := exec.Command("tmux", "send-keys", "-t", paneID, "-l", text).Run(); err != nil {
		return fmt.Errorf("send-keys -l: %w", err)
	}
	if err := exec.Command("tmux", "send-keys", "-t", paneID, "Enter").Run(); err != nil {
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
