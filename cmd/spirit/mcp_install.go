package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Global Hermes MCP registration. `spirit mcp install` promotes the per-session
// ACP registration (SpiritMCPServer at Lulu's session open) to a global entry in
// the active Hermes config, so ANY Hermes session gets Spirit's typed tools:
//
//	install [--force]  register mcp_servers.spirit = {command: <abs spirit exe>, args: [mcp]}
//	status             report whether the active config carries the expected registration
//
// This is a deliberate human act, never a `spirit setup` side effect. The config
// is comment-heavy, hand-tuned YAML — edits go through a yaml.Node round-trip so
// every other server and setting (and their comments) survive, and an
// already-expected registration never rewrites the file.

// hermesHome resolves the active Hermes home: $HERMES_HOME, else ~/.hermes.
func hermesHome() string {
	if home := os.Getenv("HERMES_HOME"); home != "" {
		return home
	}
	return filepath.Join(os.Getenv("HOME"), ".hermes")
}

// hermesConfigPath is the active Hermes config file.
func hermesConfigPath() string {
	return filepath.Join(hermesHome(), "config.yaml")
}

// spiritMCPEntry mirrors the shape of a Hermes stdio MCP server entry for
// comparison. Extra keys (env, timeout, ...) are user tuning and do not make an
// entry "different" — only command and args carry the registration's identity.
type spiritMCPEntry struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

func (e spiritMCPEntry) isExpected(exe string) bool {
	return e.Command == exe && len(e.Args) == 1 && e.Args[0] == "mcp"
}

// mcpConflictError reports an existing mcp_servers.spirit entry that differs
// from the expected registration; install refuses it without --force.
type mcpConflictError struct {
	entry spiritMCPEntry
}

func (e *mcpConflictError) Error() string {
	return fmt.Sprintf("mcp_servers.spirit already exists with command=%q args=%v; rerun with --force to replace only Spirit's entry",
		e.entry.Command, e.entry.Args)
}

// mcpRegState is the status verdict for the spirit registration.
type mcpRegState int

const (
	mcpRegMissing  mcpRegState = iota // no config, no mcp_servers, or no spirit entry
	mcpRegExpected                    // command+args match the resolved executable
	mcpRegDiffers                     // spirit entry exists but command/args differ
)

// installSpiritMCP registers mcp_servers.spirit in the YAML config at path.
// Returns whether the file was written. Expected registration already present →
// (false, nil) without touching the file. Differing entry → *mcpConflictError
// unless force, which replaces only Spirit's entry.
func installSpiritMCP(path, exe string, force bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	root, err := documentMapping(&doc)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}

	servers := mappingValue(root, "mcp_servers")
	switch {
	case servers == nil:
		servers = &yaml.Node{Kind: yaml.MappingNode}
		mappingSet(root, "mcp_servers", servers)
	case isNullNode(servers):
		// `mcp_servers:` with no value — turn the null scalar into a mapping in
		// place so the key node (and any comments on it) is untouched.
		*servers = yaml.Node{Kind: yaml.MappingNode}
	case servers.Kind != yaml.MappingNode:
		return false, fmt.Errorf("%s: mcp_servers is not a mapping", path)
	}

	if existing := mappingValue(servers, "spirit"); existing != nil {
		var cur spiritMCPEntry
		if err := existing.Decode(&cur); err != nil {
			return false, fmt.Errorf("%s: mcp_servers.spirit is malformed: %w", path, err)
		}
		if cur.isExpected(exe) {
			return false, nil // already registered — never rewrite the config
		}
		if !force {
			return false, &mcpConflictError{entry: cur}
		}
		*existing = *spiritMCPNode(exe) // replace only Spirit's entry
	} else {
		mappingSet(servers, "spirit", spiritMCPNode(exe))
	}

	out, err := marshalYAMLDoc(&doc)
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(path, out); err != nil {
		return false, err
	}
	return true, nil
}

