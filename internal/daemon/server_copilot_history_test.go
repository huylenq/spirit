package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/claude"
)

// overrideHistoryDir redirects chat_history.json IO to a temp dir for the test.
func overrideHistoryDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	restore := claude.OverrideStatusDirForTest(dir)
	t.Cleanup(restore)
	return dir
}

// historySnapshot returns a copy of the daemon's in-memory display history.
func historySnapshot(d *Daemon) []CopilotHistoryMsg {
	d.copilotHistoryMu.RLock()
	defer d.copilotHistoryMu.RUnlock()
	return append([]CopilotHistoryMsg(nil), d.copilotHistory...)
}

// waitForHistory polls until cond holds on the in-memory history or fails the test.
func waitForHistory(t *testing.T, d *Daemon, cond func([]CopilotHistoryMsg) bool) []CopilotHistoryMsg {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := historySnapshot(d); cond(msgs) {
			return msgs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for history condition; have %+v", historySnapshot(d))
	return nil
}

// readHistoryFile parses the on-disk chat_history.json written under dir.
func readHistoryFile(t *testing.T, dir string) []CopilotHistoryMsg {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "copilot", "chat_history.json"))
	if err != nil {
		t.Fatalf("reading history file: %v", err)
	}
	var msgs []CopilotHistoryMsg
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("parsing history file: %v", err)
	}
	return msgs
}

// chatData marshals a minimal CopilotChatData for handler-level history tests.
func chatData(msg, requestID string) json.RawMessage {
	return marshalData(CopilotChatData{Message: msg, RequestID: requestID, ClientID: "client-A"})
}

// The ACP client must hand back whatever text already streamed when the turn is
// cancelled — a partial reply is history-persistable, not discardable.
func TestPromptReturnsPartialTextOnCancel(t *testing.T) {
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			f.textDelta(f.sessionID, "partial ")
			f.textDelta(f.sessionID, "answer")
			<-f.cancelled
			f.reply(id, map[string]any{"stopReason": "cancelled"})
		},
	}
	client := newFakeClient(t, f)
	f.start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	got := ""
	output, err := client.Prompt(ctx, "hi", func(evt CopilotStreamData) {
		if evt.Type != "text_delta" {
			return
		}
		mu.Lock()
		got += evt.Content
		if got == "partial answer" {
			cancel()
		}
		mu.Unlock()
	})
	if err == nil {
		t.Fatal("cancelled prompt should return an error")
	}
	if output != "partial answer" {
		t.Fatalf("cancelled prompt discarded the partial text: %q", output)
	}
}

// The user message must be durable from the moment of submit: a daemon killed
// mid-turn (e.g. a `make` restart) cannot erase what the user said.
func TestCopilotHistoryPersistsUserMessageAtSubmit(t *testing.T) {
	dir := overrideHistoryDir(t)
	release := make(chan struct{})
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			<-release
			f.textDelta(f.sessionID, "full reply")
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		},
	}
	client := newFakeClient(t, f)
	f.start()
	d := &Daemon{subscribers: map[*subscriber]struct{}{}, acpClient: client}

	if resp := d.handleCopilotChat(chatData("implement it", "req-1")); resp.Error != "" {
		t.Fatalf("chat rejected: %s", resp.Error)
	}

	// While the turn is still in flight, the user half is already on disk.
	waitForHistory(t, d, func(m []CopilotHistoryMsg) bool { return len(m) == 1 })
	onDisk := readHistoryFile(t, dir)
	if len(onDisk) != 1 || onDisk[0].Role != "user" || onDisk[0].Content != "implement it" {
		t.Fatalf("user message not durable at submit: %+v", onDisk)
	}

	close(release)
	msgs := waitForHistory(t, d, func(m []CopilotHistoryMsg) bool { return len(m) == 2 })
	if msgs[1].Role != "copilot" || msgs[1].Content != "full reply" {
		t.Fatalf("completed turn should append the copilot half: %+v", msgs[1])
	}
}

