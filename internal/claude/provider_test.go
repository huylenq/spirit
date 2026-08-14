package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	tree := map[int][]processInfo{10: {{PID: 11, Comm: "zsh", Raw: "zsh"}}, 11: {{PID: 12, Comm: "codex", Raw: "codex"}, {PID: 13, Comm: "claude", Raw: "claude"}}}
	byPID := map[int]processInfo{
		10: {PID: 10, Comm: "tmux", Raw: "tmux"},
		11: {PID: 11, Comm: "zsh", Raw: "zsh"},
		12: {PID: 12, Comm: "codex", Raw: "codex"},
		13: {PID: 13, Comm: "claude", Raw: "claude"},
	}
	if got := findProviderInTree(tree, byPID, 10, ProviderCodex); got != 12 {
		t.Fatalf("codex pid = %d, want 12", got)
	}
	if got := findProviderInTree(tree, byPID, 10, ProviderClaude); got != 13 {
		t.Fatalf("claude pid = %d, want 13", got)
	}
}

func TestFindProviderMatchesSelfUpdatedVersionedBinary(t *testing.T) {
	// A CLI self-update re-execs the process into its versioned install path
	// (e.g. ~/.local/share/claude/versions/2.1.226); ps then reports that full
	// path as comm, with a bare version number as its basename.
	raw := "/Users/huy/.local/share/claude/versions/2.1.226"
	tree := map[int][]processInfo{}
	byPID := map[int]processInfo{
		20: {PID: 20, Comm: filepath.Base(raw), Raw: raw},
	}
	if got := findProviderInTree(tree, byPID, 20, ProviderClaude); got != 20 {
		t.Fatalf("claude pid = %d, want 20 (self-updated versioned binary should still match)", got)
	}
	if got := findProviderInTree(tree, byPID, 20, ProviderCodex); got != 0 {
		t.Fatalf("codex pid = %d, want 0 (must not cross-match another provider's versions dir)", got)
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

func TestCodexCompletedItemTranscriptAdapterFindsPromptAfterLargePreamble(t *testing.T) {
	useTempStatusDir(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	// Real Codex rollouts can put more than 2 MiB of injected context in one
	// session_meta record. The prompt after it must still reach synthesis.
	data := `{"type":"session_meta","payload":{"id":"codex-modern","instructions":"` + strings.Repeat("x", 3*1024*1024) + `"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"injected context"}]}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"Fix the modern parser"}]}}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","phase":"final","content":[{"type":"Text","text":"Modern parser fixed."}]}}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSessionMeta(SessionMeta{Provider: ProviderCodex, SessionID: "codex-modern", TranscriptPath: path}); err != nil {
		t.Fatal(err)
	}
	if got := ReadFirstUserMessage("codex-modern"); got != "Fix the modern parser" {
		t.Fatalf("first user message = %q", got)
	}
	msgs, err := ReadUserMessages("codex-modern")
	if err != nil || len(msgs) != 1 || msgs[0] != "Fix the modern parser" {
		t.Fatalf("user messages = %#v, err=%v", msgs, err)
	}
	if got := ReadLastAssistantInfo("codex-modern").Message; got != "Modern parser fixed." {
		t.Fatalf("last assistant message = %q", got)
	}
	entries, err := ReadTranscriptEntries("codex-modern")
	if err != nil || len(entries) != 4 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	if entries[0].Type != "assistant" || entries[0].ContentType != "final" || entries[1].Type != "user" {
		t.Fatalf("newest entries = %#v", entries[:2])
	}
}

func TestCodexCustomTitleReadsLatestSessionIndexEntry(t *testing.T) {
	useTempStatusDir(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if err := WriteSessionMeta(SessionMeta{Provider: ProviderCodex, SessionID: "codex-title"}); err != nil {
		t.Fatal(err)
	}
	index := strings.Join([]string{
		`{"id":"codex-title","thread_name":"old title","updated_at":"2026-07-12T01:02:03Z"}`,
		`{"id":"other","thread_name":"other title","updated_at":"2026-07-12T01:02:04Z"}`,
		`{"id":"codex-title","thread_name":"new title","updated_at":"2026-07-12T01:02:05Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(codexHome, "session_index.jsonl"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadCustomTitle("codex-title"); got != "new title" {
		t.Fatalf("Codex custom title = %q, want %q", got, "new title")
	}
}

func TestCodexCustomTitleReadsStateDatabaseName(t *testing.T) {
	sqlite3, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not installed")
	}
	useTempStatusDir(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if err := WriteSessionMeta(SessionMeta{Provider: ProviderCodex, SessionID: "codex-state-title"}); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(codexHome, "state_5.sqlite")
	cmd := exec.Command(sqlite3, statePath, "create table threads (id text, name text); insert into threads values ('codex-state-title', 'state title');")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create Codex state database: %v: %s", err, output)
	}
	if got := ReadCustomTitle("codex-state-title"); got != "state title" {
		t.Fatalf("Codex custom title = %q, want %q", got, "state title")
	}
}

func TestCodexCustomTitleFallsBackToTranscriptEvent(t *testing.T) {
	useTempStatusDir(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"type":"event_msg","payload":{"type":"thread_name_updated","thread_name":"transcript title"}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSessionMeta(SessionMeta{Provider: ProviderCodex, SessionID: "codex-transcript-title", TranscriptPath: path}); err != nil {
		t.Fatal(err)
	}
	if got := ReadCustomTitle("codex-transcript-title"); got != "transcript title" {
		t.Fatalf("Codex transcript custom title = %q, want %q", got, "transcript title")
	}
}
