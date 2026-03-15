# `cmc eval` — Lua Scripting Interface

Programmatically observe and control Claude Code sessions through Mission Control.

## Usage

```sh
cmc eval script.lua          # run a file
cmc eval -e 'return 1 + 1'   # inline expression
echo 'return sessions()' | cmc eval  # stdin
```

Output: JSON of the last expression to stdout. Errors go to stderr with exit code 1.

Each invocation is stateless — no persistent VM between calls.

## Environment

The VM is sandboxed: `base`, `table`, `string`, and `math` libraries are available. No `os`, `io`, `debug`, `package`, `dofile`, `loadfile`, or `load`.

Scripts can use `pcall()` for recoverable errors. Unhandled errors abort with exit 1.

## API Reference

### Session Discovery

```lua
-- All sessions (orchestrator-filtered)
sessions()

-- Filter by status: "idle" or "working"
sessions({status = "idle"})

-- Single session by ID (returns table or nil)
session("299f9e95-c051-4e76-898d-c4ca42e61d58")
```

Session tables have these fields:

| Field | Type | Description |
|---|---|---|
| `id` | string | Claude session ID |
| `pane_id` | string | tmux pane ID |
| `status` | string | `"idle"` or `"working"` |
| `display_name` | string | resolved display name (title → headline → first message) |
| `project` | string | basename of CWD |
| `cwd` | string | working directory |
| `git_branch` | string | current git branch |
| `tmux_session` | string | tmux session name |
| `tmux_window` | number | window index |
| `tmux_pane` | number | pane index |
| `pid` | number | Claude process PID |
| `first_message` | string | first user message |
| `last_user_message` | string | most recent user message |
| `headline` | string | synthesized summary headline |
| `custom_title` | string | user-set title via `/rename` |
| `permission_mode` | string | `"plan"`, `"bypassPermissions"`, etc. |
| `stop_reason` | string | from Stop hook (cleared on next agent-turn) |
| `is_waiting` | bool | true when waiting for permission/input |
| `compact_count` | number | number of PreCompact events |
| `commit_done_pending` | bool | waiting for commit completion |
| `queue_pending` | string | message queued for delivery |
| `created_at` | number | unix timestamp |
| `last_changed` | number | unix timestamp |

### Send & Wait

```lua
-- Fire and forget
send(id, "fix the bug in main.go")

-- Block until the session starts working
send(id, "fix the bug", {wait = "working"})

-- Block until the session finishes (with timeout)
send(id, "fix the bug", {wait = "idle", timeout = 120})

-- Queue a message for delivery when the session becomes idle
queue(id, "now run the tests")

-- Cancel a queued message
cancel_queue(id)

-- Block until a session reaches idle
wait(id)
wait(id, {timeout = 30})
```

`send` delivers text directly to the tmux pane regardless of session status. `queue` waits until the session is idle before delivering.

`wait` defaults to a 5-minute timeout.

### Lifecycle

```lua
-- Spawn a new Claude session (blocks until registered with daemon, up to 30s)
s = spawn("/path/to/project")
s = spawn("/path/to/project", {tmux_session = "main"})
s = spawn("/path/to/project", {message = "fix the tests"})
-- Returns: {session_id = "...", pane_id = "%42"}

-- Kill a session (SIGTERM + kill pane + cleanup)
kill(id)
```

### Orchestrator Exclusion

When a Claude session is acting as an orchestrator, it should exclude itself from `sessions()` results so it doesn't try to manage itself.

```lua
register_orchestrator("my-session-id")
unregister_orchestrator("my-session-id")
```

Also available as CLI commands:
```sh
cmc orchestrator register <session-id>
cmc orchestrator unregister <session-id>
```

### Mission Control Features

```lua
-- Later
later(id)                  -- mark session for later
later_kill(id)             -- mark later + kill pane
unlater(later_id)       -- remove Later record

-- Synthesis (LLM-generated summaries)
synthesize(id)             -- returns {headline = "...", from_cache = bool}
synthesize_all()           -- synthesize all sessions

-- Commit tracking
commit(id)                 -- send /commit (no auto-kill)
commit_done(id)            -- send /commit + kill on completion
cancel_commit_done(id)     -- cancel pending auto-kill

-- Transcript
transcript(id)             -- user messages (string array)
raw_transcript(id)         -- parsed entries with type, summary, timestamp

-- Diffs
diff_stats(id)             -- {filepath = {added = N, removed = N}}
diff_hunks(id)             -- [{file_path, old_string, new_string, is_write}]

-- Summary & hooks
summary(id)                -- cached summary or nil
hook_events(id)            -- [{time, hook_type, effect}]
```

### Utilities

```lua
sleep(5)                   -- sleep N seconds
log("debug info")          -- print to stderr (not part of JSON output)
```

## Examples

### List idle sessions and their projects

```lua
idle = sessions({status = "idle"})
result = {}
for i, s in ipairs(idle) do
  result[i] = {id = s.id, project = s.project, name = s.display_name}
end
return result
```

### Fan out a task to all idle sessions

```lua
for _, s in ipairs(sessions({status = "idle"})) do
  send(s.id, "run the linter and fix any issues")
end
```

### Spawn, send, wait for completion

```lua
s = spawn("/home/user/myproject", {tmux_session = "work"})
send(s.session_id, "add unit tests for the auth module", {wait = "idle", timeout = 300})
return diff_stats(s.session_id)
```

### Orchestrator pattern

```lua
-- Self-exclude so we don't see ourselves
register_orchestrator("my-orchestrator-session-id")

-- Work with managed sessions
for _, s in ipairs(sessions({status = "idle"})) do
  send(s.id, "refactor error handling")
end

-- Wait for all to finish
for _, s in ipairs(sessions()) do
  if s.status == "working" then
    wait(s.id, {timeout = 600})
  end
end

return sessions()
```

### Commit all idle sessions

```lua
for _, s in ipairs(sessions({status = "idle"})) do
  commit_done(s.id)
end
```
