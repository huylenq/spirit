package agent

import (
	"reflect"
	"testing"
)

func TestStoreSharedPersistence(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	if err := store.WriteSessionMeta(SessionMeta{Provider: ProviderCodex, SessionID: "s1", TranscriptPath: "/tmp/rollout"}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteSessionMeta(SessionMeta{SessionID: "s1", TurnID: "turn-2"}); err != nil {
		t.Fatal(err)
	}
	if got := store.ReadSessionMeta("s1"); got.Provider != ProviderCodex || got.TurnID != "turn-2" || got.TranscriptPath != "/tmp/rollout" {
		t.Fatalf("metadata merge = %+v", got)
	}
	if err := store.WriteQueue("s1", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if got := store.ReadQueue("s1"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("queue = %#v", got)
	}
	if err := store.WriteTags("s1", []string{"urgent", "review"}); err != nil {
		t.Fatal(err)
	}
	if got := store.ReadTags("s1"); !reflect.DeepEqual(got, []string{"urgent", "review"}) {
		t.Fatalf("tags = %#v", got)
	}
	if err := store.WriteNote("s1", " note "); err != nil {
		t.Fatal(err)
	}
	if got := store.ReadNote("s1"); got != "note" {
		t.Fatalf("note = %q", got)
	}
	record := LaterRecord{ID: GenerateLaterID(), Provider: ProviderCodex, SessionID: "s1"}
	if err := store.WriteLater(record); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadLater(record.ID)
	if err != nil || got.Provider != ProviderCodex || got.SessionID != "s1" {
		t.Fatalf("later = %+v, %v", got, err)
	}
}
