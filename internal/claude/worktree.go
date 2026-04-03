package claude

import (
	"math/rand/v2"
	"os"
	"path/filepath"

	"github.com/huylenq/spirit/internal/spirit"
)

const (
	numColors  = len(spirit.Adjectives)
	numAnimals = len(spirit.Animals)
	totalNames = numColors * numAnimals // 184
)

// GenerateWorktreeName picks a random spirit name (e.g. "ember-cat") that doesn't
// collide with existing worktree directories under <repoDir>/.claude/worktrees/.
func GenerateWorktreeName(repoDir string) string {
	existing := existingWorktreeNames(repoDir)

	for range 50 { // 184 possible names — collision is rare, 50 tries is plenty
		ci := rand.IntN(numColors)
		ai := rand.IntN(numAnimals)
		name := spirit.WorktreeName(ci, ai)
		if !existing[name] {
			return name
		}
	}
	// Exhaustive fallback (shouldn't happen in practice)
	for ci := range numColors {
		for ai := range numAnimals {
			name := spirit.WorktreeName(ci, ai)
			if !existing[name] {
				return name
			}
		}
	}
	return spirit.WorktreeName(rand.IntN(numColors), rand.IntN(numAnimals))
}

func existingWorktreeNames(repoDir string) map[string]bool {
	dir := filepath.Join(repoDir, ".claude", "worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names[e.Name()] = true
		}
	}
	return names
}
