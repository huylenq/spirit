package claude

import "github.com/huylenq/spirit/internal/agent"

// Compatibility aliases keep the current daemon protocol and Lua API stable
// while provider-neutral code migrates to internal/agent.
type Provider = agent.ProviderID

const (
	ProviderClaude = agent.ProviderClaude
	ProviderCodex  = agent.ProviderCodex
)

func ParseProvider(s string) Provider { return agent.ParseProviderID(s) }

type Status = agent.Status

const (
	StatusAgentTurn = agent.StatusAgentTurn
	StatusUserTurn  = agent.StatusUserTurn
)

func ParseStatus(s string) Status { return agent.ParseStatus(s) }

type Location = agent.Location
type ClaudeSession = agent.Session
type LaterRecord = agent.LaterRecord
