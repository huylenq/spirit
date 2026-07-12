package claude

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/tmux"
)

func useTempStatusDir(t *testing.T) string {
	t.Helper()
	oldDir := cachedStatusDir
	dir := t.TempDir()
	cachedStatusDir = dir
	statusDirOnce = sync.Once{}
	statusDirOnce.Do(func() {})
	t.Cleanup(func() {
		cachedStatusDir = oldDir
		statusDirOnce = sync.Once{}
		if oldDir != "" {
			statusDirOnce.Do(func() {})
		}
		transcriptPathCacheMu.Lock()
		transcriptPathCache = make(map[string]string)
		transcriptPathCacheMu.Unlock()
	})
	return dir
}

func TestSessionMetaRoundTripAndMerge(t *testing.T) {
	dir := useTempStatusDir(t)
	meta := SessionMeta{Provider: ProviderCodex, SessionID: "codex-1", TurnID: "turn-1", TranscriptPath: "/tmp/rollout.jsonl", CWD: "/repo", Model: "gpt-test"}
	if err := WriteSessionMeta(meta); err != nil {
		t.Fatal(err)
	}
	if err := WriteSessionMeta(SessionMeta{Provider: ProviderCodex, SessionID: "codex-1", TurnID: "turn-2"}); err != nil {
		t.Fatal(err)
	}
	got := ReadSessionMeta("codex-1")
	if got.Provider != ProviderCodex || got.TurnID != "turn-2" || got.TranscriptPath != meta.TranscriptPath || got.Model != meta.Model {
		t.Fatalf("unexpected merged metadata: %#v", got)
	}
	RemoveSessionFiles("codex-1")
	if _, err := os.Stat(filepath.Join(dir, "codex-1.meta.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata was not cleaned up: %v", err)
	}
}

func TestFindProviderInNestedProcessTree(t *testing.T) {
	tree := map[int][]processInfo{10: {{PID: 11, Comm: "zsh"}}, 11: {{PID: 12, Comm: "codex"}, {PID: 13, Comm: "claude"}}}
	comm := map[int]string{10: "tmux", 11: "zsh", 12: "codex", 13: "claude"}
	if got := findProviderInTree(tree, comm, 10, ProviderCodex); got != 12 {
		t.Fatalf("codex pid = %d, want 12", got)
	}
	if got := findProviderInTree(tree, comm, 10, ProviderClaude); got != 13 {
		t.Fatalf("claude pid = %d, want 13", got)
	}
}

func TestBuildUnregisteredCodexSession(t *testing.T) {
	created := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	pane := tmux.PaneInfo{
		PaneID: "%42", PanePID: 123, CurrentPath: "/work/example",
		SessionName: "main", WindowIndex: 4, PaneIndex: 2, PaneCreated: created,
	}
	got := buildUnregisteredCodexSession(pane, 456, map[string]string{"example": "EX"})
	if got.Provider != ProviderCodex || got.SessionID != "" || got.PaneID != "%42" || got.PID != 456 {
		t.Fatalf("unexpected pane-only session: %#v", got)
	}
	if got.Project != "example" || got.ProjectCode != "EX" || got.Status != StatusUserTurn || !got.LastChanged.Equal(created) {
		t.Fatalf("unexpected pane-only metadata: %#v", got)
	}
}

func TestCodexTranscriptAdapter(t *testing.T) {
	useTempStatusDir(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"timestamp":"2026-07-12T01:02:03Z","type":"session_meta","payload":{"id":"codex-1","cwd":"/repo"}}` + "\n" +
		`{"timestamp":"2026-07-12T01:02:04Z","type":"event_msg","payload":{"type":"user_message","message":"Fix the parser"}}` + "\n" +
		`{"timestamp":"2026-07-12T01:02:05Z","type":"event_msg","payload":{"type":"agent_message","message":"I am inspecting it.","phase":"commentary"}}` + "\n" +
		`{"timestamp":"2026-07-12T01:02:06Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}` + "\n" +
		`{"timestamp":"2026-07-12T01:02:07Z","type":"event_msg","payload":{"type":"agent_message","message":"Parser fixed.","phase":"final"}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSessionMeta(SessionMeta{Provider: ProviderCodex, SessionID: "codex-1", TranscriptPath: path}); err != nil {
		t.Fatal(err)
	}
	msgs, err := ReadUserMessages("codex-1")
	if err != nil || len(msgs) != 1 || msgs[0] != "Fix the parser" {
		t.Fatalf("user messages = %#v, err=%v", msgs, err)
	}
	if got := ReadLastAssistantInfo("codex-1").Message; got != "Parser fixed." {
		t.Fatalf("last assistant message = %q", got)
	}
	turn := ReadCurrentTurn("codex-1")
	if len(turn.Events) != 1 || turn.Events[0].Text != "I am inspecting it.\n\nParser fixed." {
		t.Fatalf("current turn = %#v", turn)
	}
	entries, err := ReadTranscriptEntries("codex-1")
	if err != nil || len(entries) != 5 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	if entries[0].Type != "assistant" || entries[0].ContentType != "final" {
		t.Fatalf("newest entry = %#v", entries[0])
	}
}
