package claude

import (
	"log"
	"sync"
	"time"
)

const (
	defaultLocalURL   = "http://localhost:11434"
	defaultLocalModel = "qwen3.5:9b"
	lightweightCfgTTL = 30 * time.Second
)

type lightweightConfig struct {
	Backend     string // "auto" | "openai" | "local" | "claude"
	LocalURL    string
	LocalModel  string
	OpenAIURL   string
	OpenAIModel string
	OpenAIKey   string
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
	cfg := lightweightConfig{
		Backend:     "auto",
		LocalURL:    defaultLocalURL,
		LocalModel:  defaultLocalModel,
		OpenAIURL:   defaultOpenAIURL,
		OpenAIModel: defaultOpenAIModel,
		OpenAIKey:   defaultOpenAIKey,
	}
	if v := prefs["lightweightBackend"]; v != "" {
		cfg.Backend = v
	}
	if v := prefs["lightweightLocalURL"]; v != "" {
		cfg.LocalURL = v
	}
	if v := prefs["lightweightLocalModel"]; v != "" {
		cfg.LocalModel = v
	}
	if v := prefs["lightweightOpenAIURL"]; v != "" {
		cfg.OpenAIURL = v
	}
	if v := prefs["lightweightOpenAIModel"]; v != "" {
		cfg.OpenAIModel = v
	}
	if v := prefs["lightweightOpenAIKey"]; v != "" {
		cfg.OpenAIKey = v
	}
	lwCfg = cfg
	lwCfgUntil = time.Now().Add(lightweightCfgTTL)
	return cfg
}

// LightweightJSON runs a JSON-output prompt through the configured lightweight
// backend. auto = Hermes OpenAI-compat proxy if reachable, else claude CLI.
// openai / local / claude pin one backend with no fallback.
func LightweightJSON(systemPrompt, userPrompt string) (string, error) {
	return lightweightCall(systemPrompt, userPrompt, true)
}

// LightweightText is the plaintext counterpart of LightweightJSON. The local
// backend skips Ollama's format:"json" constraint; the OpenAI-compat and
// claude CLI paths are unchanged (the system prompt alone steers output shape
// there, except OpenAI JSON mode which sets response_format).
func LightweightText(systemPrompt, userPrompt string) (string, error) {
	return lightweightCall(systemPrompt, userPrompt, false)
}

func lightweightCall(systemPrompt, userPrompt string, jsonMode bool) (string, error) {
	return runLightweight(readLightweightConfig(), systemPrompt, userPrompt, jsonMode)
}

func runLightweight(cfg lightweightConfig, systemPrompt, userPrompt string, jsonMode bool) (string, error) {
	tryOpenAI := cfg.Backend == "openai" ||
		(cfg.Backend == "auto" && openaiBackendReachable(cfg.OpenAIURL))
	if tryOpenAI {
		var out string
		var err error
		if jsonMode {
			out, err = openaiGenerateJSON(cfg.OpenAIURL, cfg.OpenAIModel, cfg.OpenAIKey, systemPrompt, userPrompt)
		} else {
			out, err = openaiGenerateText(cfg.OpenAIURL, cfg.OpenAIModel, cfg.OpenAIKey, systemPrompt, userPrompt)
		}
		if err == nil {
			return out, nil
		}
		if cfg.Backend == "openai" {
			return "", err
		}
		log.Printf("lightweight: openai backend failed, falling back to claude: %v", err)
	}

	if cfg.Backend == "local" {
		var out string
		var err error
		if jsonMode {
			out, err = localGenerateJSON(cfg.LocalURL, cfg.LocalModel, systemPrompt, userPrompt)
		} else {
			out, err = localGenerateText(cfg.LocalURL, cfg.LocalModel, systemPrompt, userPrompt)
		}
		if err == nil {
			return out, nil
		}
		return "", err
	}

	cmd := newLightweightClaude(systemPrompt, userPrompt)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
