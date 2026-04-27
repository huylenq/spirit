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
			Name: "Tag session", Hotkey: "#",
			Enabled: hasSessionID,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execTagRelay() },
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
			Name: "Rename", Hotkey: "r",
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execRenamePrompt() },
		},
		{
			Name: "Apply title", Hotkey: "alt+r",
			Enabled: func(m *Model) bool {
				s, ok := m.sidebar.SelectedItem()
				return ok && s.TitleDrift
			},
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execApplyTitle() },
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
		{
			Name: "Commit + simplify + done", Hotkey: "D",
			Enabled: canCommit,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execCommitSimplifyAndDone() },
		},

		{
			Name: "New session", Hotkey: "a",
			Enabled: func(m *Model) bool {
				_, ok := m.sidebar.SelectedProject()
				return ok
			},
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execNewSession() },
		},
		{
			Name: "New session at path", Hotkey: "A",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execNewSessionAtPath() },
		},

		// --- Copilot ---
		{
			Name: "Copilot", Hotkey: "tab",
			Execute: func(m *Model) (Model, tea.Cmd) {
				return execOpenCopilot(m)
			},
		},
		{
			Name: "Copilot mode (float/docked)", Hotkey: "⇧tab",
			Execute: func(m *Model) (Model, tea.Cmd) {
				return execSwitchCopilotMode(m)
			},
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
			Name: "Toggle chat outline", Hotkey: "t",
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execChatOutline() },
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
			Name: "Settings", Hotkey: "P",
			Execute: func(m *Model) (Model, tea.Cmd) {
				m.state = StatePrefsEditor
				m.settingsCursor = 0
				return *m, nil
			},
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
