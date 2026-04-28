package claude

import (
	"log"
	"sync"
	"time"
)

const (
	defaultLocalURL    = "http://localhost:11434"
	defaultLocalModel  = "qwen3.5:9b"
	lightweightCfgTTL  = 30 * time.Second
)

type lightweightConfig struct {
	Backend    string // "auto" | "local" | "claude"
	LocalURL   string
	LocalModel string
}

var (
	lwCfgMu    sync.Mutex
	lwCfg      lightweightConfig
	lwCfgUntil time.Time
)

func readLightweightConfig() lightweightConfig {
	lwCfgMu.Lock()
	defer lwCfgMu.Unlock()
	if time.Now().Before(lwCfgUntil) {
		return lwCfg
	}
	prefs := LoadPrefs()
	cfg := lightweightConfig{Backend: "auto", LocalURL: defaultLocalURL, LocalModel: defaultLocalModel}
	if v := prefs["lightweightBackend"]; v != "" {
		cfg.Backend = v
	}
	if v := prefs["lightweightLocalURL"]; v != "" {
		cfg.LocalURL = v
	}
	if v := prefs["lightweightLocalModel"]; v != "" {
		cfg.LocalModel = v
	}
	lwCfg = cfg
	lwCfgUntil = time.Now().Add(lightweightCfgTTL)
	return cfg
}

// LightweightJSON runs a JSON-output prompt through the configured lightweight
// backend (local Ollama or claude CLI). In "auto" mode, prefers local when
// reachable and silently falls through to claude on any local error.
func LightweightJSON(systemPrompt, userPrompt string) (string, error) {
	cfg := readLightweightConfig()
	useLocal := cfg.Backend == "local" ||
		(cfg.Backend != "claude" && localBackendReachable(cfg.LocalURL))

	if useLocal {
		out, err := localGenerateJSON(cfg.LocalURL, cfg.LocalModel, systemPrompt, userPrompt)
		if err == nil {
			return out, nil
		}
		if cfg.Backend == "local" {
			return "", err
		}
		log.Printf("lightweight: local backend failed, falling back to claude: %v", err)
	}

	cmd := newLightweightClaude(systemPrompt, userPrompt)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
