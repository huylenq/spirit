package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultOpenAIURL   = "http://127.0.0.1:8645/v1"
	defaultOpenAIModel = "grok-4.20-0309-non-reasoning"
	defaultOpenAIKey   = "spirit-via-hermes-xai-proxy"
	openaiCallTimeout   = 45 * time.Second
	openaiReachTimeout  = 300 * time.Millisecond
	openaiModelsTimeout = 1500 * time.Millisecond
	openaiReachTTL      = 60 * time.Second
)

type openaiChatRequest struct {
	Model          string              `json:"model"`
	Messages       []openaiChatMessage `json:"messages"`
	Temperature    float64             `json:"temperature"`
	MaxTokens      int                 `json:"max_tokens"`
	Stream         bool                `json:"stream"`
	ResponseFormat *openaiResponseFmt  `json:"response_format,omitempty"`
}

type openaiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponseFmt struct {
	Type string `json:"type"`
}

type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func openaiGenerateJSON(url, model, key, systemPrompt, userPrompt string) (string, error) {
	return openaiGenerate(url, model, key, systemPrompt, userPrompt, true)
}

func openaiGenerateText(url, model, key, systemPrompt, userPrompt string) (string, error) {
	return openaiGenerate(url, model, key, systemPrompt, userPrompt, false)
}

func openaiGenerate(url, model, key, systemPrompt, userPrompt string, jsonMode bool) (string, error) {
	req := openaiChatRequest{
		Model: model,
		Messages: []openaiChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.3,
		MaxTokens:   512,
		Stream:      false,
	}
	if jsonMode {
		req.ResponseFormat = &openaiResponseFmt{Type: "json_object"}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), openaiCallTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiJoin(url, "/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if auth := strings.TrimSpace(key); auth != "" {
		httpReq.Header.Set("Authorization", "Bearer "+auth)
	}

	resp, err := localHTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out openaiChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("openai decode: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return "", fmt.Errorf("openai: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai: empty choices")
	}
	return out.Choices[0].Message.Content, nil
}

var (
	openaiReachMu     sync.Mutex
	openaiReachUntil  time.Time
	openaiReachURL    string
	openaiReachResult bool
)

// openaiBackendReachable probes GET {base}/models with a short timeout and
// caches the result for 60s so auto-mode polls don't hammer a downed proxy.
func openaiBackendReachable(url string) bool {
	url = strings.TrimRight(url, "/")
	openaiReachMu.Lock()
	defer openaiReachMu.Unlock()
	if time.Now().Before(openaiReachUntil) && openaiReachURL == url {
		return openaiReachResult
	}

	ok := false
	if strings.HasSuffix(url, "/v1") {
		ok = openaiGETOK(strings.TrimSuffix(url, "/v1")+"/health", openaiReachTimeout)
	}
	if !ok {
		ok = openaiGETOK(openaiJoin(url, "/models"), openaiModelsTimeout)
	}
	openaiReachURL = url
	openaiReachResult = ok
	openaiReachUntil = time.Now().Add(openaiReachTTL)
	return ok
}

func openaiGETOK(url string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := localHTTP.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	return err == nil && resp != nil && resp.StatusCode == http.StatusOK
}

func openaiJoin(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func resetOpenAIReachCache() {
	openaiReachMu.Lock()
	defer openaiReachMu.Unlock()
	openaiReachUntil = time.Time{}
	openaiReachURL = ""
	openaiReachResult = false
}
