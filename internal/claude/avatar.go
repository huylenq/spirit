package claude

import (
	"encoding/json"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Avatar holds the assigned animal and color indices for a session.
// AnimalIdx is derived from the session's project name (so all sessions on
// the same project share an animal). ColorIdx is per-session and chosen to
// be unique among active sessions sharing the same animal.
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
	return filepath.Join(home, ".cache", "spirit", "avatars.json")
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
	os.WriteFile(path, data, 0o644)        //nolint:errcheck
}

// hashStr returns an FNV-1a 32-bit hash of s.
func hashStr(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// AnimalIdxForProject returns the deterministic animal index for a project.
// Returns -1 if project is empty so the caller can fall back to a
// session-stable hash (e.g. session/pane key).
func AnimalIdxForProject(project string) int {
	if project == "" {
		return -1
	}
	return int(hashStr(project) % uint32(numAvatarAnimals))
}

// pickColorForAnimal selects a color for a session whose animal is animalIdx.
// Prefers a color unused among currently-active sessions sharing the same
// animal; falls back to the least-assigned color across the entire store
// when all 8 are taken on this animal.
func pickColorForAnimal(entries map[string]Avatar, activeKeys map[string]bool, animalIdx int) int {
	used := make(map[int]bool)
	for k, a := range entries {
		if activeKeys[k] && a.AnimalIdx == animalIdx {
			used[a.ColorIdx] = true
		}
	}
	var available []int
	for ci := range numAvatarColors {
		if !used[ci] {
			available = append(available, ci)
		}
	}
	if len(available) > 0 {
		return available[rand.IntN(len(available))]
	}

	// All 8 colors active on this animal — pick least-assigned across all
	// historical entries (active or not) for this animal.
	counts := make(map[int]int)
	for _, a := range entries {
		if a.AnimalIdx == animalIdx {
			counts[a.ColorIdx]++
		}
	}
	minCount := -1
	var subset []int
	for ci := range numAvatarColors {
		c := counts[ci]
		if minCount < 0 || c < minCount {
			minCount = c
			subset = []int{ci}
		} else if c == minCount {
			subset = append(subset, ci)
		}
	}
	return subset[rand.IntN(len(subset))]
}

// assignAvatarLocked picks the avatar for (key, project). Caller must hold
// avStoreMu. Returns the avatar and whether the store was mutated.
func assignAvatarLocked(key, project string, activeKeys map[string]bool) (Avatar, bool) {
	animalIdx := AnimalIdxForProject(project)
	if animalIdx < 0 {
		// No project — keep the session's animal stable by hashing its key.
		animalIdx = int(hashStr(key) % uint32(numAvatarAnimals))
	}

	if a, ok := avStore.Entries[key]; ok && a.AnimalIdx == animalIdx {
		return a, false
	}

	colorIdx := pickColorForAnimal(avStore.Entries, activeKeys, animalIdx)
	a := Avatar{AnimalIdx: animalIdx, ColorIdx: colorIdx, Seq: avStore.Counter}
	avStore.Counter++
	avStore.Entries[key] = a
	return a, true
}

// scheduleSave kicks off a background save with a snapshot of activeKeys.
// Caller must hold avStoreMu.
func scheduleSave(activeKeys map[string]bool) {
	activeKeysCopy := make(map[string]bool, len(activeKeys))
	for k, v := range activeKeys {
		activeKeysCopy[k] = v
	}
	go saveAvatarStore(activeKeysCopy)
}

// GetOrAssignAvatar returns the avatar for (key, project). The animal is
// derived from project (deterministic). If a stored entry exists and its
// animal still matches, its color is reused; otherwise a fresh color is
// picked, preferring colors unused among active sessions sharing the same
// animal.
func GetOrAssignAvatar(key, project string, activeKeys map[string]bool) Avatar {
	avStoreOnce.Do(loadAvatarStore)

	avStoreMu.Lock()
	defer avStoreMu.Unlock()

	a, dirty := assignAvatarLocked(key, project, activeKeys)
	if dirty {
		scheduleSave(activeKeys)
	}
	return a
}

// AssignAvatars assigns avatar indices to all sessions in-place. Holds the
// store mutex once across the whole batch and triggers at most one save.
func AssignAvatars(sessions []ClaudeSession) {
	avStoreOnce.Do(loadAvatarStore)

	avStoreMu.Lock()
	defer avStoreMu.Unlock()

	activeKeys := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		activeKeys[avatarKey(s)] = true
	}
	dirty := false
	for i := range sessions {
		key := avatarKey(sessions[i])
		a, d := assignAvatarLocked(key, sessions[i].Project, activeKeys)
		if d {
			dirty = true
		}
		sessions[i].AvatarAnimalIdx = a.AnimalIdx
		sessions[i].AvatarColorIdx = a.ColorIdx
	}
	if dirty {
		scheduleSave(activeKeys)
	}
}

func avatarKey(s ClaudeSession) string {
	if s.SessionID != "" {
		return "s:" + s.SessionID
	}
	return "p:" + s.PaneID
}
