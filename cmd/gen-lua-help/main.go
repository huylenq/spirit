// gen-lua-help parses doc comments on luaXxx functions in internal/scripting/api_*.go
// and generates register_generated.go and help_generated.go.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// categoryOrder defines the fixed order of categories in the generated help.
var categoryOrder = []string{
	"Session Discovery",
	"Send & Wait",
	"Lifecycle",
	"Orchestrator",
	"Features",
	"Backlog",
	"Context",
	"Utilities",
}

type funcInfo struct {
	GoName      string // e.g. "luaSessions"
	LuaName     string // e.g. "sessions"
	Signature   string // e.g. "sessions([{status}]) -> []session"
	Category    string
	Description string // may be multi-line
}

func main() {
	// go:generate sets CWD to the directory containing the directive (internal/scripting/).
	scriptingDir := "."
	if _, err := os.Stat("api_sessions.go"); err != nil {
		// Fallback: try from project root
		scriptingDir = filepath.Join("internal", "scripting")
		if _, err := os.Stat(filepath.Join(scriptingDir, "api_sessions.go")); err != nil {
			fmt.Fprintf(os.Stderr, "gen-lua-help: cannot find api_sessions.go in . or internal/scripting/\n")
			os.Exit(1)
		}
	}

	funcs, err := parseFuncs(scriptingDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-lua-help: %v\n", err)
		os.Exit(1)
	}

	sessionFields, err := extractTableFields(scriptingDir, "sessionToTable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-lua-help: extracting session fields: %v\n", err)
		os.Exit(1)
	}

	backlogFields, err := extractTableFields(scriptingDir, "backlogToTable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-lua-help: extracting backlog fields: %v\n", err)
		os.Exit(1)
	}

	if err := writeRegisterFile(scriptingDir, funcs); err != nil {
		fmt.Fprintf(os.Stderr, "gen-lua-help: writing register file: %v\n", err)
		os.Exit(1)
	}

	if err := writeHelpFile(scriptingDir, funcs, sessionFields, backlogFields); err != nil {
		fmt.Fprintf(os.Stderr, "gen-lua-help: writing help file: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "gen-lua-help: generated %d functions across %d categories\n", len(funcs), len(categoryOrder))
}

func parseFuncs(dir string) ([]funcInfo, error) {
	fset := token.NewFileSet()

	files, err := filepath.Glob(filepath.Join(dir, "api_*.go"))
	if err != nil {
		return nil, err
	}

	var funcs []funcInfo
	seen := map[string]bool{}

	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			if !strings.HasPrefix(name, "lua") || len(name) < 4 || !unicode.IsUpper(rune(name[3])) {
				continue
			}

			// Validate return type is lua.LGFunction
			if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				return nil, fmt.Errorf("%s: %s must return exactly one value (lua.LGFunction)", filepath.Base(path), name)
			}
			retType := fn.Type.Results.List[0].Type
			sel, ok := retType.(*ast.SelectorExpr)
			if !ok {
				return nil, fmt.Errorf("%s: %s return type must be lua.LGFunction", filepath.Base(path), name)
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "lua" || sel.Sel.Name != "LGFunction" {
				return nil, fmt.Errorf("%s: %s return type must be lua.LGFunction, got %s.%s", filepath.Base(path), name, ident.Name, sel.Sel.Name)
			}

			// Parse doc comment
			if fn.Doc == nil || len(fn.Doc.List) == 0 {
				return nil, fmt.Errorf("%s: %s is missing a doc comment", filepath.Base(path), name)
			}

			info, err := parseDoc(fn.Doc, name, filepath.Base(path))
			if err != nil {
				return nil, err
			}

			if seen[info.LuaName] {
				return nil, fmt.Errorf("duplicate Lua global name: %s", info.LuaName)
			}
			seen[info.LuaName] = true

			funcs = append(funcs, info)
		}
	}

	// Validate every category has at least one function
	catCount := map[string]int{}
	for _, fn := range funcs {
		catCount[fn.Category]++
	}
	for _, cat := range categoryOrder {
		if catCount[cat] == 0 {
			return nil, fmt.Errorf("category %q in categoryOrder has no functions — remove it or add a function", cat)
		}
	}

	// Sort by category order, then alphabetically within category
	catIdx := map[string]int{}
	for i, c := range categoryOrder {
		catIdx[c] = i
	}
	sort.Slice(funcs, func(i, j int) bool {
		ci, cj := catIdx[funcs[i].Category], catIdx[funcs[j].Category]
		if ci != cj {
			return ci < cj
		}
		return funcs[i].LuaName < funcs[j].LuaName
	})

	return funcs, nil
}

