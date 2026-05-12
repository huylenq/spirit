package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// localChatRequest matches Ollama's /api/chat schema.
type localChatRequest struct {
	Model    string                 `json:"model"`
	Messages []localChatMessage     `json:"messages"`
	Stream   bool                   `json:"stream"`
	Think    bool                   `json:"think"` // Qwen3+: top-level disables reasoning. /no_think prefix is unreliable.
	Format   string                 `json:"format,omitempty"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type localChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type localChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error,omitempty"`
}

// localHTTP is owned by this package so we don't share http.DefaultClient
// with anything that might mutate its Transport or Timeout. Per-call
// timeouts come from request contexts.
var localHTTP = &http.Client{}

// Ollama /api/chat `format` values. Empty string means unconstrained output
// (free-form prose); "json" forces parseable JSON. Kept as named constants so
// call sites don't litter magic strings.
const (
	localFormatText = ""
	localFormatJSON = "json"
)

// localGenerateJSON sends a JSON-mode chat request to a local Ollama-compatible
// endpoint and returns the assistant content. Uses /api/chat with think:false
// and format:"json" so Qwen3-family models skip reasoning and emit parseable JSON.
func localGenerateJSON(url, model, systemPrompt, userPrompt string) (string, error) {
	return localGenerate(url, model, systemPrompt, userPrompt, localFormatJSON)
}

// localGenerateText is the plain-text counterpart of localGenerateJSON — same
// chat endpoint, no format constraint, so the model emits free-form prose.
func localGenerateText(url, model, systemPrompt, userPrompt string) (string, error) {
	return localGenerate(url, model, systemPrompt, userPrompt, localFormatText)
}

func localGenerate(url, model, systemPrompt, userPrompt, format string) (string, error) {
	req := localChatRequest{
		Model: model,
		Messages: []localChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:  false,
		Think:   false,
		Format:  format,
		Options: map[string]interface{}{"temperature": 0.3},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	// 90s cap covers cold-start model loading. Warm calls return in 1-3s.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := localHTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ollama HTTP %d", resp.StatusCode)
	}

	var out localChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ollama decode: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama: %s", out.Error)
	}
	return out.Message.Content, nil
}

var (
	reachCacheMu     sync.Mutex
	reachCacheUntil  time.Time
	reachCacheResult bool
)

// localBackendReachable probes /api/tags with a short timeout and caches the
// result for 60s so back-to-back calls don't repeatedly hit a downed server.
func localBackendReachable(url string) bool {
	reachCacheMu.Lock()
	defer reachCacheMu.Unlock()
	if time.Now().Before(reachCacheUntil) {
		return reachCacheResult
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url+"/api/tags", nil)
	if err != nil {
		reachCacheResult = false
		reachCacheUntil = time.Now().Add(60 * time.Second)
		return false
	}
	resp, err := localHTTP.Do(req)
	ok := err == nil && resp.StatusCode == 200
	if resp != nil {
		resp.Body.Close()
	}
	reachCacheResult = ok
	reachCacheUntil = time.Now().Add(60 * time.Second)
	return ok
}
