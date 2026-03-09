package app

import tea "github.com/charmbracelet/bubbletea"

// buildCommands returns all palette-worthy commands grouped by category.
func buildCommands() []Command {
	return []Command{
		// --- Session actions ---
		{
			Name: "Switch to pane", Hotkey: "enter",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSwitchPane() },
		},
		{
			Name: "Send to session", Hotkey: ">",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execPromptRelay() },
		},
		{
			Name: "Queue message", Hotkey: "<",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execQueue() },
		},
		{
			Name: "Later", Hotkey: "w",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execLater() },
		},
		{
			Name: "Later + kill", Hotkey: "W",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execLaterKill() },
		},
		{
			Name: "Kill + close", Hotkey: "d",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execKill() },
		},
		{
			Name: "Synthesize", Hotkey: "s",
			Enabled: hasSessionID,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSynthesize() },
		},
		{
			Name: "Rename window", Hotkey: "R",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execRename() },
		},
		{
			Name: "Refresh preview", Hotkey: "r",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execRefresh() },
		},
		{
			Name: "Commit", Hotkey: "c",
			Enabled: canCommit,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execCommit() },
		},
		{
			Name: "Commit + done", Hotkey: "C",
			Enabled: canCommit,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execCommitAndDone() },
		},

		// --- Global actions ---
		{
			Name: "Search", Hotkey: "/",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSearch() },
		},
		{
			Name: "Synthesize all", Hotkey: "S",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSynthesizeAll() },
		},
		{
			Name: "Fullscreen toggle", Hotkey: "z",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execFullscreen() },
		},

		// --- Toggles ---
		{
			Name: "Group by project", Hotkey: "g",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execGroupMode() },
		},
		{
			Name: "Minimap", Hotkey: "m",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execMinimap() },
		},
		{
			Name: "Toggle transcript", Hotkey: "t",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execTranscript() },
		},
		{
			Name: "Toggle diffs", Hotkey: "g d",
			Enabled: hasSessionID,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execToggleDiffs() },
		},
		{
			Name: "Toggle hooks", Hotkey: "g h",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execToggleHooks() },
		},
		{
			Name: "Debug overlay", Hotkey: "D",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execDebug() },
		},
		{
			Name: "Help", Hotkey: "?",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execHelp() },
		},

		// --- Copy ---
		{
			Name: "Copy session ID", Hotkey: "y s",
			Enabled: hasSessionID,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execCopySessionID() },
		},
		{
			Name: "Capture view", Hotkey: "y c",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execCaptureView() },
		},
	}
}
