package app

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/tmux"
	"github.com/huylenq/spirit/internal/ui"
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
	case msg.String() == "tab":
		return execOpenCopilot(&m)

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

	case msg.String() == "f":
		m.sidebar.ToggleFlagSelected()
		saveSidebarState(m.sidebar.ExportState())
		return m, nil

	case key.Matches(msg, Keys.FocusMode):
		newVal := !m.sidebar.FocusMode()
		m.sidebar.SetFocusMode(newVal)
		savePrefBool("focusMode", newVal)
		flashText := "FOCUS OFF"
		if newVal {
			flashText = fmt.Sprintf("FOCUS ON (%d flagged)", m.sidebar.FocusedCount())
		}
		return m, m.setFlash(flashText, false, 2*time.Second)

	case isSlotKey(msg.String()):
		n := slotKeyNum(msg.String())
		paneID := m.sidebar.PaneIDForSlot(n)
		if paneID == "" {
			return m, nil
		}
		return m.doJump(paneID)

	case isAltSlotKey(msg.String()):
		n := altSlotKeyNum(msg.String())
		if m.sidebar.BindSlot(n) {
			saveSidebarState(m.sidebar.ExportState())
			if m.sidebar.PaneIDForSlot(n) != "" {
				return m, m.setFlash(fmt.Sprintf("Bound to slot %d", n), false, 3*time.Second)
			}
			return m, m.setFlash(fmt.Sprintf("Unbound slot %d", n), false, 3*time.Second)
		}
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

	case !m.showDiffs && key.Matches(msg, Keys.JumpBack):
		return m.doJump(m.jumpBack())

	case !m.showDiffs && key.Matches(msg, Keys.JumpForward):
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
		if isClaude && m.selectByPaneID(paneID) {
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, tea.Batch(m.fetchForSelection(s, false)...)
			}
		} else if !isClaude {
			return m, m.focusNonClaudePane()
		}
		return m, nil

	case key.Matches(msg, Keys.NewSession):
		return m.execNewSession()

	case key.Matches(msg, Keys.NewSessionAtPath):
		return m.execNewSessionAtPath()

	case key.Matches(msg, Keys.NavLeft):
		if m.viewMode == ViewWorkQueue {
			m.workQueue.MoveLeft()
			return m, m.syncWorkQueueSelection()
		}
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
		if m.viewMode == ViewWorkQueue {
			m.workQueue.MoveRight()
			return m, m.syncWorkQueueSelection()
		}
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
		if m.viewMode == ViewWorkQueue {
			// In work queue: j/k navigate the horizontal queue (up=left, down=right)
			m.workQueue.MoveLeft()
			return m, m.syncWorkQueueSelection()
		}
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
		if m.viewMode == ViewWorkQueue {
			m.workQueue.MoveRight()
			return m, m.syncWorkQueueSelection()
		}
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
		if m.viewMode == ViewWorkQueue {
			m.workQueue.MoveToEnd()
			return m, m.syncWorkQueueSelection()
		}
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
				// Dead Later → create new window + remove Later record
				laterID, cwd := s.LaterID, s.CWD
				tmuxSession := m.origPane.Session
				return m, func() tea.Msg {
					if err := m.client.OpenLater(laterID, cwd, tmuxSession); err != nil {
						return flashErrorMsg("open failed: " + err.Error())
					}
					return tea.QuitMsg{}
				}
			}
			// Live Later → auto-remove Later record before switching
			if s.LaterID != "" {
				m.client.Unlater(s.LaterID) //nolint:errcheck
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
		return m.execLater()

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
		flashText := "LATER collapsed"
		if newVal {
			flashText = "LATER expanded"
		}
		return m, tea.Batch(m.setFlash(flashText, false, 2*time.Second), m.syncAllQuietAnim())

	case key.Matches(msg, Keys.ClaudingToggle):
		newVal := !m.sidebar.ClaudingExpanded()
		m.sidebar.SetClaudingExpanded(newVal)
		savePrefBool("claudingCollapsed", !newVal)
		flashText := "CLAUDING collapsed"
		if newVal {
			flashText = "CLAUDING expanded"
		}
		return m, tea.Batch(m.setFlash(flashText, false, 2*time.Second), m.syncAllQuietAnim())

	case key.Matches(msg, Keys.ViewMode):
		var syncCmd tea.Cmd
		if m.viewMode == ViewSidebar {
			m.viewMode = ViewWorkQueue
			syncCmd = m.reconcileWorkQueueSelection()
		} else {
			m.viewMode = ViewSidebar
		}
		savePrefString("viewMode", m.viewMode)
		m.applyLayout()
		return m, tea.Batch(syncCmd, m.setFlash("view: "+m.viewMode, false, 2*time.Second))

	case key.Matches(msg, Keys.AutoJumpToggle):
		newVal := !m.autoJumpOn
		savePrefBool("autoJump", newVal)
		m.autoJumpOn = newVal
		m.sidebar.ShowAutoJump = newVal
		m.autoJumpTextUntil = time.Now().Add(2 * time.Second)
		return m, nil

	case key.Matches(msg, Keys.BacklogToggle):
		newVal := !m.sidebar.BacklogExpanded()
		m.sidebar.SetBacklogExpanded(newVal)
		savePrefBool("backlogExpanded", newVal)
		if newVal {
			flashCmd := m.setFlash("BACKLOG expanded", false, 2*time.Second)
			discoverCmd := m.discoverBacklogs(m.sessions)
			return m, tea.Batch(flashCmd, discoverCmd, m.syncAllQuietAnim())
		}
		return m, tea.Batch(m.setFlash("BACKLOG collapsed", false, 2*time.Second), m.syncAllQuietAnim())

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
		m.state = StatePrefsEditor
		m.settingsCursor = 0
		return m, nil

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
			if s.IsPhantom && s.LaterID != "" {
				// Phantom Later — no pane to kill, just remove Later record
				laterID := s.LaterID
				return m, func() tea.Msg {
					claude.RemoveLaterRecord(laterID)
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
			m.killTargetLaterID = s.LaterID
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

	case key.Matches(msg, Keys.CommitSimplifyAndDone):
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.Status != claude.StatusUserTurn {
				return m, func() tea.Msg { return flashErrorMsg("session is busy") }
			}
			if s.CommitDonePending {
				return m, func() tea.Msg { return flashInfoMsg("commit+simplify+done already pending") }
			}
			paneID, sessionID, pid := s.PaneID, s.SessionID, s.PID
			cmds := []tea.Cmd{func() tea.Msg {
				if err := m.client.CommitSimplifyAndDone(paneID, sessionID, pid); err != nil {
					return flashErrorMsg("commit+simplify+done failed: " + err.Error())
				}
				return flashInfoMsg("commit+simplify+done started")
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
		m.sidebarWidthPct = max(m.sidebarWidthPct-5, minSidebarWidthPct)
		m.applyLayout()
		savePrefInt("sidebarWidthPct", m.sidebarWidthPct)
		return m, nil

	case key.Matches(msg, Keys.ListGrow):
		m.sidebarWidthPct = min(m.sidebarWidthPct+5, maxSidebarWidthPct)
		m.applyLayout()
		savePrefInt("sidebarWidthPct", m.sidebarWidthPct)
		return m, nil

	case key.Matches(msg, Keys.ApplyTitle):
		if s, ok := m.sidebar.SelectedItem(); ok && s.TitleDrift {
			return m, m.fetchApplyTitle(s.PaneID, s.SessionID)
		}
		return m, nil

	case key.Matches(msg, Keys.RenamePrompt):
		return m.execRenamePrompt()

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

// parseSlotDigit extracts the digit 1-9 from a key string at the given offset.
// Returns the digit and true, or 0 and false if not a valid slot key.
func parseSlotDigit(s string, expectedLen, digitOffset int) (int, bool) {
	if len(s) != expectedLen || s[digitOffset] < '1' || s[digitOffset] > '9' {
		return 0, false
	}
	return int(s[digitOffset] - '0'), true
}

func isSlotKey(s string) bool    { _, ok := parseSlotDigit(s, 1, 0); return ok }
func slotKeyNum(s string) int    { n, _ := parseSlotDigit(s, 1, 0); return n }
func isAltSlotKey(s string) bool { _, ok := parseSlotDigit(s, 5, 4); return ok && s[:4] == "alt+" }
func altSlotKeyNum(s string) int { n, _ := parseSlotDigit(s, 5, 4); return n }