// spiritMCPStatus reads the config at path and classifies the spirit
// registration against the resolved executable. The entry pointer is non-nil
// only for mcpRegDiffers, carrying what is actually registered.
func spiritMCPStatus(path, exe string) (mcpRegState, *spiritMCPEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return mcpRegMissing, nil, nil
	}
	if err != nil {
		return mcpRegMissing, nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return mcpRegMissing, nil, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return mcpRegMissing, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, err := documentMapping(&doc)
	if err != nil {
		return mcpRegMissing, nil, fmt.Errorf("%s: %w", path, err)
	}
	servers := mappingValue(root, "mcp_servers")
	if servers == nil || servers.Kind != yaml.MappingNode {
		return mcpRegMissing, nil, nil
	}
	entry := mappingValue(servers, "spirit")
	if entry == nil {
		return mcpRegMissing, nil, nil
	}
	var cur spiritMCPEntry
	if err := entry.Decode(&cur); err != nil {
		return mcpRegMissing, nil, fmt.Errorf("%s: mcp_servers.spirit is malformed: %w", path, err)
	}
	if cur.isExpected(exe) {
		return mcpRegExpected, nil, nil
	}
	return mcpRegDiffers, &cur, nil
}

// spiritMCPNode builds the expected mcp_servers.spirit value node.
func spiritMCPNode(exe string) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "command"},
		{Kind: yaml.ScalarNode, Value: exe},
		{Kind: yaml.ScalarNode, Value: "args"},
		{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "mcp"},
		}},
	}}
}

// documentMapping returns the top-level mapping of doc, synthesizing an empty
// document (missing/empty file) or upgrading a bare null document in place.
func documentMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 {
		m := &yaml.Node{Kind: yaml.MappingNode}
		*doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{m}}
		return m, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("unexpected YAML document structure")
	}
	root := doc.Content[0]
	if isNullNode(root) {
		*root = yaml.Node{Kind: yaml.MappingNode}
		return root, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("top level is not a mapping")
	}
	return root, nil
}

func isNullNode(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// mappingValue returns the value node for key in mapping m, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingSet replaces the value for key in mapping m, or appends the pair.
func mappingSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		val)
}

func marshalYAMLDoc(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFileAtomic writes via a same-directory temp file + rename so a crash
// mid-write can never leave the user's Hermes config truncated.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".spirit-mcp-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// --- CLI ---

// runMcpCmd dispatches `spirit mcp [install|status]`; bare `spirit mcp` stays
// the stdio JSON-RPC MCP server.
func runMcpCmd() {
	if len(os.Args) < 3 {
		runMcp()
		return
	}
	switch os.Args[2] {
	case "install":
		force := false
		for _, arg := range os.Args[3:] {
			if arg == "--force" {
				force = true
				continue
			}
			fmt.Fprintf(os.Stderr, "spirit mcp install: unknown flag %q\n", arg)
			os.Exit(1)
		}
		runMcpInstall(force)
	case "status":
		runMcpStatus()
	default:
		fmt.Fprintf(os.Stderr, "spirit mcp: unknown subcommand %q (install, status, or no args for the stdio server)\n", os.Args[2])
		os.Exit(1)
	}
}

// resolveSpiritExe resolves the absolute, symlink-free path to this binary —
// the same resolution `spirit setup` uses for hook commands.
func resolveSpiritExe() string {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
		os.Exit(1)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving executable path: %v\n", err)
		os.Exit(1)
	}
	return exe
}

func runMcpInstall(force bool) {
	exe := resolveSpiritExe()
	path := hermesConfigPath()
	changed, err := installSpiritMCP(path, exe, force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spirit mcp install: %v\n", err)
		os.Exit(1)
	}
	if changed {
		fmt.Printf("Registered mcp_servers.spirit in %s (%s mcp)\n", path, exe)
	} else {
		fmt.Printf("mcp_servers.spirit already registered in %s — config untouched\n", path)
	}
}

func runMcpStatus() {
	exe := resolveSpiritExe()
	path := hermesConfigPath()
	state, cur, err := spiritMCPStatus(path, exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spirit mcp status: %v\n", err)
		os.Exit(1)
	}
	switch state {
	case mcpRegExpected:
		fmt.Printf("ok: mcp_servers.spirit registered in %s (%s mcp)\n", path, exe)
	case mcpRegDiffers:
		fmt.Printf("differs: mcp_servers.spirit in %s has command=%q args=%v, expected %s [mcp]\nRun `spirit mcp install --force` to replace it.\n",
			path, cur.Command, cur.Args, exe)
		os.Exit(1)
	default:
		fmt.Printf("missing: no mcp_servers.spirit in %s\nRun `spirit mcp install` to register Spirit's MCP tools globally in Hermes.\n", path)
		os.Exit(1)
	}
}
