package main

import (
	"testing"
	"time"
)

func TestReactiveDigestDue(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 7, 15, h, m, 0, 0, time.Local) }
	cases := []struct {
		name     string
		now      time.Time
		digestAt string
		lastDay  string
		want     bool
	}{
		{"unset never fires", at(12, 0), "", "", false},
		{"malformed never fires", at(12, 0), "nope", "", false},
		{"before configured time", at(8, 59), "09:00", "", false},
		{"at configured time", at(9, 0), "09:00", "", true},
		{"after configured time", at(11, 0), "09:00", "", true},
		{"already fired today", at(11, 0), "09:00", "2026-07-15", false},
		{"fired yesterday, due again", at(11, 0), "09:00", "2026-07-14", true},
	}
	for _, c := range cases {
		if got := reactiveDigestDue(c.now, c.digestAt, c.lastDay); got != c.want {
			t.Errorf("%s: reactiveDigestDue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseHHMMField(t *testing.T) {
	for _, c := range []struct {
		s    string
		mins int
		ok   bool
	}{
		{"09:00", 540, true},
		{"23:59", 1439, true},
		{"00:00", 0, true},
		{"24:00", 0, false},
		{"09:60", 0, false},
		{"", 0, false},
		{"9", 0, false},
	} {
		mins, ok := parseHHMMField(c.s)
		if ok != c.ok || (ok && mins != c.mins) {
			t.Errorf("parseHHMMField(%q) = (%d,%v), want (%d,%v)", c.s, mins, ok, c.mins, c.ok)
		}
	}
}
