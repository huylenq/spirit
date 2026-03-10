package scripting

import (
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// registerFeatureAPIs registers thin wrappers around existing daemon client methods.
func registerFeatureAPIs(L *lua.LState, client *daemon.Client) {
	// Later bookmarks
	L.SetGlobal("later", L.NewFunction(luaLater(client)))
	L.SetGlobal("later_kill", L.NewFunction(luaLaterKill(client)))
	L.SetGlobal("unlater", L.NewFunction(luaUnlater(client)))

	// Synthesis
	L.SetGlobal("synthesize", L.NewFunction(luaSynthesize(client)))
	L.SetGlobal("synthesize_all", L.NewFunction(luaSynthesizeAll(client)))

	// Commit
	L.SetGlobal("commit", L.NewFunction(luaCommit(client)))
	L.SetGlobal("commit_done", L.NewFunction(luaCommitDone(client)))
	L.SetGlobal("cancel_commit_done", L.NewFunction(luaCancelCommitDone(client)))

	// Transcript
	L.SetGlobal("transcript", L.NewFunction(luaTranscript(client)))
	L.SetGlobal("raw_transcript", L.NewFunction(luaRawTranscript(client)))

	// Diff
	L.SetGlobal("diff_stats", L.NewFunction(luaDiffStats(client)))
	L.SetGlobal("diff_hunks", L.NewFunction(luaDiffHunks(client)))

	// Summary + hooks
	L.SetGlobal("summary", L.NewFunction(luaSummary(client)))
	L.SetGlobal("hook_events", L.NewFunction(luaHookEvents(client)))
}

// later(id) — requires resolving sessionID → paneID
func luaLater(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		paneID := resolvePane(L, client, id)
		if err := client.Later(paneID, id); err != nil {
			L.RaiseError("later: %v", err)
		}
		return 0
	}
}

// later_kill(id)
func luaLaterKill(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		s := resolveSession(L, client, id)
		if err := client.LaterKill(s.PaneID, s.PID, id); err != nil {
			L.RaiseError("later_kill: %v", err)
		}
		return 0
	}
}

// unlater(bookmark_id)
func luaUnlater(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		bookmarkID := L.CheckString(1)
		if err := client.Unlater(bookmarkID); err != nil {
			L.RaiseError("unlater: %v", err)
		}
		return 0
	}
}

// synthesize(id)
func luaSynthesize(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		paneID := resolvePane(L, client, id)
		summary, fromCache, err := client.Synthesize(paneID, id)
		if err != nil {
			L.RaiseError("synthesize: %v", err)
			return 0
		}
		t := L.NewTable()
		t.RawSetString("from_cache", lua.LBool(fromCache))
		if summary != nil {
			t.RawSetString("headline", lua.LString(summary.Headline))
		}
		L.Push(t)
		return 1
	}
}

// synthesize_all()
func luaSynthesizeAll(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		results, err := client.SynthesizeAll("")
		if err != nil {
			L.RaiseError("synthesize_all: %v", err)
			return 0
		}
		t := L.NewTable()
		for _, r := range results {
			entry := L.NewTable()
			entry.RawSetString("pane_id", lua.LString(r.PaneID))
			entry.RawSetString("from_cache", lua.LBool(r.FromCache))
			if r.Summary != nil {
				entry.RawSetString("headline", lua.LString(r.Summary.Headline))
			}
			t.Append(entry)
		}
		L.Push(t)
		return 1
	}
}

// commit(id) — commit only, no auto-kill
func luaCommit(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		s := resolveSession(L, client, id)
		if err := client.CommitOnly(s.PaneID, id, s.PID); err != nil {
			L.RaiseError("commit: %v", err)
		}
		return 0
	}
}

// commit_done(id) — commit + auto-kill on completion
func luaCommitDone(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		s := resolveSession(L, client, id)
		if err := client.CommitAndDone(s.PaneID, id, s.PID); err != nil {
			L.RaiseError("commit_done: %v", err)
		}
		return 0
	}
}