// A user-cancelled turn keeps its exchange: the user message plus whatever
// partial reply streamed, marked as interrupted.
func TestCopilotHistoryKeepsPartialOnCancel(t *testing.T) {
	overrideHistoryDir(t)
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			f.textDelta(f.sessionID, "partial ")
			f.textDelta(f.sessionID, "reply")
			<-f.cancelled
			f.reply(id, map[string]any{"stopReason": "cancelled"})
		},
	}
	client := newFakeClient(t, f)
	f.start()
	d := &Daemon{subscribers: map[*subscriber]struct{}{}, acpClient: client}
	sub := d.addSubscriber("client-A")

	if resp := d.handleCopilotChat(chatData("do it", "req-1")); resp.Error != "" {
		t.Fatalf("chat rejected: %s", resp.Error)
	}

	// Cancel only after the deltas reached the daemon, so the partial is real.
	seen := ""
	deadline := time.After(5 * time.Second)
	for seen != "partial reply" {
		select {
		case evt := <-sub.copilot:
			if evt.Type == "text_delta" {
				seen += evt.Content
			}
		case <-deadline:
			t.Fatalf("timed out streaming deltas; saw %q", seen)
		}
	}
	d.handleCopilotCancel()

	msgs := waitForHistory(t, d, func(m []CopilotHistoryMsg) bool { return len(m) == 2 })
	if msgs[0].Role != "user" || msgs[0].Content != "do it" {
		t.Fatalf("user half missing after cancel: %+v", msgs[0])
	}
	if msgs[1].Role != "copilot" || !strings.Contains(msgs[1].Content, "partial reply") || !strings.Contains(msgs[1].Content, "⚠ turn interrupted") {
		t.Fatalf("cancelled turn should keep the partial reply with a marker: %+v", msgs[1])
	}
}

// A turn superseded by the next prompt keeps its exchange too — both exchanges
// land in history, the superseded one marked interrupted.
func TestCopilotHistoryKeepsPartialOnSupersede(t *testing.T) {
	overrideHistoryDir(t)
	f := &fakeHermes{
		onPrompt: func(f *fakeHermes, id int64, text string) {
			if strings.Contains(text, "first") {
				f.textDelta(f.sessionID, "working on first")
				<-f.cancelled
				f.reply(id, map[string]any{"stopReason": "cancelled"})
				return
			}
			f.textDelta(f.sessionID, "second reply")
			f.reply(id, map[string]any{"stopReason": "end_turn"})
		},
	}
	client := newFakeClient(t, f)
	f.start()
	d := &Daemon{subscribers: map[*subscriber]struct{}{}, acpClient: client}
	sub := d.addSubscriber("client-A")

	if resp := d.handleCopilotChat(chatData("first", "req-1")); resp.Error != "" {
		t.Fatalf("first chat rejected: %s", resp.Error)
	}
	// Let the first turn stream before superseding it.
	deadline := time.After(5 * time.Second)
	for streamed := false; !streamed; {
		select {
		case evt := <-sub.copilot:
			streamed = evt.Type == "text_delta"
		case <-deadline:
			t.Fatal("timed out waiting for the first turn to stream")
		}
	}
	if resp := d.handleCopilotChat(chatData("second", "req-2")); resp.Error != "" {
		t.Fatalf("second chat rejected: %s", resp.Error)
	}

	msgs := waitForHistory(t, d, func(m []CopilotHistoryMsg) bool { return len(m) == 4 })
	find := func(role, substr string) *CopilotHistoryMsg {
		for i := range msgs {
			if msgs[i].Role == role && strings.Contains(msgs[i].Content, substr) {
				return &msgs[i]
			}
		}
		return nil
	}
	if find("user", "first") == nil || find("user", "second") == nil {
		t.Fatalf("both user messages must survive: %+v", msgs)
	}
	interrupted := find("copilot", "working on first")
	if interrupted == nil || !strings.Contains(interrupted.Content, "⚠ turn interrupted") {
		t.Fatalf("superseded turn should keep its partial reply with a marker: %+v", msgs)
	}
	if find("copilot", "second reply") == nil {
		t.Fatalf("superseding turn's reply missing: %+v", msgs)
	}
}

// Daemon shutdown flushes the in-flight turn's partial reply exactly once, and
// a late-waking turn goroutine cannot double-append behind it.
func TestFlushActiveCopilotTurnPersistsPartial(t *testing.T) {
	dir := overrideHistoryDir(t)
	d := &Daemon{}

	epoch := d.beginCopilotTurn("hello", "req-1", "client-A")
	d.updateCopilotTurn(epoch, CopilotStreamData{Type: "text_delta", Content: "half a "})
	d.updateCopilotTurn(epoch, CopilotStreamData{Type: "text_delta", Content: "reply"})

	d.flushActiveCopilotTurn()

	msgs := readHistoryFile(t, dir)
	if len(msgs) != 1 || msgs[0].Role != "copilot" {
		t.Fatalf("flush should persist exactly the copilot half: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "half a reply") || !strings.Contains(msgs[0].Content, "daemon shutdown") {
		t.Fatalf("flushed reply lost the partial text or the reason: %q", msgs[0].Content)
	}
	if d.isCurrentCopilotTurn(epoch) {
		t.Fatal("flush must clear the active turn")
	}
	if d.claimCopilotReply(epoch) {
		t.Fatal("a late goroutine must not be able to re-claim the flushed turn's reply")
	}

	// A second flush (or a flush with no active turn) appends nothing.
	d.flushActiveCopilotTurn()
	if again := readHistoryFile(t, dir); len(again) != 1 {
		t.Fatalf("repeat flush duplicated history: %+v", again)
	}
}
