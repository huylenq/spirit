package claude

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	dayRe       = regexp.MustCompile(`\d+d`)
	unitlessRe  = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// ParseWaitDuration parses a duration string with optional day units (e.g. "1d", "2d12h").
// Bare numbers (e.g. "15") default to minutes. Go's time.ParseDuration stops at hours,
// so day tokens are expanded before parsing.
func ParseWaitDuration(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if unitlessRe.MatchString(trimmed) {
		trimmed += "m"
	}
	expanded := dayRe.ReplaceAllStringFunc(trimmed, func(match string) string {
		n, _ := strconv.Atoi(strings.TrimSuffix(match, "d"))
		return strconv.Itoa(n*24) + "h"
	})
	return time.ParseDuration(expanded)
}
