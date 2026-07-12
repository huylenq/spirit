package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/claude"
)

func TestProviderProjectCodeUsesSameWidth(t *testing.T) {
	cl := claude.ClaudeSession{Provider: claude.ProviderClaude, ProjectCode: "DOS"}
	cx := claude.ClaudeSession{Provider: claude.ProviderCodex, ProjectCode: "DOS"}
	if got, want := lipgloss.Width(renderProjectCode(cl)), projectCodeWidth(cl); got != want {
		t.Fatalf("Claude rendered width = %d, want %d", got, want)
	}
	if got, want := lipgloss.Width(renderProjectCode(cx)), projectCodeWidth(cx); got != want {
		t.Fatalf("Codex rendered width = %d, want %d", got, want)
	}
}

func TestProviderPrefixesUseFixedWidth(t *testing.T) {
	for _, provider := range []claude.Provider{claude.ProviderClaude, claude.ProviderCodex} {
		s := claude.ClaudeSession{Provider: provider}
		if got := lipgloss.Width(providerPrefix(s)); got != providerPrefixWidth {
			t.Fatalf("%s prefix width = %d, want %d", provider, got, providerPrefixWidth)
		}
		if got := lipgloss.Width(providerPrefixBg(s, lipgloss.Color("#111111"))); got != providerPrefixWidth {
			t.Fatalf("%s selected prefix width = %d, want %d", provider, got, providerPrefixWidth)
		}
	}
}