// cancel_commit_done(id)
func luaCancelCommitDone(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := client.CancelCommitDone(id); err != nil {
			L.RaiseError("cancel_commit_done: %v", err)
		}
		return 0
	}
}

// transcript(id) — user messages
func luaTranscript(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		msgs, err := client.Transcript(id)
		if err != nil {
			L.RaiseError("transcript: %v", err)
			return 0
		}
		t := L.NewTable()
		for _, m := range msgs {
			t.Append(lua.LString(m))
		}
		L.Push(t)
		return 1
	}
}

// raw_transcript(id) — parsed transcript entries
func luaRawTranscript(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		entries, err := client.TranscriptEntries(id)
		if err != nil {
			L.RaiseError("raw_transcript: %v", err)
			return 0
		}
		t := L.NewTable()
		for _, e := range entries {
			entry := L.NewTable()
			entry.RawSetString("index", lua.LNumber(e.Index))
			entry.RawSetString("type", lua.LString(e.Type))
			entry.RawSetString("content_type", lua.LString(e.ContentType))
			entry.RawSetString("summary", lua.LString(e.Summary))
			entry.RawSetString("timestamp", lua.LString(e.Timestamp))
			t.Append(entry)
		}
		L.Push(t)
		return 1
	}
}

// diff_stats(id)
func luaDiffStats(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		stats, err := client.DiffStats(id)
		if err != nil {
			L.RaiseError("diff_stats: %v", err)
			return 0
		}
		t := L.NewTable()
		for path, stat := range stats {
			entry := L.NewTable()
			entry.RawSetString("added", lua.LNumber(stat.Added))
			entry.RawSetString("removed", lua.LNumber(stat.Removed))
			t.RawSetString(path, entry)
		}
		L.Push(t)
		return 1
	}
}

// diff_hunks(id)
func luaDiffHunks(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		hunks, err := client.DiffHunks(id)
		if err != nil {
			L.RaiseError("diff_hunks: %v", err)
			return 0
		}
		t := L.NewTable()
		for _, h := range hunks {
			entry := L.NewTable()
			entry.RawSetString("file_path", lua.LString(h.FilePath))
			entry.RawSetString("old_string", lua.LString(h.OldString))
			entry.RawSetString("new_string", lua.LString(h.NewString))
			entry.RawSetString("is_write", lua.LBool(h.IsWrite))
			t.Append(entry)
		}
		L.Push(t)
		return 1
	}
}

// summary(id)
func luaSummary(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		summary, err := client.Summary(id)
		if err != nil {
			L.RaiseError("summary: %v", err)
			return 0
		}
		if summary == nil {
			L.Push(lua.LNil)
			return 1
		}
		t := L.NewTable()
		t.RawSetString("headline", lua.LString(summary.Headline))
		L.Push(t)
		return 1
	}
}

// hook_events(id)
func luaHookEvents(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		events, err := client.HookEvents(id)
		if err != nil {
			L.RaiseError("hook_events: %v", err)
			return 0
		}
		t := L.NewTable()
		for _, e := range events {
			entry := L.NewTable()
			entry.RawSetString("time", lua.LString(e.Time))
			entry.RawSetString("hook_type", lua.LString(e.HookType))
			entry.RawSetString("effect", lua.LString(e.Effect))
			t.Append(entry)
		}
		L.Push(t)
		return 1
	}
}

// resolvePane finds the paneID for a sessionID, or raises a Lua error.
func resolvePane(L *lua.LState, client *daemon.Client, sessionID string) string {
	return resolveSession(L, client, sessionID).PaneID
}

// resolveSession finds the full session for a sessionID, or raises a Lua error.
func resolveSession(L *lua.LState, client *daemon.Client, sessionID string) claude.ClaudeSession {
	sessions, err := client.Sessions("")
	if err != nil {
		L.RaiseError("resolve session: %v", err)
		return claude.ClaudeSession{}
	}
	for _, s := range sessions {
		if s.SessionID == sessionID {
			return s
		}
	}
	L.RaiseError("session not found: %s", sessionID)
	return claude.ClaudeSession{}
}
