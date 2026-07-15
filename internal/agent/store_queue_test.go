package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestQueueItemsRoundTrip(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	items := []QueueItem{
		{ID: NewQueueItemID(), Message: "one", ActionID: "act_1"},
		{ID: NewQueueItemID(), Message: "two"},
	}
	if err := store.WriteQueueItems("s1", items); err != nil {
		t.Fatal(err)
	}
	got := store.ReadQueueItems("s1")
	if !reflect.DeepEqual(got, items) {
		t.Fatalf("round trip = %#v, want %#v", got, items)
	}
	if msgs := store.ReadQueue("s1"); !reflect.DeepEqual(msgs, []string{"one", "two"}) {
		t.Fatalf("derived messages = %#v", msgs)
	}
	// Empty write removes the file.
	if err := store.WriteQueueItems("s1", nil); err != nil {
		t.Fatal(err)
	}
	if got := store.ReadQueueItems("s1"); got != nil {
		t.Fatalf("expected nil after removal, got %#v", got)
	}
}

func TestQueueItemsLegacyStringArrayUpgradesOnce(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	path := filepath.Join(dir, "s1.queue")
	if err := os.WriteFile(path, []byte(`["alpha","beta"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	first := store.ReadQueueItems("s1")
	if len(first) != 2 || first[0].Message != "alpha" || first[1].Message != "beta" {
		t.Fatalf("legacy read = %#v", first)
	}
	if first[0].ID == "" || first[1].ID == "" || first[0].ID == first[1].ID {
		t.Fatalf("legacy upgrade must mint distinct ids: %#v", first)
	}

	// The upgrade rewrites the file, so a second read returns the SAME ids —
	// stability across daemon restarts is the whole point.
	second := store.ReadQueueItems("s1")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("ids not stable across reads: %#v vs %#v", first, second)
	}
}

func TestQueueItemsLegacyPlainTextUpgrades(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	if err := os.WriteFile(filepath.Join(dir, "s1.queue"), []byte("just one message"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := store.ReadQueueItems("s1")
	if len(items) != 1 || items[0].Message != "just one message" || items[0].ID == "" {
		t.Fatalf("plain-text upgrade = %#v", items)
	}
	if again := store.ReadQueueItems("s1"); !reflect.DeepEqual(items, again) {
		t.Fatalf("ids not stable: %#v vs %#v", items, again)
	}
}
