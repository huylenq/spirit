package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/app"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/scripting"
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
		case "eval":
			runEval()
			return
		case "orchestrator":
			runOrchestrator()
			return
		case "popup":
			runPopup()
			return
		case "--agent-help":
			printAgentHelp()
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
		tea.WithMouseCellMotion(),
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
  cmc eval <file.lua>        Evaluate a Lua script against the daemon
  cmc eval -e '<expr>'       Evaluate an inline Lua expression
  echo '<expr>' | cmc eval   Evaluate Lua from stdin
  cmc orchestrator register <session-id>     Exclude session from eval sessions()
  cmc orchestrator unregister <session-id>   Re-include session
  cmc capture [CxR]    Capture a text snapshot to stdout (e.g. 160x40)
  cmc setup            Install Claude Code hooks into ~/.claude/settings.json
  cmc _hook <type>     Handle a Claude Code hook event (internal, called by hooks)
  cmc daemon           Start the background daemon
  cmc daemon --check   Exit 0 if daemon is running, 1 otherwise
  cmc daemon --stop    Stop the running daemon

  cmc --agent-help     Machine-readable reference for LLM agents using cmc

The daemon polls sessions every 1s and pushes updates to connected clients.
It auto-shuts down after 10 minutes with no clients.

Files:
  ~/.cache/cmc/daemon.sock   Unix socket
  ~/.cache/cmc/daemon.pid    PID file
  ~/.cache/cmc/daemon.log    Log output
`, version)
}

func printAgentHelp() {
	fmt.Print(`# cmc — Claude Mission Control

CLI for monitoring and controlling Claude Code sessions across tmux panes.
Requires a running daemon (auto-started on first use).

## CLI Commands

cmc eval -e '<lua>'              Evaluate inline Lua, print JSON result to stdout
cmc eval <file.lua>              Evaluate Lua file
echo '<lua>' | cmc eval          Evaluate from stdin
cmc orchestrator register <id>   Exclude session ID from sessions()
cmc orchestrator unregister <id> Re-include session ID
cmc capture [COLSxROWS]         Text snapshot of TUI to stdout
cmc daemon --check               Exit 0 if daemon running
cmc daemon --stop                Stop daemon

## Eval: Lua Scripting Interface

Sandboxed Lua VM (base/table/string/math only — no os/io/debug).
Each invocation is stateless. Last expression is JSON-serialized to stdout.
Errors go to stderr, exit 1. Use pcall() for recovery.

### Session Discovery

sessions()                       All sessions (orchestrator-excluded)
sessions({status = "idle"})      Filter: "idle" or "working"
session(id)                      Single session by ID, or nil

Session fields: id, pane_id, status ("idle"|"working"), display_name,
  project, cwd, git_branch, tmux_session, tmux_window, tmux_pane, pid,
  first_message, last_user_message, headline, custom_title,
  permission_mode, stop_reason, is_waiting, compact_count,
  commit_done_pending, queue_pending, created_at, last_changed

### Send & Wait

send(id, msg)                              Fire-and-forget to tmux pane
send(id, msg, {wait="idle"})               Block until idle
send(id, msg, {wait="working"})            Block until working
send(id, msg, {wait="idle", timeout=60})   With timeout (seconds)
queue(id, msg)                             Deliver when session becomes idle
cancel_queue(id)                           Cancel queued message
wait(id)                                   Block until idle (default 300s)
wait(id, {timeout=30})                     With timeout

### Lifecycle

spawn(cwd)                                           New Claude session
spawn(cwd, {tmux_session="main", message="do X"})   With options
  Returns: {session_id=..., pane_id=...}             Blocks up to 30s
kill(id)                                             SIGTERM + cleanup

### Orchestrator

register_orchestrator(id)        Exclude from sessions() results
unregister_orchestrator(id)      Re-include

### Features

