package claude

import (
	"fmt"
	"testing"
)

func TestAnimalIdxForProject_Deterministic(t *testing.T) {
	a := AnimalIdxForProject("eir")
	b := AnimalIdxForProject("eir")
	if a != b {
		t.Fatalf("expected deterministic mapping, got %d vs %d", a, b)
	}
	if a < 0 || a >= numAvatarAnimals {
		t.Fatalf("out of range: %d", a)
	}
}

func TestAnimalIdxForProject_EmptyReturnsSentinel(t *testing.T) {
	if got := AnimalIdxForProject(""); got != -1 {
		t.Fatalf("expected -1 sentinel for empty project, got %d", got)
	}
}

func TestAnimalIdxForProject_DifferentProjectsLikelyDiffer(t *testing.T) {
	// Not strictly required (23 buckets ⇒ collisions exist), but spot-check
	// that the hash doesn't collapse common project names to one animal.
	names := []string{"eir", "spirit", "lifeos", "fisheye-reader", "manim", "tana"}
	seen := make(map[int]int)
	for _, n := range names {
		seen[AnimalIdxForProject(n)]++
	}
	if len(seen) < 3 {
		t.Fatalf("hash distribution too clumpy: %v", seen)
	}
}

func TestPickColor_PrefersUnusedAmongSameAnimal(t *testing.T) {
	// Two active sessions on animal 5, colors 0 and 1.
	entries := map[string]Avatar{
		"s:a": {AnimalIdx: 5, ColorIdx: 0},
		"s:b": {AnimalIdx: 5, ColorIdx: 1},
	}
	activeKeys := map[string]bool{"s:a": true, "s:b": true}

	for range 100 {
		ci := pickColorForAnimal(entries, activeKeys, 5)
		if ci == 0 || ci == 1 {
			t.Fatalf("picked color %d already in use on animal 5", ci)
		}
	}
}

func TestPickColor_IgnoresOtherAnimals(t *testing.T) {
	// Active sessions on a different animal shouldn't constrain our pick.
	entries := map[string]Avatar{
		"s:a": {AnimalIdx: 3, ColorIdx: 0},
		"s:b": {AnimalIdx: 3, ColorIdx: 1},
		"s:c": {AnimalIdx: 3, ColorIdx: 2},
	}
	activeKeys := map[string]bool{"s:a": true, "s:b": true, "s:c": true}

	// Picking for animal 7 — colors 0/1/2 should be fair game.
	saw := make(map[int]bool)
	for range 200 {
		saw[pickColorForAnimal(entries, activeKeys, 7)] = true
	}
	for ci := range numAvatarColors {
		if !saw[ci] {
			t.Fatalf("color %d never picked for animal 7 — picker is over-constrained", ci)
		}
	}
}

func TestPickColor_FallbackWhenAllColorsTaken(t *testing.T) {
	// All 8 colors active on animal 2.
	entries := make(map[string]Avatar)
	activeKeys := make(map[string]bool)
	for ci := range numAvatarColors {
		key := fmt.Sprintf("s:%d", ci)
		entries[key] = Avatar{AnimalIdx: 2, ColorIdx: ci}
		activeKeys[key] = true
	}
	// Add 4 historical (inactive) entries on (2, 0) so it's the heavy one.
	for i := range 4 {
		key := fmt.Sprintf("hist:%d", i)
		entries[key] = Avatar{AnimalIdx: 2, ColorIdx: 0}
	}

	for range 200 {
		ci := pickColorForAnimal(entries, activeKeys, 2)
		if ci == 0 {
			t.Fatal("fallback picked heavily-assigned color 0 — should prefer least-assigned")
		}
	}
}

func TestGetOrAssignAvatar_SameProjectSameAnimal(t *testing.T) {
	resetAvStoreForTest()
	activeKeys := map[string]bool{"s:1": true, "s:2": true}

	a1 := GetOrAssignAvatar("s:1", "eir", activeKeys)
	a2 := GetOrAssignAvatar("s:2", "eir", activeKeys)

	if a1.AnimalIdx != a2.AnimalIdx {
		t.Fatalf("two sessions on project 'eir' should share an animal: %d vs %d", a1.AnimalIdx, a2.AnimalIdx)
	}
	if a1.ColorIdx == a2.ColorIdx {
		t.Fatalf("two active sessions on the same animal should get distinct colors, both got %d", a1.ColorIdx)
	}
}

func TestGetOrAssignAvatar_StableAcrossCalls(t *testing.T) {
	resetAvStoreForTest()
	activeKeys := map[string]bool{"s:1": true}

	a1 := GetOrAssignAvatar("s:1", "eir", activeKeys)
	a2 := GetOrAssignAvatar("s:1", "eir", activeKeys)

	if a1 != a2 {
		t.Fatalf("repeat lookup should return identical avatar: %+v vs %+v", a1, a2)
	}
}

func TestGetOrAssignAvatar_ReassignsColorWhenProjectChanges(t *testing.T) {
	resetAvStoreForTest()
	activeKeys := map[string]bool{"s:1": true}

	a1 := GetOrAssignAvatar("s:1", "eir", activeKeys)
	a2 := GetOrAssignAvatar("s:1", "spirit", activeKeys)

	// Animal must follow the new project.
	if a2.AnimalIdx != AnimalIdxForProject("spirit") {
		t.Fatalf("animal should follow new project; got %d, want %d", a2.AnimalIdx, AnimalIdxForProject("spirit"))
	}
	// And the old animal shouldn't linger if projects hash differently.
	if AnimalIdxForProject("eir") != AnimalIdxForProject("spirit") && a1.AnimalIdx == a2.AnimalIdx {
		t.Fatalf("expected animal to change with project")
	}
}

func TestGetOrAssignAvatar_EmptyProjectStableViaKey(t *testing.T) {
	resetAvStoreForTest()
	activeKeys := map[string]bool{"s:orphan": true}

	a1 := GetOrAssignAvatar("s:orphan", "", activeKeys)
	a2 := GetOrAssignAvatar("s:orphan", "", activeKeys)

	if a1.AnimalIdx != a2.AnimalIdx {
		t.Fatalf("orphan session animal should be stable across calls: %d vs %d", a1.AnimalIdx, a2.AnimalIdx)
	}
	if a1.AnimalIdx < 0 || a1.AnimalIdx >= numAvatarAnimals {
		t.Fatalf("animal index out of range: %d", a1.AnimalIdx)
	}
}

// resetAvStoreForTest clears the in-memory store so tests don't leak state.
// Background saveAvatarStore goroutines may race against the reset, but that
// only affects the on-disk file — tests read from avStore directly and never
// re-trigger loadAvatarStore, so in-memory state stays consistent.
func resetAvStoreForTest() {
	avStoreMu.Lock()
	defer avStoreMu.Unlock()
	avStore = avatarStore{Entries: make(map[string]Avatar)}
	// Mark loadAvatarStore as already-done so it won't clobber our reset.
	avStoreOnce.Do(func() {})
}
