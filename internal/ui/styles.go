package ui

import "github.com/charmbracelet/lipgloss"

func promptEditorOverlay(color lipgloss.TerminalColor) lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(1, 2)
}

func promptEditorTitle(color lipgloss.TerminalColor) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(color)
}

var (
	// Colors — adaptive for light/dark terminals
	ColorWorking     = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#f59e0b"} // amber
	ColorDone        = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"} // blue
	ColorLater       = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#a78bfa"} // purple
	ColorPlan        = lipgloss.AdaptiveColor{Light: "#006666", Dark: "#48968c"} // teal (plan mode, matches Claude Code)
	ColorMuted       = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9ca3af"} // gray
	ColorAccent      = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"} // blue
	ColorGreen       = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#10b981"} // green
	ColorBorder      = lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#4b5563"} // border gray
	ColorSelectionBg = lipgloss.AdaptiveColor{Light: "#dde3f0", Dark: "#1e2235"} // selection row bg
	ColorWaiting     = lipgloss.AdaptiveColor{Light: "#be185d", Dark: "#f472b6"} // magenta/rose — waiting for user
	ColorPostTool    = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#22d3ee"} // cyan — PostToolUse
	ColorOverlap     = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#fbbf24"} // yellow/amber — file overlap

	// Border frame (custom TUI outline)
	BorderCharStyle  = lipgloss.NewStyle().Foreground(ColorBorder)
	BorderLabelStyle = lipgloss.NewStyle().Align(lipgloss.Right).PaddingRight(1)

	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent)

	// Stats in header
	StatWorkingStyle  = lipgloss.NewStyle().Foreground(ColorWorking)
	StatDoneStyle     = lipgloss.NewStyle().Foreground(ColorDone)
	StatLaterStyle    = lipgloss.NewStyle().Foreground(ColorLater)
	StatPlanStyle     = lipgloss.NewStyle().Foreground(ColorPlan)
	StatWaitingStyle  = lipgloss.NewStyle().Foreground(ColorWaiting).Bold(true)
	StatPostToolStyle = lipgloss.NewStyle().Foreground(ColorPostTool)
	CommitDoneStyle   = DiffAddedStyle
	OverlapStyle      = lipgloss.NewStyle().Foreground(ColorOverlap)

	// Sidebar panel
	SidebarPanelStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderRight(true).
			BorderForeground(ColorBorder)

	// Group headers in list
	GroupHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1)

	ColorBacklog = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#22d3ee"} // cyan — backlog

	GroupHeaderWorkingStyle  = GroupHeaderStyle.Foreground(ColorWorking)
	GroupHeaderDoneStyle     = GroupHeaderStyle.Foreground(ColorDone)
	GroupHeaderLaterStyle    = GroupHeaderStyle.Foreground(ColorLater)
	GroupHeaderBacklogStyle  = GroupHeaderStyle.Foreground(ColorBacklog)
	GroupHeaderProjectStyle  = GroupHeaderStyle.Foreground(ColorMuted)
	ProjectSubHeaderStyle   = lipgloss.NewStyle().Foreground(ColorMuted).Padding(0, 1)

	// List items
	ItemStyle = lipgloss.NewStyle()

	SelectedBgStyle = lipgloss.NewStyle().Background(ColorSelectionBg)

	ItemDetailStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Detail panel
	DetailPanelStyle = lipgloss.NewStyle().
				Padding(0, 1)

	DetailTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorAccent)

	DetailMetaStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Padding(0, 1)

	DetailContentStyle = lipgloss.NewStyle().
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

	// Search
	SearchPromptStyle = lipgloss.NewStyle().
				Foreground(ColorAccent).
				Bold(true)

	// Relay (prompt relay input)
	RelayPromptStyle = lipgloss.NewStyle().
				Foreground(ColorGreen).
				Bold(true)

	// Queue relay prompt
	QueuePromptStyle = lipgloss.NewStyle().
			Foreground(ColorWorking).
			Bold(true)

	// Group separator
	SeparatorStyle = lipgloss.NewStyle().
			Foreground(ColorBorder)

	// Transcript overlay in preview
	TranscriptOverlayStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorBorder).
				Padding(0, 1, 1, 1)

	TranscriptTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorAccent)

	TranscriptMsgStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#e5e7eb"})
	TranscriptBulletStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#4b5563"}).
				Padding(0, 1)
	TranscriptCursorStyle = lipgloss.NewStyle().
				Foreground(ColorAccent).
				Padding(0, 1)

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

	// Diff background highlights (dimmed, used for all diff lines)
	DiffDelBg = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#f5e6e6", Dark: "#2a1517"})
	DiffAddBg = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#e6f2e6", Dark: "#152a1a"})
	// Diff prefix symbols (+/-/~) with distinct foreground colors
	DiffDelSymbol = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f87171"}).Bold(true)
	DiffAddSymbol = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#4ade80"}).Bold(true)
	DiffModSymbol = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#f59e0b"}).Bold(true)
	// Char-level emphasis within inline diffs (slightly brighter bg)
	DiffInlineDelBg = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#ebc8c8", Dark: "#3d1a1d"})
	DiffInlineAddBg = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#c3dfc3", Dark: "#1a3d24"})
	// Diff hunks overlay
	DiffOverlayStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorGreen).
				Padding(0, 1)

	DiffTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorGreen)

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

	// Toast notification overlay (transient, bottom-right)
	ToastStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(ColorMuted).
			Padding(0, 1)

	// Help overlay
	HelpOverlayStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorAccent).
				Padding(1, 2)

	HelpTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent)

	HelpGroupStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorMuted).
			Underline(true)

	// Command palette
	PaletteOverlayStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorAccent).
				Padding(0, 1)

	PaletteSelectedStyle = lipgloss.NewStyle().
				Foreground(ColorAccent).
				Bold(true)

	PaletteDisabledStyle = lipgloss.NewStyle().
				Foreground(ColorMuted)

	PaletteSepStyle = lipgloss.NewStyle().
			Foreground(ColorBorder)

	// Prompt editor overlays — session (green) and backlog (cyan)
	PromptEditorOverlayStyle        = promptEditorOverlay(ColorGreen)
	BacklogPromptEditorOverlayStyle = promptEditorOverlay(ColorBacklog)
	PromptEditorTitleStyle          = promptEditorTitle(ColorGreen)
	BacklogPromptEditorTitleStyle   = promptEditorTitle(ColorBacklog)

	// Preferences editor overlay
	PrefsEditorOverlayStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorAccent).
				Padding(1, 2)

	PrefsEditorTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorAccent)
)
