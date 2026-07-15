// Package runbook promotes Lua macros into named, parameterized, inspectable
// runbooks (W8, spec Decision 4). A runbook is a durable Lua definition with a
// metadata header (description, params, declared action classes) whose BUILD
// phase computes a batch of steps — it never executes side effects itself; the
// emitted batch rides the same plan/action pipeline as every other surface
// (one approval, per-action receipts, no second execution path).
//
// This package owns the file store and metadata; the build-phase Lua VM lives
// in internal/scripting (BuildRunbookSteps).
package runbook

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed builtin/*.lua
var builtinFS embed.FS

// Param is one declared runbook parameter. A trailing "!" on the name in the
// header marks it required; required params are enforced before the build
// phase runs (fail-fast).
type Param struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// Runbook is a named, parameterized batch-emitting definition.
type Runbook struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Params      []Param `json:"params,omitempty"`
	// Actions is the header-declared set of side-effect ops the runbook may
	// emit (e.g. "queue", "kill"). The build phase is checked against it:
	// emitting an undeclared side-effect op is a validation error. "wait" is
	// read-only and never needs declaring. This is the honest basis for
	// surfaces to mark a runbook destructive BEFORE running anything.
	Actions []string `json:"actions,omitempty"`
	Script  string   `json:"-"`
	Path    string   `json:"path,omitempty"`
	BuiltIn bool     `json:"builtin,omitempty"`
}

// Destructive reports whether the runbook declares any destructive action
// class (kill; later/commit are conditionally destructive, so a declaration of
// them counts — the plan preview shows the actual per-step risk).
func (r Runbook) Destructive() bool {
	for _, a := range r.Actions {
		switch a {
		case "kill", "later", "commit":
			return true
		}
	}
	return false
}

// dirOverride lets tests point the user runbook dir at a temp dir.
var (
	dirMu       sync.Mutex
	dirOverride string
)

// Dir returns the user runbook directory (~/.spirit/runbooks).
func Dir() string {
	dirMu.Lock()
	defer dirMu.Unlock()
	if dirOverride != "" {
		return dirOverride
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".spirit", "runbooks")
}

// OverrideDirForTest points Dir at dir and returns a restore func.
func OverrideDirForTest(dir string) func() {
	dirMu.Lock()
	prev := dirOverride
	dirOverride = dir
	dirMu.Unlock()
	return func() {
		dirMu.Lock()
		dirOverride = prev
		dirMu.Unlock()
	}
}

// ParseHeader extracts runbook metadata from leading "-- key: value" comment
// lines. Recognized keys: name, description, param (repeatable, "name!" marks
// required), actions (comma-separated ops). Parsing stops at the first
// non-comment line.
func ParseHeader(script string) (name, description string, params []Param, actions []string) {
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "--") {
			break
		}
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))
		key, value, ok := strings.Cut(body, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		case "param":
			pname, desc, _ := strings.Cut(value, " ")
			p := Param{Name: pname, Description: strings.TrimSpace(desc)}
			if strings.HasSuffix(p.Name, "!") {
				p.Name = strings.TrimSuffix(p.Name, "!")
				p.Required = true
			}
			if p.Name != "" {
				params = append(params, p)
			}
		case "actions":
			for _, a := range strings.Split(value, ",") {
				if a = strings.TrimSpace(a); a != "" {
					actions = append(actions, a)
				}
			}
		}
	}
	return name, description, params, actions
}

func fromScript(fileStem, script, path string, builtIn bool) Runbook {
	name, desc, params, actions := ParseHeader(script)
	if name == "" {
		name = fileStem
	}
	return Runbook{
		Name:        name,
		Description: desc,
		Params:      params,
		Actions:     actions,
		Script:      script,
		Path:        path,
		BuiltIn:     builtIn,
	}
}

// Builtins returns the runbooks embedded in the binary.
func Builtins() []Runbook {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil
	}
	out := make([]Runbook, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
			continue
		}
		data, err := builtinFS.ReadFile("builtin/" + e.Name())
		if err != nil {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".lua")
		out = append(out, fromScript(stem, string(data), "builtin:"+e.Name(), true))
	}
	return out
}

// List returns all known runbooks, user definitions overriding builtins with
// the same name, sorted by name. Unreadable files are skipped (fail-soft for
// listing; Load is the fail-fast path).
func List() []Runbook {
	byName := make(map[string]Runbook)
	for _, rb := range Builtins() {
		byName[rb.Name] = rb
	}
	if entries, err := os.ReadDir(Dir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
				continue
			}
			path := filepath.Join(Dir(), e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			stem := strings.TrimSuffix(e.Name(), ".lua")
			rb := fromScript(stem, string(data), path, false)
			byName[rb.Name] = rb
		}
	}
	out := make([]Runbook, 0, len(byName))
	for _, rb := range byName {
		out = append(out, rb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Load returns one runbook by name (user definition wins over a builtin).
func Load(name string) (Runbook, error) {
	for _, rb := range List() {
		if rb.Name == name {
			return rb, nil
		}
	}
	return Runbook{}, fmt.Errorf("runbook not found: %s (known: %s)", name, strings.Join(names(List()), ", "))
}

// CheckParams enforces required params before the build phase runs, and
// rejects params the runbook does not declare (a typo'd param silently doing
// nothing is exactly the chaos runbooks exist to remove).
func CheckParams(rb Runbook, given map[string]string) error {
	declared := make(map[string]bool, len(rb.Params))
	for _, p := range rb.Params {
		declared[p.Name] = true
		if p.Required && strings.TrimSpace(given[p.Name]) == "" {
			return fmt.Errorf("runbook %s: required param %q is missing", rb.Name, p.Name)
		}
	}
	for name := range given {
		if !declared[name] {
			return fmt.Errorf("runbook %s: unknown param %q (declared: %s)", rb.Name, name, strings.Join(paramNames(rb.Params), ", "))
		}
	}
	return nil
}

func names(rbs []Runbook) []string {
	out := make([]string, 0, len(rbs))
	for _, rb := range rbs {
		out = append(out, rb.Name)
	}
	return out
}

func paramNames(ps []Param) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}
