package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
)

func (d *Daemon) sessionByPaneID(paneID string) (agent.Session, bool) {
	for _, session := range d.currentSessions() {
		if session.PaneID == paneID {
			return session, true
		}
	}
	return agent.Session{}, false
}

func (d *Daemon) sessionByID(sessionID string) (agent.Session, bool) {
	for _, session := range d.currentSessions() {
		if session.SessionID == sessionID {
			return session, true
		}
	}
	return agent.Session{}, false
}

func (d *Daemon) require(session agent.Session, capability agent.Capability) error {
	if err := d.providers.Require(session, capability); err != nil {
		return fmt.Errorf("%s: %w", session.Provider, err)
	}
	return nil
}

func (d *Daemon) requireSessionID(sessionID string, capability agent.Capability) error {
	session, ok := d.sessionByID(sessionID)
	if !ok {
		meta := claude.ReadSessionMeta(sessionID)
		session = agent.Session{Provider: meta.Provider, SessionID: sessionID}
	}
	return d.require(session, capability)
}

func (d *Daemon) sendPrompt(session agent.Session, text string) error {
	if err := d.require(session, agent.CapabilityRelayPrompt); err != nil {
		return err
	}
	provider, err := d.providers.Resolve(session.Provider)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return provider.Input(session).SendPrompt(ctx, session.PaneID, text)
}

func (d *Daemon) sendCommand(session agent.Session, command string) error {
	if err := d.require(session, agent.CapabilityRelayCommand); err != nil {
		return err
	}
	provider, err := d.providers.Resolve(session.Provider)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return provider.Input(session).SendCommand(ctx, session.PaneID, command)
}

func (d *Daemon) enableRemoteControl(session agent.Session) error {
	if err := d.require(session, agent.CapabilityRemoteControl); err != nil {
		return err
	}
	provider, err := d.providers.Resolve(session.Provider)
	if err != nil {
		return err
	}
	command, err := provider.Lifecycle(session).RemoteControlCommand(session)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return provider.Input(session).SendCommand(ctx, session.PaneID, command)
}
