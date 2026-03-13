package search

import (
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var datedFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// TemporalDecayMultiplier returns exp(-lambda * ageDays) where lambda = ln(2) / halfLifeDays.
// Default half-life is 30 days.
func TemporalDecayMultiplier(fileDate time.Time, halfLifeDays float64) float64 {
	if halfLifeDays <= 0 {
		halfLifeDays = 30
	}
	lambda := math.Ln2 / halfLifeDays
	ageDays := time.Since(fileDate).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return math.Exp(-lambda * ageDays)
}

// IsEvergreenPath returns true if the path is MEMORY.md (case-insensitive)
// or a non-dated file under memory/. Dated files match pattern memory/YYYY-MM-DD*.md.
func IsEvergreenPath(path string) bool {
	base := filepath.Base(path)
	if strings.EqualFold(base, "MEMORY.md") {
		return true
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "memory" {
		// It's under memory/ — evergreen only if NOT dated
		return !datedFilePattern.MatchString(base)
	}
	return false
}

// ParseDateFromPath extracts a date from memory/YYYY-MM-DD.md or memory/YYYY-MM-DD-slug.md patterns.
// Returns zero time and false if no date found.
func ParseDateFromPath(path string) (time.Time, bool) {
	base := filepath.Base(path)
	if len(base) < 10 {
		return time.Time{}, false
	}
	dateStr := base[:10]
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
