package ui

import "testing"

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		text    string
		pattern string
		want    bool
		desc    string
	}{
		// Should match: exact and substring
		{"fix the bug", "fix", true, "exact prefix"},
		{"the fix is in", "fix", true, "word boundary substring"},
		{"daemon", "daemon", true, "exact match"},
		{"spirit-animal", "sa", true, "word initials"},
		{"CamelCase", "cc", true, "camelCase initials"},
		{"bug_fix_parser", "bfp", true, "underscore-separated initials"},

		// Should match: consecutive chars at word boundary
		{"we need to fix bugs", "fix", true, "word in middle"},
		{"session title with debug info", "debug", true, "word deep in text"},

		// Word-initials matching (Sublime Text-style feature)
		{"something totally unrelated maybe", "stum", true, "word initials across words"},
		{"add basic controllers", "abc", true, "word initials a.b.c."},

		// Should reject: scattered with no word-boundary or consecutive bonus
		{"xaxxbxxcxxxxxxxxxxxxxxxx", "abc", false, "scattered mid-text no boundaries"},
		{"qqxqqyqqz", "xyz", false, "scattered mid-text no bonuses"},

		// Edge cases
		{"", "abc", false, "empty text"},
		{"abc", "", true, "empty pattern"},
		{"", "", true, "both empty"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, got := fuzzyMatch(tt.text, tt.pattern)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.text, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestMatchesNarrow(t *testing.T) {
	tests := []struct {
		text  string
		query string
		want  bool
		desc  string
	}{
		{"Fix the Bug", "fix", true, "case insensitive"},
		{"DAEMON_CONFIG", "dc", true, "uppercase word initials"},
		{"qqxqqyqqzqq", "xyz", false, "scattered no bonuses"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := matchesNarrow(tt.text, tt.query); got != tt.want {
				t.Errorf("matchesNarrow(%q, %q) = %v, want %v", tt.text, tt.query, got, tt.want)
			}
		})
	}
}
