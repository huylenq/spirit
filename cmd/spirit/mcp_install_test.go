package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const testExe = "/opt/spirit/bin/spirit"

func readConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func spiritEntryOf(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	servers, ok := cfg["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers missing or not a mapping: %#v", cfg["mcp_servers"])
	}
	entry, ok := servers["spirit"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers.spirit missing or not a mapping: %#v", servers["spirit"])
	}
	return entry
}

func assertExpectedEntry(t *testing.T, entry map[string]any) {
	t.Helper()
	if entry["command"] != testExe {
		t.Errorf("command = %#v, want %q", entry["command"], testExe)
	}
	args, ok := entry["args"].([]any)
	if !ok || len(args) != 1 || args[0] != "mcp" {
		t.Errorf("args = %#v, want [mcp]", entry["args"])
	}
}

// Fresh install: no config file at all — install creates it with just the
// spirit registration, and status flips from missing to expected.
func TestInstallSpiritMCPFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	state, _, err := spiritMCPStatus(path, testExe)
	if err != nil || state != mcpRegMissing {
		t.Fatalf("status before install = %v, %v; want missing", state, err)
	}

	changed, err := installSpiritMCP(path, testExe, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !changed {
		t.Error("fresh install reported unchanged")
	}
	assertExpectedEntry(t, spiritEntryOf(t, readConfig(t, path)))

	state, _, err = spiritMCPStatus(path, testExe)
	if err != nil || state != mcpRegExpected {
		t.Fatalf("status after install = %v, %v; want expected", state, err)
	}
}

