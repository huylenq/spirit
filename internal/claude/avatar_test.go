package claude

import (
	"fmt"
	"testing"
)

func TestPickAvatarPair_EmptyState(t *testing.T) {
	entries := map[string]Avatar{}
	activeKeys := map[string]bool{}

	ai, ci := pickAvatarPair(entries, activeKeys)
	if ai < 0 || ai >= numAvatarAnimals || ci < 0 || ci >= numAvatarColors {
		t.Fatalf("out of range: animal=%d color=%d", ai, ci)
	}
}

func TestPickAvatarPair_Tier1_PrefersUnusedAnimal(t *testing.T) {
	// One active session using animal 0.
	entries := map[string]Avatar{
		"s:a": {AnimalIdx: 0, ColorIdx: 0},
	}
	activeKeys := map[string]bool{"s:a": true}

	for range 100 {
		ai, _ := pickAvatarPair(entries, activeKeys)
		if ai == 0 {
			t.Fatal("picked animal 0 which is already active — should prefer unused animals (tier 1)")
		}
	}
}

func TestPickAvatarPair_Tier2_UnusedPairWithUsedAnimal(t *testing.T) {
	// All 23 animals used, each with color 0. Unused pairs still exist (e.g. animal 0 + color 1).
	entries := make(map[string]Avatar)
	activeKeys := make(map[string]bool)
	for i := range numAvatarAnimals {
		key := fmt.Sprintf("s:%d", i)
		entries[key] = Avatar{AnimalIdx: i, ColorIdx: 0}
		activeKeys[key] = true
	}

	for range 100 {
		ai, ci := pickAvatarPair(entries, activeKeys)
		if ci == 0 {
			// The pair (ai, 0) is already active for every animal — should pick a different color.
			t.Fatalf("picked used pair (%d, 0)", ai)
		}
	}
}

func TestPickAvatarPair_Tier3_AllPairsExhausted(t *testing.T) {
	// Fill all 184 pairs as active.
	entries := make(map[string]Avatar)
	activeKeys := make(map[string]bool)
	for ai := range numAvatarAnimals {
		for ci := range numAvatarColors {
			key := fmt.Sprintf("s:%d-%d", ai, ci)
			entries[key] = Avatar{AnimalIdx: ai, ColorIdx: ci}
			activeKeys[key] = true
		}
	}

	ai, ci := pickAvatarPair(entries, activeKeys)
	if ai < 0 || ai >= numAvatarAnimals || ci < 0 || ci >= numAvatarColors {
		t.Fatalf("out of range: animal=%d color=%d", ai, ci)
	}
}

func TestPickAvatarPair_Tier3_LeastAssigned(t *testing.T) {
	// All pairs active. One pair assigned 5 times, the rest once.
	// Should never pick the heavily-assigned pair.
	entries := make(map[string]Avatar)
	activeKeys := make(map[string]bool)
	for ai := range numAvatarAnimals {
		for ci := range numAvatarColors {
			key := fmt.Sprintf("s:%d-%d", ai, ci)
			entries[key] = Avatar{AnimalIdx: ai, ColorIdx: ci}
			activeKeys[key] = true
		}
	}
	// Add 4 extra entries for pair (0,0) — total count 5 vs 1 for others.
	for i := range 4 {
		key := fmt.Sprintf("extra:%d", i)
		entries[key] = Avatar{AnimalIdx: 0, ColorIdx: 0}
	}

	for range 200 {
		ai, ci := pickAvatarPair(entries, activeKeys)
		if ai == 0 && ci == 0 {
			t.Fatal("picked heavily-assigned pair (0,0) — should prefer least-assigned")
		}
	}
}

func TestPickAvatarPair_NoDuplicatePairs(t *testing.T) {
	// Assign 20 sessions sequentially, verify no duplicate (animal, color) pairs.
	entries := make(map[string]Avatar)
	activeKeys := make(map[string]bool)
	type pair = [2]int
	seen := make(map[pair]bool)

	for i := range 20 {
		key := fmt.Sprintf("s:%d", i)
		activeKeys[key] = true
		ai, ci := pickAvatarPair(entries, activeKeys)
		p := pair{ai, ci}
		if seen[p] {
			t.Fatalf("duplicate pair %v at session %d", p, i)
		}
		seen[p] = true
		entries[key] = Avatar{AnimalIdx: ai, ColorIdx: ci}
	}
}

func TestPickAvatarPair_AnimalPriorityOverColor(t *testing.T) {
	// 3 active sessions each with a distinct animal. A new session should get a 4th animal,
	// not reuse an existing animal with a different color.
	entries := map[string]Avatar{
		"s:0": {AnimalIdx: 0, ColorIdx: 0},
		"s:1": {AnimalIdx: 1, ColorIdx: 1},
		"s:2": {AnimalIdx: 2, ColorIdx: 2},
	}
	activeKeys := map[string]bool{"s:0": true, "s:1": true, "s:2": true}

	usedAnimals := map[int]bool{0: true, 1: true, 2: true}
	for range 100 {
		ai, _ := pickAvatarPair(entries, activeKeys)
		if usedAnimals[ai] {
			t.Fatalf("reused active animal %d when unused animals are available", ai)
		}
	}
}
