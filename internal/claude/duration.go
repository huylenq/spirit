package claude

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var dayRe = regexp.MustCompile(`\d+d`)

// ParseWaitDuration parses a duration string with optional day units (e.g. "1d", "2d12h").
// Go's time.ParseDuration stops at hours, so day tokens are expanded before parsing.
func ParseWaitDuration(s string) (time.Duration, error) {
	expanded := dayRe.ReplaceAllStringFunc(s, func(match string) string {
		n, _ := strconv.Atoi(strings.TrimSuffix(match, "d"))
		return strconv.Itoa(n*24) + "h"
	})
	return time.ParseDuration(expanded)
}