func parseDoc(doc *ast.CommentGroup, goName, filename string) (funcInfo, error) {
	var lines []string
	for _, c := range doc.List {
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimPrefix(text, " ")
		lines = append(lines, text)
	}

	if len(lines) < 3 {
		return funcInfo{}, fmt.Errorf("%s: %s doc comment must have at least 3 lines (signature, category, description)", filename, goName)
	}

	signature := lines[0]

	// Parse category
	if !strings.HasPrefix(lines[1], "Category: ") {
		return funcInfo{}, fmt.Errorf("%s: %s doc line 2 must be 'Category: <name>', got %q", filename, goName, lines[1])
	}
	category := strings.TrimPrefix(lines[1], "Category: ")

	// Validate category
	validCat := false
	for _, c := range categoryOrder {
		if c == category {
			validCat = true
			break
		}
	}
	if !validCat {
		return funcInfo{}, fmt.Errorf("%s: %s has unknown category %q", filename, goName, category)
	}

	description := strings.Join(lines[2:], "\n")

	// Derive lua name and validate against signature
	luaName := goNameToLuaName(goName)
	sigName := signature
	if idx := strings.IndexByte(sigName, '('); idx >= 0 {
		sigName = sigName[:idx]
	}
	if sigName != luaName {
		return funcInfo{}, fmt.Errorf("%s: %s derived Lua name %q doesn't match signature %q", filename, goName, luaName, sigName)
	}

	return funcInfo{
		GoName:      goName,
		LuaName:     luaName,
		Signature:   signature,
		Category:    category,
		Description: description,
	}, nil
}

// goNameToLuaName converts "luaCancelQueue" → "cancel_queue".
// NOTE: This assumes single-uppercase-initial CamelCase (no Go-style acronyms like HTTP or ID).
// "luaHTTPSend" would incorrectly become "h_t_t_p_send". If acronyms are needed,
// add consecutive-uppercase run detection. The signature validation catches mismatches.
func goNameToLuaName(goName string) string {
	// Strip "lua" prefix
	name := goName[3:]

	var buf strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				buf.WriteByte('_')
			}
			buf.WriteRune(unicode.ToLower(r))
		} else {
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

func extractTableFields(dir, funcName string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(dir, "convert.go"), nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing convert.go: %w", err)
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}

		var fields []string
		seen := map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RawSetString" {
				return true
			}
			if len(call.Args) >= 1 {
				lit, ok := call.Args[0].(*ast.BasicLit)
				if ok && lit.Kind == token.STRING {
					field := strings.Trim(lit.Value, `"`)
					if !seen[field] {
						seen[field] = true
						fields = append(fields, field)
					}
				}
			}
			return true
		})

		return fields, nil
	}

	return nil, fmt.Errorf("function %s not found in convert.go", funcName)
}

func writeRegisterFile(dir string, funcs []funcInfo) error {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by gen-lua-help; DO NOT EDIT.\n\n")
	buf.WriteString("package scripting\n\n")
	buf.WriteString("import lua \"github.com/yuin/gopher-lua\"\n\n")
	buf.WriteString("func registerAllAPIs(L *lua.LState, deps Deps) {\n")
	for _, fn := range funcs {
		fmt.Fprintf(&buf, "\tL.SetGlobal(%q, L.NewFunction(%s(deps)))\n", fn.LuaName, fn.GoName)
	}
	buf.WriteString("}\n")

	return os.WriteFile(filepath.Join(dir, "register_generated.go"), buf.Bytes(), 0o644)
}

type categoryGroup struct {
	Name  string
	Funcs []funcInfo
}

func writeHelpFile(dir string, funcs []funcInfo, sessionFields, backlogFields []string) error {
	// Group by category
	catMap := map[string][]funcInfo{}
	for _, fn := range funcs {
		catMap[fn.Category] = append(catMap[fn.Category], fn)
	}

	var cats []categoryGroup
	for _, name := range categoryOrder {
		if fns, ok := catMap[name]; ok {
			cats = append(cats, categoryGroup{Name: name, Funcs: fns})
		}
	}

	var buf bytes.Buffer
	buf.WriteString("// Code generated by gen-lua-help; DO NOT EDIT.\n\n")
	buf.WriteString("package scripting\n\n")
	buf.WriteString("// LuaScriptingReference is the auto-generated Lua API reference used by --agent-help.\n")
	buf.WriteString("const LuaScriptingReference = `## Eval: Lua Scripting Interface\n\n")
	buf.WriteString("Sandboxed Lua VM (base/table/string/math only — no os/io/debug).\n")
	buf.WriteString("Each invocation is stateless. Last expression is JSON-serialized to stdout.\n")
	buf.WriteString("Errors go to stderr, exit 1. Use pcall() for recovery.\n")

	for _, cat := range cats {
		fmt.Fprintf(&buf, "\n### %s\n", cat.Name)
		for _, fn := range cat.Funcs {
			fmt.Fprintf(&buf, "\n%s\n", fn.Signature)
			// Indent each description line
			for _, line := range strings.Split(fn.Description, "\n") {
				fmt.Fprintf(&buf, "  %s\n", line)
			}
		}
	}

	buf.WriteString("\n### Session Fields\n\n")
	buf.WriteString(strings.Join(sessionFields, ", "))
	buf.WriteString("\n\n### Backlog Fields\n\n")
	buf.WriteString(strings.Join(backlogFields, ", "))
	buf.WriteString("\n`\n")

	return os.WriteFile(filepath.Join(dir, "help_generated.go"), buf.Bytes(), 0o644)
}