later(id)                        Bookmark session
later_kill(id)                   Bookmark + kill pane
unlater(bookmark_id)             Remove bookmark
synthesize(id)                   LLM summary → {headline, from_cache}
synthesize_all()                 Summarize all sessions
commit(id)                       Send /commit (no auto-kill)
commit_done(id)                  Send /commit + kill on completion
cancel_commit_done(id)           Cancel pending auto-kill
transcript(id)                   User messages (string array)
raw_transcript(id)               Parsed entries [{index, type, content_type, summary, timestamp}]
diff_stats(id)                   {filepath = {added=N, removed=N}}
diff_hunks(id)                   [{file_path, old_string, new_string, is_write}]
summary(id)                      Cached summary {headline} or nil
hook_events(id)                  [{time, hook_type, effect}]

### Utilities

sleep(n)                         Sleep n seconds
log(...)                         Print to stderr (not in JSON output)

## Examples

# List idle sessions
cmc eval -e 'return sessions({status = "idle"})'

# Send to all idle sessions
cmc eval -e 'for _, s in ipairs(sessions({status="idle"})) do send(s.id, "run tests") end'

# Spawn, work, collect results
cmc eval -e '
  s = spawn("/tmp/myproject")
  send(s.session_id, "fix the tests", {wait="idle", timeout=300})
  return diff_stats(s.session_id)
'

# Orchestrator self-exclusion
cmc orchestrator register <my-session-id>
cmc eval -e 'return sessions()'  -- won't include the orchestrator
`)
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

	type hookRegistration struct {
		HookType string
		Matcher  string // empty = catch-all
	}
	regs := []hookRegistration{
		{"PreToolUse", ""},
		{"PostToolUse", "Bash|Edit|Write"},
		{"UserPromptSubmit", ""},
		{"Stop", ""},
		{"Notification", ""},
		{"SessionStart", ""},
		{"SessionEnd", ""},
		{"PreCompact", ""},
	}

	hooksMap, _ := settings["hooks"].(map[string]any)
	if hooksMap == nil {
		hooksMap = map[string]any{}
	}

	changed := false
	for _, reg := range regs {
		cmd := exe + " _hook " + reg.HookType + " " + hookMarker
		newGroups, modified := upsertHookCmd(hooksMap[reg.HookType], cmd, reg.Matcher)
		if modified {
			hooksMap[reg.HookType] = newGroups
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
func upsertHookCmd(existing any, newCmd, matcher string) ([]any, bool) {
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
				existingMatcher, _ := gm["matcher"].(string)
				if cmd == newCmd && existingMatcher == matcher {
					return groups, false // already up to date
				}
				hm["command"] = newCmd
				hooks[i] = hm
				gm["matcher"] = matcher
				return groups, true
			}
		}
	}

	// No existing cmc hook — append a new group
	newGroup := map[string]any{
		"matcher": matcher,
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

func runEval() {
	var script string

	switch {
	case len(os.Args) > 2 && os.Args[2] == "-e":
		// Inline expression: cmc eval -e 'expr'
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: cmc eval -e '<expression>'")
			os.Exit(1)
		}
		script = os.Args[3]

	case len(os.Args) > 2 && os.Args[2] != "-":
		// File: cmc eval script.lua
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", os.Args[2], err)
			os.Exit(1)
		}
		script = string(data)

	default:
		// Stdin: echo 'expr' | cmc eval
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		script = string(data)
	}

	if strings.TrimSpace(script) == "" {
		fmt.Fprintln(os.Stderr, "empty script")
		os.Exit(1)
	}

	client, err := daemon.ConnectRPCOnly()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to daemon: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	result, err := scripting.RunEval(script, client, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if result != "" {
		fmt.Println(result)
	}
}

func runOrchestrator() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: cmc orchestrator register|unregister <session-id>")
		os.Exit(1)
	}
	action := os.Args[2]
	sessionID := os.Args[3]

	client, err := daemon.ConnectRPCOnly()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to daemon: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	switch action {
	case "register":
		if err := client.RegisterOrchestrator(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "unregister":
		if err := client.UnregisterOrchestrator(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown orchestrator action: %s (expected register or unregister)\n", action)
		os.Exit(1)
	}
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

