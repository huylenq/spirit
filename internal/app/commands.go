package app

import tea "github.com/charmbracelet/bubbletea"

// buildCommands returns all palette-worthy commands grouped by category.
func buildCommands() []Command {
	cmds := []Command{
		// --- Session actions ---
		{
			Name: "Switch to pane", Binding: &Keys.Enter,
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSwitchPane() },
		},
		{
			Name: "Send to session", Binding: &Keys.PromptRelay,
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execPromptRelay() },
		},
		{
			Name: "Tag session", Binding: &Keys.PromptTag,
			Enabled: hasSessionID,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execTagRelay() },
		},
		{
			Name: "Queue message", Binding: &Keys.Queue,
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execQueue() },
		},
		{
			Name: "Later", Binding: &Keys.Later,
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execLater() },
		},
		{
			Name: "Later + kill", Binding: &Keys.LaterKill,
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execLaterKill() },
		},
		{
			Name: "Kill + close", Binding: &Keys.Kill,
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execKill() },
		},
		{
			Name: "Synthesize", Binding: &Keys.Synthesize,
			Enabled: hasSessionID,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSynthesize() },
		},
		{
			Name: "Rename all windows", Binding: &Keys.Rename,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execRename() },
		},
		{
			Name: "Rename", Binding: &Keys.RenamePrompt,
			Enabled: hasSelection,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execRenamePrompt() },
		},
		{
			Name: "Apply title", Binding: &Keys.ApplyTitle,
			Enabled: func(m *Model) bool {
				s, ok := m.sidebar.SelectedItem()
				return ok && s.TitleDrift
			},
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execApplyTitle() },
		},
		{
			Name: "Commit", Binding: &Keys.Commit,
			Enabled: canCommit,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execCommit() },
		},

		{
			Name: "New session", Binding: &Keys.NewSession,
			Enabled: func(m *Model) bool {
				_, ok := m.sidebar.SelectedProject()
				return ok
			},
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execNewSession() },
		},
		{
			Name: "New session at path", Binding: &Keys.NewSessionAtPath,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execNewSessionAtPath() },
		},

		// --- Copilot ---
		{
			Name: "Copilot", Binding: &Keys.Copilot,
			Execute: func(m *Model) (Model, tea.Cmd) {
				return execOpenCopilot(m)
			},
		},
		{
			Name: "Copilot mode (float/docked)", Binding: &Keys.CopilotMode,
			Execute: func(m *Model) (Model, tea.Cmd) {
				return execSwitchCopilotMode(m)
			},
		},

		// --- Global actions ---
		{
			Name: "Search", Binding: &Keys.Search,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSearch() },
		},
		{
			Name: "Synthesize all", Binding: &Keys.SynthesizeAll,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execSynthesizeAll() },
		},
		{
			Name: "Fullscreen toggle", Binding: &Keys.Fullscreen,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execFullscreen() },
		},

		// --- Toggles ---
		{
			Name: "Group by project", Binding: &Keys.GroupMode,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execGroupMode() },
		},
		{
			Name: "Minimap", Binding: &Keys.Minimap,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execMinimap() },
		},
		{
			Name: "Toggle chat outline", Binding: &Keys.ChatOutline,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execChatOutline() },
		},
		{
			Name: "Toggle diffs", Chord: chord("gd"),
			Enabled: hasSessionID,
		},
		{
			Name: "Toggle hooks", Chord: chord("gh"),
		},
		{
			Name: "Debug overlay", Binding: &Keys.Debug,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execDebug() },
		},
		{
			Name: "Settings", Binding: &Keys.Prefs,
			Execute: func(m *Model) (Model, tea.Cmd) {
				m.state = StatePrefsEditor
				m.settingsCursor = 0
				return *m, nil
			},
		},
		{
			Name: "Help", Binding: &Keys.Help,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execHelp() },
		},

		// --- Copy ---
		{
			Name: "Copy session ID", Chord: chord("ys"),
			Enabled: hasSessionID,
		},
		{
			Name: "Capture view", Chord: chord("yc"),
		},
	}
	// Chord-bound entries inherit their executor from the chord registry.
	for i := range cmds {
		if cmds[i].Chord != nil && cmds[i].Execute == nil {
			cmds[i].Execute = cmds[i].Chord.Execute
		}
	}
	return cmds
}
