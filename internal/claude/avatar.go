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

// pickIdx selects an index in [0, n) for avatar assignment.
// Prefers a random absent index (not currently used by active sessions).
// Falls back to a random index from the least-assigned subset across all store entries.
func pickIdx(n int, used map[int]bool, entries map[string]Avatar, field func(Avatar) int) int {
	absent := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if !used[i] {
			absent = append(absent, i)
		}
	}
	if len(absent) > 0 {
		return absent[rand.IntN(len(absent))]
	}
	counts := make([]int, n)
	for _, a := range entries {
		idx := field(a)
		if idx >= 0 && idx < n {
			counts[idx]++
		}
	}
	minCount := counts[0]
	for _, c := range counts {
		if c < minCount {
			minCount = c
		}
	}
	subset := make([]int, 0, n)
	for i, c := range counts {
		if c == minCount {
			subset = append(subset, i)
		}
	}
	return subset[rand.IntN(len(subset))]
}

// GetOrAssignAvatar returns the existing avatar for key, or assigns a new one.
// activeKeys is used to avoid reusing animals/colors already visible.
func GetOrAssignAvatar(key string, activeKeys map[string]bool) Avatar {
	avStoreOnce.Do(loadAvatarStore)

	avStoreMu.Lock()
	defer avStoreMu.Unlock()

	if a, ok := avStore.Entries[key]; ok {
		return a
	}

	usedAnimals := make(map[int]bool)
	usedColors := make(map[int]bool)
	for k, a := range avStore.Entries {
		if activeKeys[k] {
			usedAnimals[a.AnimalIdx] = true
			usedColors[a.ColorIdx] = true
		}
	}

	animalIdx := pickIdx(numAvatarAnimals, usedAnimals, avStore.Entries, func(a Avatar) int { return a.AnimalIdx })
	colorIdx := pickIdx(numAvatarColors, usedColors, avStore.Entries, func(a Avatar) int { return a.ColorIdx })

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
