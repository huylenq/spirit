package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors — adaptive for light/dark terminals
	ColorWorking     = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#f59e0b"} // amber
	ColorDone        = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"} // blue
	ColorDeferred    = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#a78bfa"} // purple
	ColorPlan        = lipgloss.AdaptiveColor{Light: "#006666", Dark: "#48968c"} // teal (plan mode, matches Claude Code)
	ColorMuted       = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9ca3af"} // gray
	ColorAccent      = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"} // blue
	ColorGreen       = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#10b981"} // green
	ColorBorder      = lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#4b5563"} // border gray
	ColorSelectionBg = lipgloss.AdaptiveColor{Light: "#dde3f0", Dark: "#1e2235"} // selection row bg

	// Header
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1)

	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent)

	// Stats in header
	StatWorkingStyle  = lipgloss.NewStyle().Foreground(ColorWorking)
	StatDoneStyle     = lipgloss.NewStyle().Foreground(ColorDone)
	StatDeferredStyle = lipgloss.NewStyle().Foreground(ColorDeferred)
	StatPlanStyle     = lipgloss.NewStyle().Foreground(ColorPlan)

	// List panel
	ListPanelStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderRight(true).
			BorderForeground(ColorBorder)

	// Group headers in list
	GroupHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1)

	GroupHeaderWorkingStyle  = GroupHeaderStyle.Foreground(ColorWorking)
	GroupHeaderDoneStyle     = GroupHeaderStyle.Foreground(ColorDone)
	GroupHeaderDeferredStyle = GroupHeaderStyle.Foreground(ColorDeferred)
	GroupHeaderProjectStyle  = GroupHeaderStyle.Foreground(ColorMuted)
	ProjectSubHeaderStyle   = lipgloss.NewStyle().Foreground(ColorMuted).Padding(0, 1)

	// List items
	ItemStyle = lipgloss.NewStyle()

	SelectedBarStyle = lipgloss.NewStyle().Foreground(ColorAccent).Background(ColorSelectionBg) // accent bar on selection bg
	SelectedBgStyle  = lipgloss.NewStyle().Background(ColorSelectionBg)

	ItemDetailStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Preview panel
	PreviewPanelStyle = lipgloss.NewStyle().
				Padding(0, 1)

	PreviewTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorAccent)

	PreviewMetaStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Padding(0, 1)

	PreviewContentStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorBorder)

	// Footer
	FooterStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Padding(0, 1)

	FooterKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent)

	FooterDimStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	FooterDangerStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FF5555"))

	// Filter
	FilterPromptStyle = lipgloss.NewStyle().
				Foreground(ColorAccent).
				Bold(true)

	// Relay (prompt relay input)
	RelayPromptStyle = lipgloss.NewStyle().
				Foreground(ColorGreen).
				Bold(true)

	// Group separator
	SeparatorStyle = lipgloss.NewStyle().
			Foreground(ColorBorder)

	// Transcript overlay in preview
	TranscriptOverlayStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorAccent).
				Padding(0, 1)

	TranscriptTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorAccent)

	TranscriptMsgStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#e5e7eb"})

	SummaryStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9ca3af"})

	// Debug overlay in preview
	DebugOverlayStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorWorking).
				Padding(0, 1)

	DebugTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorWorking)

	// Diff stats
	DiffAddedStyle = lipgloss.NewStyle().Foreground(ColorGreen)

	// Empty state
	EmptyStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Padding(2, 4).
			Align(lipgloss.Center)

	// Flash overlay
	FlashErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ff5555")).
			Background(lipgloss.Color("#1a1a1a")).
			Bold(true).
			Padding(0, 1)

	FlashInfoStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Padding(0, 1)
)
