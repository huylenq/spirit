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
	"sync"
	"syscall"

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
		case "dev":
			runDev()
			return
		case "mcp-serve":
			if err := runMCPServe(); err != nil {
				fmt.Fprintf(os.Stderr, "mcp-serve: %v\n", err)
				os.Exit(1)
			}
			return
		case "usage-dump":
			refresh := len(os.Args) > 2 && os.Args[2] == "--refresh"
			if refresh {
				raw, err := claude.FetchUsageRaw()
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				fmt.Print(raw)
			} else {
				client, err := daemon.Connect()
				if err != nil {
					fmt.Fprintln(os.Stderr, "daemon not running:", err)
					os.Exit(1)
				}
				_, usage, err := client.Subscribe()
				client.Close()
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				if usage == nil {
					fmt.Fprintln(os.Stderr, "no cached usage data (daemon may still be fetching)")
					os.Exit(1)
				}
				fmt.Printf("session=%d%% resets=%q\n", usage.SessionPct, usage.SessionResets)
				fmt.Printf("week_all=%d%% resets=%q\n", usage.WeekAllPct, usage.WeekAllResets)
				fmt.Printf("week_sonnet=%d%% resets=%q\n", usage.WeekSonnetPct, usage.WeekSonnetResets)
			}
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
  cmc dev              Dev picker: fzf over git worktrees, launch chosen worktree's TUI
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

` + scripting.LuaScriptingReference + `

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
		// Will flash on the tmux view, not nice!
		// fmt.Println("Hooks already up to date.")
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

// worktreeInfo holds a single entry from git worktree list --porcelain.
type worktreeInfo struct {
	path   string
	branch string // short name, e.g. "main" or "feat-x"
}

// listWorktrees returns all git worktrees for the given repo root.
func listWorktrees(repoRoot string) ([]worktreeInfo, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var result []worktreeInfo
	var cur worktreeInfo
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			cur.branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "":
			if cur.path != "" {
				if cur.branch == "" {
					cur.branch = filepath.Base(cur.path)
				}
				result = append(result, cur)
				cur = worktreeInfo{}
			}
		}
	}
	// handle final entry with no trailing blank line
	if cur.path != "" {
		if cur.branch == "" {
			cur.branch = filepath.Base(cur.path)
		}
		result = append(result, cur)
	}
	return result, nil
}

// ensureWorktreeBinary builds bin/cmc in the given worktree if it's missing
// or stale (older than the current process's binary).
func ensureWorktreeBinary(wtPath string) {
	binPath := filepath.Join(wtPath, "bin", "cmc")
	myExe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cmc dev: cannot resolve own executable: %v\n", err)
		os.Exit(1)
	}
	myExe, _ = filepath.EvalSymlinks(myExe)

	binInfo, statErr := os.Stat(binPath)
	stale := false
	if statErr == nil {
		if myInfo, err := os.Stat(myExe); err == nil {
			stale = binInfo.ModTime().Before(myInfo.ModTime())
		}
	}
	if statErr == nil && !stale {
		return // up to date
	}

	var action string
	if statErr != nil {
		action = "building (no binary)"
	} else {
		action = "rebuilding (stale)"
	}
	fmt.Fprintf(os.Stderr, "cmc dev: %s %s/bin/cmc...\n", action, filepath.Base(wtPath))

	cmd := exec.Command("make", "build")
	cmd.Dir = wtPath
	cmd.Stdout = os.Stderr // show build output in popup
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cmc dev: build failed in %s: %v\n", wtPath, err)
		os.Exit(1)
	}
}

// execWorktreeCMC builds (if needed) then execs into the given worktree's
// bin/cmc, setting the appropriate env var for select-active or rotate-next mode.
// This replaces the current process — caller must not return.
func execWorktreeCMC(wtPath string, selectActive bool) {
	ensureWorktreeBinary(wtPath)
	binPath := filepath.Join(wtPath, "bin", "cmc")
	env := os.Environ()
	if selectActive {
		env = append(env, "CMC_SELECT_ACTIVE=1")
	} else {
		env = append(env, "CMC_ROTATE_NEXT=1")
	}
	if err := syscall.Exec(binPath, []string{binPath}, env); err != nil {
		fmt.Fprintln(os.Stderr, "cmc dev: exec:", err)
		os.Exit(1)
	}
}

// runDev is the dev-mode worktree picker.
// It lists all git worktrees for this binary's repo, shows an fzf picker with
// daemon status, and execs the chosen worktree's bin/cmc (auto-starting its
// daemon on first connect as usual).
func runDev() {
	selectActive := false
	rotateNext := false
	for _, arg := range os.Args[2:] {
		switch arg {
		case "--select-active":
			selectActive = true
		case "--rotate-next":
			rotateNext = true
		}
	}
	if !selectActive && !rotateNext {
		selectActive = true // default
	}

	// Resolve repo root from this binary's location.
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmc dev: cannot resolve executable:", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	repoRoot, err := daemon.RepoRootForDir(filepath.Dir(exe))
	if err != nil {
		// Fallback: try $PWD (useful when running `go run ./cmd/cmc dev`).
		cwd, _ := os.Getwd()
		repoRoot, err = daemon.RepoRootForDir(cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cmc dev: not in a git repository")
			os.Exit(1)
		}
	}

	worktrees, err := listWorktrees(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmc dev: listing worktrees:", err)
		os.Exit(1)
	}

	// Probe daemon liveness for all worktrees in parallel.
	alive := make([]bool, len(worktrees))
	var wg sync.WaitGroup
	for i, wt := range worktrees {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			info := daemon.WorkdirDaemonInfo(path)
			alive[i] = daemon.CheckAlive(info)
		}(i, wt.path)
	}
	wg.Wait()

	// Build fzf input lines: "N  branch  STATUS\t/path" (tab-delimited; fzf shows col 1 only).
	var lines []string
	for i, wt := range worktrees {
		status := "○"
		if alive[i] {
			status = "●"
		}
		label := fmt.Sprintf("%d  %-30s %s", i+1, wt.branch, status)
		lines = append(lines, label+"\t"+wt.path)
	}

	fzfCmd := exec.Command("fzf",
		"--height=~12",
		"--reverse",
		"--no-sort",
		"--delimiter=\t",
		"--with-nth=1",
		"--header=Select worktree  (● daemon running  ○ stopped)",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	// fzf draws its UI to /dev/tty; stdout carries the selected line.
	selected, err := fzfCmd.Output()
	if err != nil {
		return // user cancelled (exit 130) or fzf not available
	}

	parts := strings.SplitN(strings.TrimSpace(string(selected)), "\t", 2)
	if len(parts) < 2 {
		return
	}
	execWorktreeCMC(strings.TrimSpace(parts[1]), selectActive)
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

	result, msgs, err := scripting.RunEval(script, client, os.Stderr)
	for _, f := range msgs.Flashes {
		fmt.Fprintln(os.Stderr, f)
	}
	for _, t := range msgs.Toasts {
		fmt.Fprintln(os.Stderr, t)
	}
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
