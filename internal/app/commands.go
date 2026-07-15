package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/runbook"
)

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
			Enabled:    hasSelection,
			Capability: agent.CapabilityRelayPrompt,
			Execute:    func(m *Model) (Model, tea.Cmd) { return m.execPromptRelay() },
		},
		{
			Name: "Tag session", Binding: &Keys.PromptTag,
			Enabled: hasSessionID,
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execTagRelay() },
		},
		{
			Name: "Queue message", Binding: &Keys.Queue,
			Enabled:    hasSelection,
			Capability: agent.CapabilityQueue,
			Execute:    func(m *Model) (Model, tea.Cmd) { return m.execQueue() },
		},
		{
			Name: "Later", Binding: &Keys.Later,
			Enabled:    hasSelection,
			Capability: agent.CapabilityLater,
			Execute:    func(m *Model) (Model, tea.Cmd) { return m.execLater() },
		},
		{
			Name: "Later + kill", Binding: &Keys.LaterKill,
			Enabled:    hasSelection,
			Capability: agent.CapabilityLater,
			Execute:    func(m *Model) (Model, tea.Cmd) { return m.execLaterKill() },
		},
		{
			Name: "Kill + close", Binding: &Keys.Kill,
			Enabled:    hasSelection,
			Capability: agent.CapabilityKill,
			Execute:    func(m *Model) (Model, tea.Cmd) { return m.execKill() },
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
			Enabled:    hasSelection,
			Capability: agent.CapabilityRenameNative,
			Execute:    func(m *Model) (Model, tea.Cmd) { return m.execRenamePrompt() },
		},
		{
			Name: "Apply title", Binding: &Keys.ApplyTitle,
			Capability: agent.CapabilityRenameNative,
			Enabled: func(m *Model) bool {
				s, ok := m.sidebar.SelectedItem()
				return ok && s.TitleDrift
			},
			Execute: func(m *Model) (Model, tea.Cmd) { return m.execApplyTitle() },
		},
		{
			Name: "Commit", Binding: &Keys.Commit,
			Enabled:    canCommit,
			Capability: agent.CapabilityCommit,
			Execute:    func(m *Model) (Model, tea.Cmd) { return m.execCommit() },
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
			Name: "Lulu", Binding: &Keys.Copilot,
			Execute: func(m *Model) (Model, tea.Cmd) {
				return execOpenCopilot(m)
			},
		},
		{
			Name: "Lulu toggle", Binding: &Keys.CopilotToggle,
			Execute: func(m *Model) (Model, tea.Cmd) {
				return execToggleCopilot(m)
			},
		},
		{
			Name: "Lulu mode (float/docked)", Binding: &Keys.CopilotSwitchMode,
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

		// --- Durable reactivity (W9) ---
		{
			Name:    "Durable reactivity: enable",
			Execute: func(m *Model) (Model, tea.Cmd) { return execReactiveEnable(m) },
		},
		{
			Name:    "Durable reactivity: pause",
			Execute: func(m *Model) (Model, tea.Cmd) { return execReactivePause(m) },
		},
		{
			Name:    "Durable reactivity: resume",
			Execute: func(m *Model) (Model, tea.Cmd) { return execReactiveResume(m) },
		},
		{
			Name:    "Durable reactivity: disable",
			Execute: func(m *Model) (Model, tea.Cmd) { return execReactiveDisable(m) },
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
	// Runbooks (W8): each named runbook is a palette entry. Selecting one
	// dry-runs it and shows the preview overlay; y executes the exact
	// previewed steps. Runbooks with required params open a prefilled Lua
	// invocation instead.
	for _, rb := range runbook.List() {
		rb := rb
		cmds = append(cmds, Command{
			Name: "Runbook: " + rb.Name,
			Execute: func(m *Model) (Model, tea.Cmd) {
				return m.execRunbookPlan(rb)
			},
		})
	}

	// Chord-bound entries inherit their executor from the chord registry.
	for i := range cmds {
		if cmds[i].Chord != nil && cmds[i].Execute == nil {
			cmds[i].Execute = cmds[i].Chord.Execute
		}
	}
	return cmds
}
