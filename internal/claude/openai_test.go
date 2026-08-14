package claude

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestOpenAIGenerateJSON_PostsChatCompletionsAndReturnsContent(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"headline\":\"fix auth crash\"}"}}]}`))
	}))
	defer srv.Close()

	out, err := openaiGenerateJSON(srv.URL+"/v1", "grok-test", "spirit-key", "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"headline":"fix auth crash"}` {
		t.Fatalf("content: got %q", out)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path: got %q", gotPath)
	}
	if gotAuth != "Bearer spirit-key" {
		t.Fatalf("auth: got %q", gotAuth)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("request json: %v\n%s", err, gotBody)
	}
	if payload["model"] != "grok-test" {
		t.Fatalf("model: got %#v", payload["model"])
	}
	rf, _ := payload["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Fatalf("response_format: got %#v", payload["response_format"])
	}
}

func TestOpenAIGenerateText_OmitsJSONResponseFormat(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"sidebar toggle"}}]}`))
	}))
	defer srv.Close()

	out, err := openaiGenerateText(srv.URL+"/v1", "grok-test", "k", "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "sidebar toggle" {
		t.Fatalf("content: got %q", out)
	}
	if strings.Contains(gotBody, "response_format") {
		t.Fatalf("text mode should omit response_format: %s", gotBody)
	}
}

func TestOpenAIGenerateJSON_HTTPErrorIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := openaiGenerateJSON(srv.URL+"/v1", "missing", "k", "sys", "user")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected HTTP 404 error, got %v", err)
	}
}

func TestOpenAIBackendReachable_ModelsOK(t *testing.T) {
	resetOpenAIReachCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	if !openaiBackendReachable(srv.URL + "/v1") {
		t.Fatal("expected reachable")
	}
}

func TestOpenAIBackendReachable_HealthOnParent(t *testing.T) {
	resetOpenAIReachCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	if !openaiBackendReachable(srv.URL + "/v1") {
		t.Fatal("expected reachable via /health")
	}
}

func TestOpenAIBackendReachable_Down(t *testing.T) {
	if openaiBackendReachable("http://127.0.0.1:1/v1") {
		t.Fatal("expected unreachable")
	}
}

func TestLiveHermesProxyJSON(t *testing.T) {
	if os.Getenv("SPIRIT_LIVE_OPENAI") == "" {
		t.Skip("set SPIRIT_LIVE_OPENAI=1 to hit the local Hermes proxy")
	}
	resetOpenAIReachCache()
	if !openaiBackendReachable(defaultOpenAIURL) {
		t.Fatalf("proxy not reachable at %s", defaultOpenAIURL)
	}
	out, err := openaiGenerateJSON(
		defaultOpenAIURL,
		defaultOpenAIModel,
		defaultOpenAIKey,
		"Output ONLY valid JSON. No markdown.",
		`Reply with exactly {"ok":true,"headline":"sidebar toggle"}`,
	)
	if err != nil {
		t.Fatalf("live proxy: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("unexpected live body: %q", out)
	}
}
