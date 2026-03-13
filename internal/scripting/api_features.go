package scripting

import (
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// later(id)
// Category: Features
// Bookmark session for later review.
func luaLater(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		paneID := resolvePane(L, deps.Client, id)
		if err := deps.Client.Later(paneID, id); err != nil {
			L.RaiseError("later: %v", err)
		}
		return 0
	}
}

// later_kill(id)
// Category: Features
// Bookmark session and kill its pane.
func luaLaterKill(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		s := resolveSession(L, deps.Client, id)
		if err := deps.Client.LaterKill(s.PaneID, s.PID, id); err != nil {
			L.RaiseError("later_kill: %v", err)
		}
		return 0
	}
}

// unlater(bookmark_id)
// Category: Features
// Remove a bookmark by its bookmark ID.
func luaUnlater(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		bookmarkID := L.CheckString(1)
		if err := deps.Client.Unlater(bookmarkID); err != nil {
			L.RaiseError("unlater: %v", err)
		}
		return 0
	}
}

// synthesize(id) -> {synthesized_title, from_cache}
// Category: Features
// Generate LLM summary for session.
func luaSynthesize(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		paneID := resolvePane(L, deps.Client, id)
		summary, fromCache, err := deps.Client.Synthesize(paneID, id)
		if err != nil {
			L.RaiseError("synthesize: %v", err)
			return 0
		}
		t := L.NewTable()
		t.RawSetString("from_cache", lua.LBool(fromCache))
		if summary != nil {
			t.RawSetString("synthesized_title", lua.LString(summary.SynthesizedTitle))
		}
		L.Push(t)
		return 1
	}
}

// synthesize_all() -> [{pane_id, synthesized_title, from_cache}]
// Category: Features
// Generate LLM summaries for all sessions.
func luaSynthesizeAll(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		results, err := deps.Client.SynthesizeAll("")
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
				entry.RawSetString("synthesized_title", lua.LString(r.Summary.SynthesizedTitle))
			}
			t.Append(entry)
		}
		L.Push(t)
		return 1
	}
}

// commit(id)
// Category: Features
// Send /commit to session (no auto-kill).
func luaCommit(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		s := resolveSession(L, deps.Client, id)
		if err := deps.Client.CommitOnly(s.PaneID, id, s.PID); err != nil {
			L.RaiseError("commit: %v", err)
		}
		return 0
	}
}

// commit_done(id)
// Category: Features
// Send /commit and auto-kill session on completion.
func luaCommitDone(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		s := resolveSession(L, deps.Client, id)
		if err := deps.Client.CommitAndDone(s.PaneID, id, s.PID); err != nil {
			L.RaiseError("commit_done: %v", err)
		}
		return 0
	}
}

// cancel_commit_done(id)
// Category: Features
// Cancel pending commit-done auto-kill.
func luaCancelCommitDone(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		if err := deps.Client.CancelCommitDone(id); err != nil {
			L.RaiseError("cancel_commit_done: %v", err)
		}
		return 0
	}
}

// transcript(id) -> []string
// Category: Features
// Get user messages from session transcript.
func luaTranscript(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		msgs, err := deps.Client.Transcript(id)
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

// raw_transcript(id) -> []entry
// Category: Features
// Get parsed transcript entries with index, type, content_type, summary, timestamp.
func luaRawTranscript(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		entries, err := deps.Client.TranscriptEntries(id)
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

// diff_stats(id) -> {filepath: {added, removed}}
// Category: Features
// Get diff statistics per file for session.
func luaDiffStats(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		stats, err := deps.Client.DiffStats(id)
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

// diff_hunks(id) -> [{file_path, old_string, new_string, is_write}]
// Category: Features
// Get individual diff hunks for session.
func luaDiffHunks(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		hunks, err := deps.Client.DiffHunks(id)
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

// summary(id) -> {synthesized_title}|nil
// Category: Features
// Get cached summary for session, or nil.
func luaSummary(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		summary, err := deps.Client.Summary(id)
		if err != nil {
			L.RaiseError("summary: %v", err)
			return 0
		}
		if summary == nil {
			L.Push(lua.LNil)
			return 1
		}
		t := L.NewTable()
		t.RawSetString("synthesized_title", lua.LString(summary.SynthesizedTitle))
		L.Push(t)
		return 1
	}
}

// hook_events(id) -> [{time, hook_type, effect}]
// Category: Features
// Get hook events for session.
func luaHookEvents(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		events, err := deps.Client.HookEvents(id)
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
