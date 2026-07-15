package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	dmp "github.com/sergi/go-diff/diffmatchpatch"
)

// CopilotPermission is the TUI-side view of a Hermes session/request_permission the
// daemon forwarded for a human decision (W4). It is a ui-local mirror of
// daemon.CopilotPermissionRequest so the ui package stays independent of daemon.
type CopilotPermission struct {
	PermissionID string
	ToolCallID   string
	Title        string
	Kind         string // "edit", "execute", ...
	Command      string
	Diffs        []CopilotPermissionDiff
	BatchSteps   []CopilotPermissionBatchStep
	Options      []CopilotPermissionOption
	Sensitive    bool
	SensitiveHit string
	DeadlineUnix int64
}

// CopilotPermissionBatchStep mirrors daemon.CopilotPermissionBatchStep: one
// legible line of a W8 batch approval — operation, resolved target, detail,
// risk class.
type CopilotPermissionBatchStep struct {
	Index  int
	Op     string
	Target string
	Detail string
	Risk   string
}

type CopilotPermissionOption struct {
	OptionID string
	Kind     string
	Name     string
	Key      string
}

type CopilotPermissionDiff struct {
	Path    string
	OldText string
	NewText string
}

// OptionForKey resolves a keyboard accelerator to the option it selects.
func (p CopilotPermission) OptionForKey(key string) (CopilotPermissionOption, bool) {
	for _, o := range p.Options {
		if o.Key == key {
			return o, true
		}
	}
	return CopilotPermissionOption{}, false
}

// DenyOption returns a deny/reject option (for the Escape shortcut), preferring a
// reject-once. Returns ok=false if Hermes offered no explicit deny (the caller then
// refuses by sending "").
func (p CopilotPermission) DenyOption() (CopilotPermissionOption, bool) {
	for _, o := range p.Options {
		if o.Key == "n" {
			return o, true
		}
	}
	for _, o := range p.Options {
		if o.Key == "N" {
			return o, true
		}
	}
	return CopilotPermissionOption{}, false
}

// SecondsRemaining returns whole seconds until the auto-deny deadline, floored at 0.
func (p CopilotPermission) SecondsRemaining(now time.Time) int {
	if p.DeadlineUnix == 0 {
		return 0
	}
	rem := int(p.DeadlineUnix - now.Unix())
	if rem < 0 {
		return 0
	}
	return rem
}

var (
	permTitleStyle     = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	permKindStyle      = lipgloss.NewStyle().Foreground(ColorMuted)
	permSensitiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Bold(true)
	permCmdStyle       = lipgloss.NewStyle().Foreground(ColorGreen)
	permOptKeyStyle    = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	permOptNameStyle   = lipgloss.NewStyle().Foreground(ColorMuted)
	permCountdownStyle = lipgloss.NewStyle().Foreground(ColorMuted).Italic(true)
	permBoxStyle       = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorAccent).
				Padding(0, 1)
)

// RenderCopilotPermission renders the approval prompt as a bordered box: a title
// with the tool kind and any sensitive-path flag, the real diff (edits) or the
// command (dangerous executes), the offered options with their keys, and a
// countdown to the auto-deny deadline.
func RenderCopilotPermission(p CopilotPermission, width int, now time.Time) string {
	contentW := max(width-4, 8) // border(2) + padding(2)

	var lines []string

	icon := "⚙" // ⚙
	switch p.Kind {
	case "edit":
		icon = "✎" // ✎
	case "execute":
		icon = "$" // $
	}
	title := p.Title
	if title == "" {
		title = "Permission required"
	}
	header := permTitleStyle.Render(icon+" "+title) + permKindStyle.Render("  ["+p.Kind+"]")
	if p.Sensitive {
		header += "  " + permSensitiveStyle.Render("⚠ sensitive: "+p.SensitiveHit) // ⚠
	}
	lines = append(lines, ansi.Truncate(header, contentW, "…"))
	lines = append(lines, "")

	switch {
	case len(p.BatchSteps) > 0:
		lines = append(lines, renderPermissionBatch(p.BatchSteps, contentW)...)
	case len(p.Diffs) > 0:
		for _, d := range p.Diffs {
			lines = append(lines, permKindStyle.Render(ansi.Truncate(d.Path, contentW, "…")))
			lines = append(lines, renderPermissionDiff(d.OldText, d.NewText, contentW)...)
		}
	case p.Command != "":
		for _, cl := range strings.Split(wrapText("$ "+p.Command, contentW), "\n") {
			lines = append(lines, permCmdStyle.Render(cl))
		}
	}

	lines = append(lines, "")
	lines = append(lines, renderPermissionOptions(p.Options, contentW))

	rem := p.SecondsRemaining(now)
	lines = append(lines, permCountdownStyle.Render(fmt.Sprintf("auto-deny in %ds · esc denies", rem)))

	return permBoxStyle.Width(contentW).Render(strings.Join(lines, "\n"))
}

