package claude

import (
	"encoding/json"
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

	animalIdx := avStore.Counter % numAvatarAnimals
	for usedAnimals[animalIdx] && len(usedAnimals) < numAvatarAnimals {
		animalIdx = (animalIdx + 1) % numAvatarAnimals
	}

	colorIdx := avStore.Counter % numAvatarColors
	for usedColors[colorIdx] && len(usedColors) < numAvatarColors {
		colorIdx = (colorIdx + 1) % numAvatarColors
	}

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