// Idempotency: an expected registration must not rewrite the config — the
// second install reports unchanged and the file is byte-identical, even for a
// hand-written config whose formatting differs from what we would emit.
func TestInstallSpiritMCPIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	handWritten := "# my config\nmcp_servers:\n    spirit:\n        command: " + testExe + "\n        args:\n            - mcp\n"
	if err := os.WriteFile(path, []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := installSpiritMCP(path, testExe, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if changed {
		t.Error("install over an expected registration reported a write")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, []byte(handWritten)) {
		t.Errorf("config was rewritten:\n%s", after)
	}
}

// Other MCP servers, unrelated settings, and comments survive an install.
func TestInstallSpiritMCPPreservesOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `# hand-tuned hermes config
model:
  default: gpt-5.6-terra
# the time server matters
mcp_servers:
  time:
    command: uvx
    args: ["mcp-server-time"]
    timeout: 30
toolsets:
  - hermes-cli
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := installSpiritMCP(path, testExe, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !changed {
		t.Error("install reported unchanged")
	}

	cfg := readConfig(t, path)
	assertExpectedEntry(t, spiritEntryOf(t, cfg))
	servers := cfg["mcp_servers"].(map[string]any)
	timeEntry, ok := servers["time"].(map[string]any)
	if !ok || timeEntry["command"] != "uvx" || timeEntry["timeout"] != 30 {
		t.Errorf("time server not preserved: %#v", servers["time"])
	}
	model, ok := cfg["model"].(map[string]any)
	if !ok || model["default"] != "gpt-5.6-terra" {
		t.Errorf("model setting not preserved: %#v", cfg["model"])
	}
	toolsets, ok := cfg["toolsets"].([]any)
	if !ok || len(toolsets) != 1 || toolsets[0] != "hermes-cli" {
		t.Errorf("toolsets not preserved: %#v", cfg["toolsets"])
	}
	raw, _ := os.ReadFile(path)
	for _, comment := range []string{"# hand-tuned hermes config", "# the time server matters"} {
		if !strings.Contains(string(raw), comment) {
			t.Errorf("comment %q lost in rewrite:\n%s", comment, raw)
		}
	}
}

// An empty `mcp_servers:` key (null value) is upgraded in place.
func TestInstallSpiritMCPEmptyServersKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("model:\n  default: gpt-5.6-terra\nmcp_servers:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installSpiritMCP(path, testExe, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	assertExpectedEntry(t, spiritEntryOf(t, readConfig(t, path)))
}

// A differing spirit entry is refused without --force, and the file is untouched.
func TestInstallSpiritMCPConflictRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "mcp_servers:\n  spirit:\n    command: /old/spirit\n    args: [mcp, --legacy]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := installSpiritMCP(path, testExe, false)
	if changed {
		t.Error("conflicting install reported a write")
	}
	var conflict *mcpConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want *mcpConflictError", err)
	}
	if conflict.entry.Command != "/old/spirit" {
		t.Errorf("conflict entry command = %q", conflict.entry.Command)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, []byte(content)) {
		t.Errorf("config modified despite refusal:\n%s", after)
	}
}

// --force replaces only Spirit's entry; siblings survive.
func TestInstallSpiritMCPForceReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "mcp_servers:\n  time:\n    command: uvx\n    args: [mcp-server-time]\n  spirit:\n    command: /old/spirit\n    args: [mcp, --legacy]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := installSpiritMCP(path, testExe, true)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}
	if !changed {
		t.Error("force install reported unchanged")
	}
	cfg := readConfig(t, path)
	assertExpectedEntry(t, spiritEntryOf(t, cfg))
	servers := cfg["mcp_servers"].(map[string]any)
	timeEntry, ok := servers["time"].(map[string]any)
	if !ok || timeEntry["command"] != "uvx" {
		t.Errorf("time server not preserved: %#v", servers["time"])
	}

	state, _, err := spiritMCPStatus(path, testExe)
	if err != nil || state != mcpRegExpected {
		t.Fatalf("status after force = %v, %v; want expected", state, err)
	}
}

// Status classifies expected / differs / missing.
func TestSpiritMCPStatus(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "absent.yaml")
	if state, _, err := spiritMCPStatus(missing, testExe); err != nil || state != mcpRegMissing {
		t.Errorf("absent file: state = %v, %v; want missing", state, err)
	}

	noServers := filepath.Join(dir, "noservers.yaml")
	os.WriteFile(noServers, []byte("model:\n  default: gpt-5.6-terra\n"), 0o644)
	if state, _, err := spiritMCPStatus(noServers, testExe); err != nil || state != mcpRegMissing {
		t.Errorf("no mcp_servers: state = %v, %v; want missing", state, err)
	}

	differs := filepath.Join(dir, "differs.yaml")
	os.WriteFile(differs, []byte("mcp_servers:\n  spirit:\n    command: /old/spirit\n    args: [mcp]\n"), 0o644)
	state, cur, err := spiritMCPStatus(differs, testExe)
	if err != nil || state != mcpRegDiffers {
		t.Fatalf("differing entry: state = %v, %v; want differs", state, err)
	}
	if cur == nil || cur.Command != "/old/spirit" {
		t.Errorf("differing entry not reported: %#v", cur)
	}

	expected := filepath.Join(dir, "expected.yaml")
	os.WriteFile(expected, []byte("mcp_servers:\n  spirit:\n    command: "+testExe+"\n    args: [mcp]\n"), 0o644)
	if state, _, err := spiritMCPStatus(expected, testExe); err != nil || state != mcpRegExpected {
		t.Errorf("expected entry: state = %v, %v; want expected", state, err)
	}
}

// Extra per-server tuning (env, timeout) does not make the entry "different":
// command+args are the registration's identity.
func TestSpiritMCPStatusIgnoresExtraKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "mcp_servers:\n  spirit:\n    command: " + testExe + "\n    args: [mcp]\n    timeout: 60\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, _, err := spiritMCPStatus(path, testExe); err != nil || state != mcpRegExpected {
		t.Errorf("state = %v, %v; want expected", state, err)
	}
	if changed, err := installSpiritMCP(path, testExe, false); err != nil || changed {
		t.Errorf("install = %v, %v; want unchanged no-op", changed, err)
	}
}

// The active config follows $HERMES_HOME, falling back to ~/.hermes.
func TestHermesConfigPath(t *testing.T) {
	t.Setenv("HERMES_HOME", "/custom/hermes")
	if got := hermesConfigPath(); got != "/custom/hermes/config.yaml" {
		t.Errorf("with HERMES_HOME: %q", got)
	}
	t.Setenv("HERMES_HOME", "")
	want := filepath.Join(os.Getenv("HOME"), ".hermes", "config.yaml")
	if got := hermesConfigPath(); got != want {
		t.Errorf("fallback: %q, want %q", got, want)
	}
}
