package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/app"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "_hook":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: cmc _hook <HookType>")
				os.Exit(1)
			}
			claude.HandleHook(os.Args[2])
			return
		case "daemon":
			runDaemon()
			return
		case "setup":
			runSetup()
			return
		case "capture":
			runCapture()
			return
		case "popup":
			runPopup()
			return
		case "-h", "--help", "help":
			printUsage()
			return
		}
	}

	// Client mode — must be inside tmux
	if os.Getenv("TMUX") == "" {
		fmt.Fprintln(os.Stderr, "cmc must be run inside a tmux session")
		os.Exit(1)
	}

	client, err := daemon.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to daemon: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	p := tea.NewProgram(
		app.NewModel(client),
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`cmc %s — Claude Mission Control

Usage:
  cmc                  Launch the TUI (connects to daemon, auto-starts if needed)
  cmc popup            Open TUI in a tmux popup (respects zoom pref)
  cmc popup --select-active  Same, but auto-select the current pane (ctrl-space)
  cmc popup --rotate-next    Same, but skip current pane → next YOUR TURN (ctrl-tab)
  cmc capture [CxR]    Capture a text snapshot to stdout (e.g. 160x40)
  cmc setup            Install Claude Code hooks into ~/.claude/settings.json
  cmc _hook <type>     Handle a Claude Code hook event (internal, called by hooks)
  cmc daemon           Start the background daemon
  cmc daemon --check   Exit 0 if daemon is running, 1 otherwise
  cmc daemon --stop    Stop the running daemon

The daemon polls sessions every 1s and pushes updates to connected clients.
It auto-shuts down after 10 minutes with no clients.

Files:
  ~/.cache/cmc/daemon.sock   Unix socket
  ~/.cache/cmc/daemon.pid    PID file
  ~/.cache/cmc/daemon.log    Log output
`, version)
}

func runDaemon() {
	// Handle --check and --stop flags
	if len(os.Args) > 2 {
		info := daemon.DefaultDaemonInfo()
		switch os.Args[2] {
		case "--check":
			if daemon.CheckAlive(info) {
				fmt.Println("daemon is running")
				os.Exit(0)
			}
			fmt.Println("daemon is not running")
			os.Exit(1)
		case "-h", "--help", "help":
			printUsage()
			return
		case "--stop":
			if err := daemon.Stop(info); err != nil {
				fmt.Fprintf(os.Stderr, "Error stopping daemon: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	// Redirect log output to a file for debugging
	logPath := os.ExpandEnv("$HOME/.cache/cmc/daemon.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f)
		defer f.Close()
	}

	info := daemon.DefaultDaemonInfo()
	if err := daemon.Run(info); err != nil {
		log.Fatalf("daemon: %v", err)
	}
}

// hookMarker is embedded in hook commands so we can identify and migrate them later.
// Even if the binary or hook script is renamed, this marker stays constant.
const hookMarker = "#cmc-hook"

func runSetup() {
	// Resolve the absolute path to our own binary
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
		os.Exit(1)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving executable path: %v\n", err)
		os.Exit(1)
	}

	settingsPath := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")

	// Read existing settings or start fresh
	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", settingsPath, err)
			os.Exit(1)
		}
	}

	hookTypes := []string{"PreToolUse", "UserPromptSubmit", "Stop", "Notification"}
	hooksMap, _ := settings["hooks"].(map[string]any)
	if hooksMap == nil {
		hooksMap = map[string]any{}
	}

	changed := false
	for _, ht := range hookTypes {
		cmd := exe + " _hook " + ht + " " + hookMarker
		newGroups, modified := upsertHookCmd(hooksMap[ht], cmd)
		if modified {
			hooksMap[ht] = newGroups
			changed = true
		}
	}

	if !changed {
		fmt.Println("Hooks already up to date.")
		return
	}

	settings["hooks"] = hooksMap

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		os.Exit(1)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling settings: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(settingsPath, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", settingsPath, err)
		os.Exit(1)
	}

	fmt.Printf("Hooks installed in %s\n", settingsPath)
	fmt.Printf("Hook command: %s _hook <type>\n", exe)
}

// upsertHookCmd inserts or updates a cmc hook command in a hook type's group list.
// Identifies existing cmc hooks by hookMarker. Preserves non-cmc hooks untouched.
// Returns the (possibly new) groups slice and whether anything changed.
func upsertHookCmd(existing any, newCmd string) ([]any, bool) {
	groups, _ := existing.([]any)

	// Search existing groups for a cmc hook to update
	for _, group := range groups {
		gm, ok := group.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := gm["hooks"].([]any)
		if !ok {
			continue
		}
		for i, h := range hooks {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hm["command"].(string)
			if strings.Contains(cmd, hookMarker) {
				if cmd == newCmd {
					return groups, false // already up to date
				}
				hm["command"] = newCmd
				hooks[i] = hm
				return groups, true
			}
		}
	}

	// No existing cmc hook — append a new group
	newGroup := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": newCmd,
			},
		},
	}
	return append(groups, newGroup), true
}

// readPref reads a single key from the cmc prefs file (~/.cache/cmc/prefs).
func readPref(key string) string {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".cache", "cmc", "prefs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}

// runPopup opens a tmux display-popup with the cmc TUI.
// Reads the fullscreen preference to determine popup size.
// Flags:
//
//	--select-active  auto-select the pane the user was on when invoked (ctrl-space)
//	--rotate-next    skip originating pane, select next YOUR TURN session (ctrl-tab)
func runPopup() {
	selectActive := false
	rotateNext := false
	for _, arg := range os.Args[2:] {
		if arg == "--select-active" {
			selectActive = true
		}
		if arg == "--rotate-next" {
			rotateNext = true
		}
	}

	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
		os.Exit(1)
	}
	bin, _ = filepath.EvalSymlinks(bin)

	fullscreen := readPref("fullscreen") == "true"
	w, h := "80%", "70%"
	if fullscreen {
		w, h = "100%", "100%"
	}

	args := []string{"display-popup", "-B", "-E", "-w", w, "-h", h}
	if fullscreen {
		args = append(args, "-e", "CLAUDE_TUI_FULLSCREEN=1")
	}
	if selectActive {
		args = append(args, "-e", "CMC_SELECT_ACTIVE=1")
	}
	if rotateNext {
		args = append(args, "-e", "CMC_ROTATE_NEXT=1")
	}
	args = append(args, bin)

	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run() //nolint:errcheck
}

func runCapture() {
	cols, rows := 0, 0 // 0 = auto-detect
	if len(os.Args) > 2 {
		arg := os.Args[2]
		if arg == "-h" || arg == "--help" || arg == "help" {
			fmt.Println(`Usage: cmc capture [COLSxROWS]

Capture a text snapshot of the TUI to stdout.

Examples:
  cmc capture          Auto-detect terminal size (default 200x50 if not a TTY)
  cmc capture 160x40   Render at 160 columns by 40 rows`)
			return
		}
		if _, err := fmt.Sscanf(arg, "%dx%d", &cols, &rows); err != nil || cols <= 0 || rows <= 0 {
			fmt.Fprintf(os.Stderr, "Invalid resolution %q, expected COLSxROWS (e.g. 160x40)\n", arg)
			os.Exit(1)
		}
	}

	client, err := daemon.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to daemon: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	text, err := app.RenderCapture(client, cols, rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error capturing: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(text)
}

