package app

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

func (m Model) handleKeyNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When help overlay is open, only ? and esc dismiss it; swallow everything else
	if m.showHelp {
		switch {
		case key.Matches(msg, Keys.Help), key.Matches(msg, Keys.Escape):
			m.showHelp = false
			return m, nil
		case key.Matches(msg, Keys.Palette):
			m.showHelp = false
			// fall through to palette handling below
		default:
			return m, nil
		}
	}

	// When spirit animal overlay is open, any key dismisses it
	if m.showSpiritAnimal {
		m.showSpiritAnimal = false
		return m, nil
	}

	// When message log overlay is open, ! and esc dismiss it
	if m.showMessageLog {
		switch {
		case key.Matches(msg, Keys.MessageLog), key.Matches(msg, Keys.Escape):
			m.showMessageLog = false
		}
		return m, nil
	}

	// Handle multi-key chord sequences
	if m.pendingChord != "" {
		seq := m.pendingChord + msg.String()
		if chord, ok := ChordExact(seq); ok {
			m.pendingChord = ""
			return m.executeChord(chord)
		}
		if len(ChordsWithPrefix(seq)) > 0 {
			m.pendingChord = seq
			return m, nil
		}
		// Not a valid chord continuation — cancel and fall through
		m.pendingChord = ""
	}

	// Check if this key starts any chord
	if len(ChordsWithPrefix(msg.String())) > 0 {
		m.pendingChord = msg.String()
		return m, nil
	}

	// Backlog-specific keys (when cursor is in backlog zone)
	if m.sidebar.IsBacklogSelected() {
		switch {
		case key.Matches(msg, Keys.Enter):
			return m.execEditBacklog()
		case key.Matches(msg, Keys.CtrlEnter), key.Matches(msg, Keys.NewSession):
			return m.execSubmitBacklog()
		case msg.String() == "e":
			return m.execOpenBacklogInEditor()
		case key.Matches(msg, Keys.Kill), msg.String() == "x":
			return m.execDeleteBacklog()
		case key.Matches(msg, Keys.ScrollDown):
			m.backlogScroll += max(m.detail.ViewportHeight()/2, 1)
			return m, nil
		case key.Matches(msg, Keys.ScrollUp):
			m.backlogScroll = max(m.backlogScroll-max(m.detail.ViewportHeight()/2, 1), 0)
			return m, nil
		case key.Matches(msg, Keys.PageDown):
			m.backlogScroll += max(m.detail.ViewportHeight()-3, 1)
			return m, nil
		case key.Matches(msg, Keys.PageUp):
			m.backlogScroll = max(m.backlogScroll-max(m.detail.ViewportHeight()-3, 1), 0)
			return m, nil
		case key.Matches(msg, Keys.LineDown), key.Matches(msg, Keys.MsgNext):
			m.backlogScroll++
			return m, nil
		case key.Matches(msg, Keys.LineUp), key.Matches(msg, Keys.MsgPrev):
			if m.backlogScroll > 0 {
				m.backlogScroll--
			}
			return m, nil
		}
		// Fall through to common nav keys (up/down/h/l/q/esc/etc.)
	}

	// New backlog item via `b` key — works from any context
	if msg.String() == "b" {
		// Session item selected
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m.execNewBacklogForCWD(s.CWD)
		}
		// Backlog item selected — create new backlog for the same project
		if backlog, ok := m.sidebar.SelectedBacklog(); ok {
			return m.execNewBacklogForCWD(backlog.CWD)
		}
		if m.sidebar.SelectionLevel() == ui.LevelProject {
			if pe, ok := m.sidebar.SelectedProject(); ok {
				if pe.StatusOrder == ui.OrderBacklog {
					// Backlog project: derive CWD from existing backlog
					return m.execNewBacklog()
				}
				// Session project: derive CWD from first session
				if s, ok := m.sidebar.SelectedProjectSession(); ok {
					return m.execNewBacklogForCWD(s.CWD)
				}
			}
		}
	}

	switch {
	case key.Matches(msg, Keys.Macro):
		m.state = StateMacro
		return m, nil

	case key.Matches(msg, Keys.Palette):
		items := make([]ui.PaletteItem, len(m.commands))
		for i, cmd := range m.commands {
			enabled := true
			if cmd.Enabled != nil {
				enabled = cmd.Enabled(&m)
			}
			items[i] = ui.PaletteItem{
				Name:    cmd.Name,
				Hotkey:  cmd.Hotkey,
				Enabled: enabled,
				Index:   i,
			}
		}
		m.state = StatePalette
		m.palette.Activate(items)
		return m, nil

	case key.Matches(msg, Keys.Escape) && (m.showHooks || m.showRawTranscript || m.showDiffs):
		m.showHooks = false
		m.showRawTranscript = false
		m.showDiffs = false
		m.detail.SetShowHooks(false)
		m.detail.SetShowRawTranscript(false)
		m.detail.SetShowDiffs(false)
		return m, nil

	case m.showHooks && msg.String() == "f":
		m.detail.CycleHookFilter()
		return m, nil

	case m.showRawTranscript && msg.String() == "e":
		if s, ok := m.sidebar.SelectedItem(); ok && s.SessionID != "" {
			return m, openTranscriptInEditor(m.origPane.Session, s.SessionID)
		}
		return m, nil

	case key.Matches(msg, Keys.Quit), key.Matches(msg, Keys.Escape):
		// At project level, esc drops back to session level instead of quitting
		if key.Matches(msg, Keys.Escape) && m.sidebar.SelectionLevel() == ui.LevelProject {
			m.sidebar.EnterSessionLevel()
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
			return m, nil
		}
		if m.origPane.Captured {
			tmux.SwitchToPaneQuiet(m.origPane.Session, m.origPane.Window, m.origPane.Pane)
		}
		return m, tea.Quit

	case key.Matches(msg, Keys.JumpBack):
		return m.doJump(m.jumpBack())

	case key.Matches(msg, Keys.JumpForward):
		target := m.jumpForward()
		// At the live head with no forward history: auto-jump to next target
		if target == "" {
			target = m.sidebar.AutoJumpTargetFromCursor()
		}
		return m.doJump(target)

	case m.showMinimap && key.Matches(msg, Keys.SpatialUp, Keys.SpatialDown, Keys.SpatialLeft, Keys.SpatialRight):
		var dir ui.SpatialDir
		dirName := ""
		switch {
		case key.Matches(msg, Keys.SpatialUp):
			dir = ui.DirUp
			dirName = "Up"
		case key.Matches(msg, Keys.SpatialDown):
			dir = ui.DirDown
			dirName = "Down"
		case key.Matches(msg, Keys.SpatialLeft):
			dir = ui.DirLeft
			dirName = "Left"
		case key.Matches(msg, Keys.SpatialRight):
			dir = ui.DirRight
			dirName = "Right"
		}
		m.minimap.LastNavDebug = "key=" + msg.String() + " dir=" + dirName
		paneID, isClaude := m.minimap.NavigateSpatial(dir)
		if paneID == "" {
			return m, nil
		}
		m.recordJump()
		if isClaude && m.sidebar.SelectByPaneID(paneID) {
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, tea.Batch(m.fetchForSelection(s, false)...)
			}
		} else if !isClaude {
			return m, m.focusNonClaudePane()
		}
		return m, nil

	case key.Matches(msg, Keys.NewSession):
		return m.execNewSession()

	case key.Matches(msg, Keys.NavLeft):
		// h: enter project-level navigation
		if m.sidebar.SelectionLevel() == ui.LevelSession {
			m.recordJump()
			m.sidebar.EnterProjectLevel()
			if s, ok := m.sidebar.SelectedProjectSession(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
		}
		return m, nil

	case key.Matches(msg, Keys.NavRight):
		// l: exit project-level, enter session-level
		if m.sidebar.SelectionLevel() == ui.LevelProject {
			m.recordJump()
			m.sidebar.EnterSessionLevel()
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
		}
		return m, nil

	case key.Matches(msg, Keys.Up):
		m.backlogScroll = 0
		if m.sidebar.SelectionLevel() == ui.LevelProject {
			m.sidebar.MoveUpProject()
			if s, ok := m.sidebar.SelectedProjectSession(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
			return m, nil
		}
		m.sidebar.MoveUp()
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(m.fetchForSelection(s, true)...)
		}
		return m, nil

	case key.Matches(msg, Keys.Down):
		m.backlogScroll = 0
		if m.sidebar.SelectionLevel() == ui.LevelProject {
			m.sidebar.MoveDownProject()
			if s, ok := m.sidebar.SelectedProjectSession(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
			return m, nil
		}
		m.sidebar.MoveDown()
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(m.fetchForSelection(s, true)...)
		}
		return m, nil

	case key.Matches(msg, Keys.GoBottom):
		m.backlogScroll = 0
		m.recordJump()
		m.sidebar.MoveToBottom()
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(m.fetchForSelection(s, true)...)
		}
		return m, nil

	case (m.showRawTranscript || m.showHooks) && msg.String() == " ":
		m.detail.ToggleExpand()
		return m, nil

	case key.Matches(msg, Keys.Enter):
		// Project level: enter drops into session level (same as l)
		if m.sidebar.SelectionLevel() == ui.LevelProject {
			m.sidebar.EnterSessionLevel()
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
			return m, nil
		}
		if m.showDiffs {
			m.detail.ToggleDiffExpand()
			return m, nil
		}
		// Minimap: Enter on non-Claude pane → switch to it directly
		if m.showMinimap {
			if info, ok := m.minimap.SelectedPaneInfo(); ok && !info.IsClaude {
				tmux.SwitchToPane(info.SessionName, info.WindowIndex, info.PaneIndex, info.PaneID)
				return m, tea.Quit
			}
		}
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.IsPhantom {
				// Dead Later → create new window + remove bookmark
				bookmarkID, cwd := s.LaterBookmarkID, s.CWD
				tmuxSession := m.origPane.Session
				return m, func() tea.Msg {
					if err := m.client.OpenLater(bookmarkID, cwd, tmuxSession); err != nil {
						return flashErrorMsg("open failed: " + err.Error())
					}
					return tea.QuitMsg{}
				}
			}
			// Live Later → auto-remove bookmark before switching
			if s.LaterBookmarkID != "" {
				m.client.Unlater(s.LaterBookmarkID) //nolint:errcheck
			}
			tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
			return m, tea.Quit
		}
		return m, nil

	case key.Matches(msg, Keys.PromptRelay):
		if _, ok := m.sidebar.SelectedItem(); ok {
			m.state = StatePromptRelay
			m.relay.Activate()
		}
		return m, nil

	case key.Matches(msg, Keys.PromptTag):
		return m.execTagRelay()

	case key.Matches(msg, Keys.Note):
		return m.execNoteEdit()

	case key.Matches(msg, Keys.Queue):
		return m.execQueue()

	case key.Matches(msg, Keys.Search):
		m.recordJump()
		// Exit project level when entering search
		if m.sidebar.SelectionLevel() == ui.LevelProject {
			m.sidebar.EnterSessionLevel()
		}
		m.state = StateSearching
		m.search.Activate()
		return m, nil

	case key.Matches(msg, Keys.Later):
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.LaterBookmarkID != "" {
				// Toggle off (unlater): stay on current item
				return m.execLater()
			}
			// Mark as later: execute + auto-jump to next session
			model, cmd := m.execLater()
			cmds := append([]tea.Cmd{cmd}, model.autoJump(s.PaneID)...)
			return model, tea.Batch(cmds...)
		}
		return m, nil

	case key.Matches(msg, Keys.LaterKill):
		return m.execLaterKill()

	case key.Matches(msg, Keys.ChatOutline):
		m.chatOutlineMode = nextChatOutlineMode(m.chatOutlineMode)
		savePrefString("chatOutlineMode", m.chatOutlineMode)
		m.detail.SetChatOutlineMode(m.chatOutlineMode)
		m.flashMsg = chatOutlineModeFlash(m.chatOutlineMode)
		m.flashIsError = false
		m.flashExpiry = time.Now().Add(3 * time.Second)
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })

	case key.Matches(msg, Keys.GroupMode):
		newMode := !m.sidebar.GroupByProject()
		m.sidebar.SetGroupByProject(newMode)
		savePrefBool("groupByProject", newMode)
		return m, nil

	case key.Matches(msg, Keys.LaterToggle):
		newVal := !m.sidebar.LaterExpanded()
		m.sidebar.SetLaterExpanded(newVal)
		savePrefBool("laterCollapsed", !newVal)
		if newVal {
			return m, m.setFlash("LATER expanded", false, 2*time.Second)
		}
		return m, m.setFlash("LATER collapsed", false, 2*time.Second)

	case key.Matches(msg, Keys.ClaudingToggle):
		newVal := !m.sidebar.ClaudingExpanded()
		m.sidebar.SetClaudingExpanded(newVal)
		savePrefBool("claudingCollapsed", !newVal)
		if newVal {
			return m, m.setFlash("CLAUDING expanded", false, 2*time.Second)
		}
		return m, m.setFlash("CLAUDING collapsed", false, 2*time.Second)

	case key.Matches(msg, Keys.BacklogToggle):
		newVal := !m.sidebar.BacklogExpanded()
		m.sidebar.SetBacklogExpanded(newVal)
		savePrefBool("backlogExpanded", newVal)
		if newVal {
			// Trigger discovery so backlog items appear immediately
			flashCmd := m.setFlash("BACKLOG expanded", false, 2*time.Second)
			discoverCmd := m.discoverBacklogs(m.sessions)
			return m, tea.Batch(flashCmd, discoverCmd)
		}
		return m, m.setFlash("BACKLOG collapsed", false, 2*time.Second)

	case key.Matches(msg, Keys.Minimap):
		m.showMinimap = !m.showMinimap
		savePrefBool("minimap", m.showMinimap)
		m.applyLayout()
		if m.showMinimap {
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, m.fetchMinimapData(s.TmuxSession)
			}
		} else {
			// Toggling off: restore list selection in case we were on a non-Claude pane
			m.sidebar.Reselect()
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, tea.Batch(
					capturePreview(s.PaneID),
					m.fetchChatOutline(s.PaneID, s.SessionID),
					m.fetchCachedSummary(s.PaneID, s.SessionID),
				)
			}
		}
		return m, nil

	case key.Matches(msg, Keys.Prefs):
		m, cmd := m.execPrefsEditor()
		return m, cmd

	case key.Matches(msg, Keys.MinimapMode):
		m.state = StateMinimapSettings
		m.flashMsg = minimapModeFlash(m.minimapMode, m.minimapMaxH, m.minimapCollapse)
		m.flashIsError = false
		m.flashExpiry = time.Now().Add(3 * time.Second)
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })

	case key.Matches(msg, Keys.Synthesize):
		if s, ok := m.sidebar.SelectedItem(); ok && s.SessionID != "" {
			m.sidebar.SetSummaryLoading(s.PaneID, true)
			return m, m.fetchSynthesize(s.PaneID, s.SessionID)
		}
		return m, nil

	case key.Matches(msg, Keys.SynthesizeAll):
		var latestPaneID string
		var latestTime time.Time
		for _, sess := range m.sessions {
			if sess.LastChanged.After(latestTime) {
				latestTime = sess.LastChanged
				latestPaneID = sess.PaneID
			}
		}
		for _, sess := range m.sessions {
			if sess.PaneID != latestPaneID && sess.SessionID != "" {
				m.sidebar.SetSummaryLoading(sess.PaneID, true)
			}
		}
		return m, m.fetchSynthesizeAll(latestPaneID)

	case key.Matches(msg, Keys.Rename):
		if s, ok := m.sidebar.SelectedItem(); ok && !m.renaming {
			m.renaming = true
			return m, m.fetchRenameWindow(s.TmuxSession, s.TmuxWindow)
		}
		return m, nil

	case key.Matches(msg, Keys.Kill):
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.IsPhantom && s.LaterBookmarkID != "" {
				// Phantom Later — no pane to kill, just remove bookmark
				bookmarkID := s.LaterBookmarkID
				return m, func() tea.Msg {
					claude.RemoveLaterBookmark(bookmarkID)
					return PaneKilledMsg{}
				}
			}
			m.state = StateKillConfirm
			m.killTargetPaneID = s.PaneID
			m.killTargetSessionID = s.SessionID
			m.killTargetPID = s.PID
			m.killTargetTitle = sessionDisplayTitle(s)
			m.killTargetAnimalIdx = s.AvatarAnimalIdx
			m.killTargetColorIdx = s.AvatarColorIdx
			m.killTargetBookmarkID = s.LaterBookmarkID
		}
		return m, nil

	case key.Matches(msg, Keys.Commit):
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.Status != claude.StatusUserTurn {
				return m, func() tea.Msg { return flashErrorMsg("session is busy") }
			}
			if s.CommitDonePending {
				return m, func() tea.Msg { return flashInfoMsg("commit already pending") }
			}
			paneID, sessionID, pid := s.PaneID, s.SessionID, s.PID
			cmds := []tea.Cmd{func() tea.Msg {
				if err := m.client.CommitOnly(paneID, sessionID, pid); err != nil {
					return flashErrorMsg("commit failed: " + err.Error())
				}
				return flashInfoMsg("commit started")
			}}
			cmds = append(cmds, m.autoJump(s.PaneID)...)
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case key.Matches(msg, Keys.CommitAndDone):
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.Status != claude.StatusUserTurn {
				return m, func() tea.Msg { return flashErrorMsg("session is busy") }
			}
			if s.CommitDonePending {
				return m, func() tea.Msg { return flashInfoMsg("commit+done already pending") }
			}
			paneID, sessionID, pid := s.PaneID, s.SessionID, s.PID
			cmds := []tea.Cmd{func() tea.Msg {
				if err := m.client.CommitAndDone(paneID, sessionID, pid); err != nil {
					return flashErrorMsg("commit+done failed: " + err.Error())
				}
				return flashInfoMsg("commit+done started")
			}}
			cmds = append(cmds, m.autoJump(s.PaneID)...)
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case key.Matches(msg, Keys.Debug):
		return m.execDebug()

	case key.Matches(msg, Keys.Help):
		m.showHelp = true
		return m, nil

	case key.Matches(msg, Keys.MessageLog):
		m.showMessageLog = true
		return m, nil

	case key.Matches(msg, Keys.Fullscreen):
		return m, reopenPopup(m.binaryPath, m.inFullscreenPopup)

	case key.Matches(msg, Keys.ListShrink):
		m.sidebarWidthPct = max(m.sidebarWidthPct-5, 10)
		m.applyLayout()
		savePrefInt("sidebarWidthPct", m.sidebarWidthPct)
		return m, nil

	case key.Matches(msg, Keys.ListGrow):
		m.sidebarWidthPct = min(m.sidebarWidthPct+5, 60)
		m.applyLayout()
		savePrefInt("sidebarWidthPct", m.sidebarWidthPct)
		return m, nil

	case key.Matches(msg, Keys.Refresh):
		// In daemon mode, sessions are pushed — but we can still force a preview refresh
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, capturePreview(s.PaneID)
		}
		return m, nil

	case key.Matches(msg, Keys.ScrollDown):
		m.detail.ScrollDown()
		return m, nil

	case key.Matches(msg, Keys.ScrollUp):
		m.detail.ScrollUp()
		return m, nil

	case key.Matches(msg, Keys.LineDown):
		m.detail.ScrollLines(1)
		return m, nil

	case key.Matches(msg, Keys.LineUp):
		m.detail.ScrollLines(-1)
		return m, nil

	case key.Matches(msg, Keys.PageDown):
		m.detail.ScrollPageDown()
		return m, nil

	case key.Matches(msg, Keys.PageUp):
		m.detail.ScrollPageUp()
		return m, nil

	case key.Matches(msg, Keys.MsgNext):
		if m.showHooks || m.showRawTranscript || m.showDiffs {
			m.detail.ScrollLines(1)
		} else {
			m.detail.NavigateMsg(1)
		}
		return m, nil

	case key.Matches(msg, Keys.MsgPrev):
		if m.showHooks || m.showRawTranscript || m.showDiffs {
			m.detail.ScrollLines(-1)
		} else {
			m.detail.NavigateMsg(-1)
		}
		return m, nil

	case msg.String() == "[" && m.showDiffs:
		m.detail.AdjustDiffSimThreshold(-0.05)
		return m, nil

	case msg.String() == "]" && m.showDiffs:
		m.detail.AdjustDiffSimThreshold(0.05)
		return m, nil
	}

	return m, nil
}
