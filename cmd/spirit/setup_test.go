package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/claude"
)

func TestInstallHooksPreservesCustomHooksAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	original := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "custom-notify"}}}}}}
	data, _ := json.Marshal(original)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	regs := []hookRegistration{{"Stop", ""}, {"PreToolUse", ""}}
	changed, err := installHooks(path, "/bin/spirit", claude.ProviderCodex, regs)
	if err != nil || !changed {
		t.Fatalf("first install changed=%v err=%v", changed, err)
	}
	changed, err = installHooks(path, "/bin/spirit", claude.ProviderCodex, regs)
	if err != nil || changed {
		t.Fatalf("second install changed=%v err=%v", changed, err)
	}
	installed, _ := os.ReadFile(path)
	for _, expected := range []string{"custom-notify", "--provider codex", hookMarker} {
		if !strings.Contains(string(installed), expected) {
			t.Fatalf("installed hooks missing %q: %s", expected, installed)
		}
	}
}
