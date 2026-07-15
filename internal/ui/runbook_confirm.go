package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// RunbookConfirmView renders the W8 runbook preview-then-approve overlay: the
// dry-run plan's steps (targets + operations + risk, destructive flagged) and
// the y/esc affordance. The confirm executes EXACTLY these previewed steps —
// the same plan→approve→action contract Lulu gets.
func RunbookConfirmView(name, description string, steps []CopilotPermissionBatchStep, width int) string {
	contentW := max(min(width-4, 90), 24)

	var lines []string
	title := permTitleStyle.Render("▶ runbook: " + name) // ▶
	lines = append(lines, ansi.Truncate(title, contentW, "…"))
	if description != "" {
		lines = append(lines, permKindStyle.Render(ansi.Truncate(description, contentW, "…")))
	}
	lines = append(lines, "")
	if len(steps) == 0 {
		lines = append(lines, permKindStyle.Render("plan is empty — nothing to run"))
	} else {
		lines = append(lines, renderPermissionBatch(steps, contentW)...)
	}
	lines = append(lines, "")
	lines = append(lines, permCountdownStyle.Render(fmt.Sprintf("y run %d step(s) · esc cancel", len(steps))))

	return permBoxStyle.BorderForeground(lipgloss.Color("#818cf8")).Width(contentW).Render(strings.Join(lines, "\n"))
}
