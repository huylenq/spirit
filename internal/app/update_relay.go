package app

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (m Model) handleKeyPromptRelay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.relay.Deactivate()
		return m, nil
	case key.Matches(msg, Keys.Enter):
		bangMode := m.relay.IsBangMode()
		var val string
		if bangMode {
			val = m.relay.ConfirmRaw()
		} else {
			val = m.relay.Confirm()
		}
		m.state = StateNormal
		if val == "" {
			return m, nil
		}
		if s, ok := m.sidebar.SelectedItem(); ok {
			cmds := []tea.Cmd{sendPromptRelay(s.PaneID, val)}
			cmds = append(cmds, m.autoJump(s.PaneID)...)
			return m, tea.Batch(cmds...)
		}
		return m, nil
	default:
		// Bang mode: ! as first character sends ! keystroke to pane (bash mode) and stays in relay
		if msg.String() == "!" && m.relay.Value() == "" {
			m.relay.EnterBangMode()
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, sendBangKey(s.PaneID)
			}
			return m, nil
		}
		ti := m.relay.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		return m, cmd
	}
}

func (m Model) handleKeyLaterWait(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.laterRelay.Deactivate()
		return m, nil
	case key.Matches(msg, Keys.Enter):
		wait := m.laterRelay.Confirm()
		m.state = StateNormal
		// Validate duration if non-empty
		if wait != "" {
			if _, err := time.ParseDuration(wait); err != nil {
				return m, m.setFlash("invalid duration: "+wait, true, 3*time.Second)
			}
		}
		return m.execLaterConfirm(wait)
	default:
		ti := m.laterRelay.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		return m, cmd
	}
}

func (m Model) handleKeyQueueRelay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		m.state = StateNormal
		m.queueRelay.Deactivate()
		return m, nil
	}
	queueLen := len(s.QueuePending)
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.queueRelay.Deactivate()
		return m, nil
	case key.Matches(msg, Keys.Enter):
		val := m.queueRelay.Confirm()
		if val == "" {
			m.state = StateNormal
			m.queueRelay.Deactivate()
			return m, nil
		}
		paneID, sessionID := s.PaneID, s.SessionID
		m.state = StateNormal
		return m, func() tea.Msg {
			if err := m.client.Queue(paneID, sessionID, val); err != nil {
				return flashErrorMsg("queue failed: " + err.Error())
			}
			return flashInfoMsg("message queued")
		}
	case msg.String() == "down", msg.String() == "ctrl+j":
		// Navigate down into queue items (inline input is above the queue list)
		if m.queueCursor == -1 && queueLen > 0 {
			m.queueCursor = 0
		} else if m.queueCursor >= 0 && m.queueCursor < queueLen-1 {
			m.queueCursor++
		}
		return m, nil
	case msg.String() == "up", msg.String() == "ctrl+k":
		// Navigate up through queue items, back to input at top
		if m.queueCursor > 0 {
			m.queueCursor--
		} else if m.queueCursor == 0 {
			m.queueCursor = -1 // back to text input
		}
		return m, nil
	case msg.String() == "ctrl+d":
		// Remove highlighted item
		if m.queueCursor >= 0 && m.queueCursor < queueLen {
			sessionID, idx := s.SessionID, m.queueCursor
			// Adjust cursor after removal
			if m.queueCursor >= queueLen-1 {
				m.queueCursor = queueLen - 2 // -1 if was last item
			}
			return m, func() tea.Msg {
				if err := m.client.CancelQueueItem(sessionID, idx); err != nil {
					return flashErrorMsg("remove failed: " + err.Error())
				}
				return flashInfoMsg("item removed")
			}
		}
		return m, nil
	default:
		// Forward to text input only when not highlighting an item
		if m.queueCursor == -1 {
			// Bang mode: ! as first character changes prompt icon
			if msg.String() == "!" && m.queueRelay.Value() == "" {
				m.queueRelay.EnterBangMode()
				return m, nil
			}
			ti := m.queueRelay.TextInput()
			newTI, cmd := ti.Update(msg)
			*ti = newTI
			return m, cmd
		}
		return m, nil
	}
}

// applyTagsCmd sends updated tags to the daemon and shows a flash confirmation.
func (m Model) applyTagsCmd(sessionID string, tags []string, flash string) tea.Cmd {
	client := m.client
	return tea.Batch(
		func() tea.Msg {
			client.SetTags(sessionID, tags) //nolint:errcheck
			return nil
		},
		m.setFlash(flash, false, 3*time.Second),
	)
}

func (m Model) handleKeyTagRelay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Backlog tag relay: toggle inline #tags in body text
	if b, ok := m.sidebar.SelectedBacklog(); ok {
		return m.handleKeyTagRelayBacklog(msg, b)
	}

	s, ok := m.sidebar.SelectedItem()
	if !ok {
		m.state = StateNormal
		m.tagRelay.Deactivate()
		return m, nil
	}
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.tagRelay.Deactivate()
		return m, nil
	case key.Matches(msg, Keys.Enter):
		val := m.tagRelay.Confirm()
		m.state = StateNormal
		if val == "" {
			return m, nil
		}
		// Toggle tag: add if absent, remove if present
		tags := make([]string, len(s.Tags))
		copy(tags, s.Tags)
		found := false
		for i, t := range tags {
			if t == val {
				tags = append(tags[:i], tags[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			tags = append(tags, val)
		}
		flash := "+#" + val
		if found {
			flash = "-#" + val
		}
		return m, m.applyTagsCmd(s.SessionID, tags, flash)
	case msg.String() == "backspace" && m.tagRelay.Value() == "":
		// Pop last tag on backspace with empty input
		if len(s.Tags) == 0 {
			return m, nil
		}
		lastTag := s.Tags[len(s.Tags)-1]
		tags := s.Tags[:len(s.Tags)-1:len(s.Tags)-1] // cap to prevent backing-array reuse
		return m, m.applyTagsCmd(s.SessionID, tags, "-#"+lastTag)
	default:
		ti := m.tagRelay.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		return m, cmd
	}
}

// saveBacklogTagCmd writes updated backlog body to disk, flashes feedback, then chains re-discovery.
func (m Model) saveBacklogTagCmd(id, cwd, newBody, flash string) tea.Cmd {
	return tea.Batch(
		func() tea.Msg {
			if err := claude.WriteBacklog(cwd, claude.Backlog{ID: id, Body: newBody}); err != nil {
				return flashErrorMsg("save backlog: " + err.Error())
			}
			return backlogWrittenMsg{}
		},
		m.setFlash(flash, false, 3*time.Second),
	)
}

func (m Model) handleKeyTagRelayBacklog(msg tea.KeyMsg, b claude.Backlog) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.tagRelay.Deactivate()
		return m, nil
	case key.Matches(msg, Keys.Enter):
		val := m.tagRelay.Confirm()
		m.state = StateNormal
		if val == "" {
			return m, nil
		}
		newBody, removed := claude.ToggleBacklogTag(b.Body, val)
		flash := "+#" + val
		if removed {
			flash = "-#" + val
		}
		return m, m.saveBacklogTagCmd(b.ID, b.CWD, newBody, flash)
	case msg.String() == "backspace" && m.tagRelay.Value() == "":
		if len(b.Tags) == 0 {
			return m, nil
		}
		lastTag := b.Tags[len(b.Tags)-1]
		newBody, _ := claude.ToggleBacklogTag(b.Body, lastTag)
		return m, m.saveBacklogTagCmd(b.ID, b.CWD, newBody, "-#"+lastTag)
	default:
		ti := m.tagRelay.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		return m, cmd
	}
}
