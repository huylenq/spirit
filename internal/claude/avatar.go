package claude

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Avatar holds the assigned animal and color indices for a session.
type Avatar struct {
	AnimalIdx int `json:"animalIdx"`
	ColorIdx  int `json:"colorIdx"`
	Seq       int `json:"seq"` // insertion order for max-size eviction
}

type avatarStore struct {
	Entries map[string]Avatar `json:"entries"`
	Counter int               `json:"counter"`
}

const (
	numAvatarAnimals = 23
	numAvatarColors  = 8
	maxAvatarEntries = 500
)

var (
	avStore     avatarStore
	avStoreOnce sync.Once
	avStoreMu   sync.Mutex
)

func avatarFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "cmc", "avatars.json")
}

func loadAvatarStore() {
	data, err := os.ReadFile(avatarFilePath())
	if err != nil {
		avStore = avatarStore{Entries: make(map[string]Avatar)}
		return
	}
	if err := json.Unmarshal(data, &avStore); err != nil {
		avStore = avatarStore{Entries: make(map[string]Avatar)}
		return
	}
	if avStore.Entries == nil {
		avStore.Entries = make(map[string]Avatar)
	}
}

func saveAvatarStore(activeKeys map[string]bool) {
	avStoreMu.Lock()
	defer avStoreMu.Unlock()

	if len(avStore.Entries) > maxAvatarEntries {
		for k := range avStore.Entries {
			if !activeKeys[k] {
				delete(avStore.Entries, k)
			}
		}
	}
	if len(avStore.Entries) > maxAvatarEntries {
		type kv struct {
			k   string
			seq int
		}
		entries := make([]kv, 0, len(avStore.Entries))
		for k, v := range avStore.Entries {
			entries = append(entries, kv{k, v.Seq})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })
		for _, e := range entries {
			if len(avStore.Entries) <= maxAvatarEntries {
				break
			}
			delete(avStore.Entries, e.k)
		}
	}

	data, err := json.Marshal(avStore)
	if err != nil {
		return
	}
	path := avatarFilePath()
	os.MkdirAll(filepath.Dir(path), 0o755) //nolint:errcheck
	os.WriteFile(path, data, 0o644)         //nolint:errcheck
}

// pickAvatarPair selects an (animal, color) pair for avatar assignment.
// Tier 1: pairs with an unused animal (animal uniqueness prioritized).
// Tier 2: pairs with a used animal but unused (animal, color) combo.
// Tier 3: all 184 pairs exhausted — pick from least-assigned pairs across all entries.
func pickAvatarPair(entries map[string]Avatar, activeKeys map[string]bool) (int, int) {
	type pair = [2]int

	usedAnimals := make(map[int]bool)
	usedPairs := make(map[pair]bool)
	for k, a := range entries {
		if activeKeys[k] {
			usedAnimals[a.AnimalIdx] = true
			usedPairs[pair{a.AnimalIdx, a.ColorIdx}] = true
		}
	}

	var tier1, tier2 []pair
	for ai := range numAvatarAnimals {
		for ci := range numAvatarColors {
			p := pair{ai, ci}
			if usedPairs[p] {
				continue
			}
			if !usedAnimals[ai] {
				tier1 = append(tier1, p)
			} else {
				tier2 = append(tier2, p)
			}
		}
	}

	if len(tier1) > 0 {
		p := tier1[rand.IntN(len(tier1))]
		return p[0], p[1]
	}
	if len(tier2) > 0 {
		p := tier2[rand.IntN(len(tier2))]
		return p[0], p[1]
	}

	// All pairs in use — pick from least-assigned across all store entries.
	counts := make(map[pair]int)
	for _, a := range entries {
		counts[pair{a.AnimalIdx, a.ColorIdx}]++
	}
	minCount := -1
	var subset []pair
	for ai := range numAvatarAnimals {
		for ci := range numAvatarColors {
			c := counts[pair{ai, ci}]
			if minCount < 0 || c < minCount {
				minCount = c
				subset = []pair{{ai, ci}}
			} else if c == minCount {
				subset = append(subset, pair{ai, ci})
			}
		}
	}
	p := subset[rand.IntN(len(subset))]
	return p[0], p[1]
}

// GetOrAssignAvatar returns the existing avatar for key, or assigns a new one.
// activeKeys is used to avoid reusing (animal, color) pairs already visible.
func GetOrAssignAvatar(key string, activeKeys map[string]bool) Avatar {
	avStoreOnce.Do(loadAvatarStore)

	avStoreMu.Lock()
	defer avStoreMu.Unlock()

	if a, ok := avStore.Entries[key]; ok {
		return a
	}

	animalIdx, colorIdx := pickAvatarPair(avStore.Entries, activeKeys)

	a := Avatar{AnimalIdx: animalIdx, ColorIdx: colorIdx, Seq: avStore.Counter}
	avStore.Counter++
	avStore.Entries[key] = a

	activeKeysCopy := make(map[string]bool, len(activeKeys))
	for k, v := range activeKeys {
		activeKeysCopy[k] = v
	}
	go saveAvatarStore(activeKeysCopy)

	return a
}

// AssignAvatars assigns avatar indices to all sessions in-place.
func AssignAvatars(sessions []ClaudeSession) {
	activeKeys := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		activeKeys[avatarKey(s)] = true
	}
	for i := range sessions {
		key := avatarKey(sessions[i])
		a := GetOrAssignAvatar(key, activeKeys)
		sessions[i].AvatarAnimalIdx = a.AnimalIdx
		sessions[i].AvatarColorIdx = a.ColorIdx
	}
}

func avatarKey(s ClaudeSession) string {
	if s.SessionID != "" {
		return "s:" + s.SessionID
	}
	return "p:" + s.PaneID
}