// renderPermissionBatch renders a W8 batch payload as one legible line per
// step — operation → target — detail, destructive steps flagged — plus a
// header counting steps and destructive approval points. Never an opaque
// JSON blob.
func renderPermissionBatch(steps []CopilotPermissionBatchStep, width int) []string {
	destructive := 0
	for _, s := range steps {
		if s.Risk == "destructive" {
			destructive++
		}
	}
	header := fmt.Sprintf("batch: %d step(s)", len(steps))
	if destructive > 0 {
		header += "  " + permSensitiveStyle.Render(fmt.Sprintf("⚠ %d destructive", destructive)) // ⚠
	}
	out := []string{ansi.Truncate(header, width, "…")}
	for _, s := range steps {
		line := fmt.Sprintf("%d. %s", s.Index, s.Op)
		if s.Target != "" {
			line += " → " + s.Target
		}
		if s.Detail != "" {
			line += "  — " + s.Detail
		}
		rendered := permKindStyle.Render(ansi.Truncate(line, width-2, "…"))
		if s.Risk == "destructive" {
			rendered = permSensitiveStyle.Render(ansi.Truncate(line+"  ⚠", width-2, "…")) // ⚠
		}
		out = append(out, "  "+rendered)
	}
	return out
}

// renderPermissionOptions renders the offered answers as "y allow once   n deny".
func renderPermissionOptions(opts []CopilotPermissionOption, width int) string {
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		label := o.Name
		if label == "" {
			label = o.OptionID
		}
		seg := label
		if o.Key != "" {
			seg = permOptKeyStyle.Render(o.Key) + " " + permOptNameStyle.Render(label)
		} else {
			seg = permOptNameStyle.Render(label)
		}
		parts = append(parts, seg)
	}
	return ansi.Truncate(strings.Join(parts, "   "), width, "…")
}

// renderPermissionDiff renders a compact line-level diff of an edit, capped in
// height, reusing the detail-view diff colors.
func renderPermissionDiff(oldText, newText string, width int) []string {
	const maxLines = 16
	bodyW := max(width-2, 4)

	differ := dmp.New()
	c1, c2, arr := differ.DiffLinesToChars(oldText, newText)
	diffs := differ.DiffMain(c1, c2, false)
	diffs = differ.DiffCharsToLines(diffs, arr)

	var out []string
	add := DiffAddSymbol.Render("+")
	del := DiffDelSymbol.Render("-")
	for _, d := range diffs {
		text := strings.TrimRight(d.Text, "\n")
		if text == "" && d.Type == dmp.DiffEqual {
			continue
		}
		for _, ln := range strings.Split(text, "\n") {
			trunc := ansi.Truncate(ln, bodyW, "…")
			switch d.Type {
			case dmp.DiffInsert:
				out = append(out, add+" "+DiffAddBg.Render(trunc))
			case dmp.DiffDelete:
				out = append(out, del+" "+DiffDelBg.Render(trunc))
			default:
				out = append(out, permKindStyle.Render("  "+trunc))
			}
			if len(out) >= maxLines {
				out = append(out, permKindStyle.Render("  … (diff truncated)"))
				return out
			}
		}
	}
	return out
}
