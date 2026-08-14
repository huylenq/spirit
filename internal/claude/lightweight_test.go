package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunLightweight_OpenAIBackendHitsChatCompletions(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		hits++
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"json_object"`) {
			t.Errorf("expected json_object request, got %s", b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer srv.Close()

	out, err := runLightweight(lightweightConfig{
		Backend:     "openai",
		OpenAIURL:   srv.URL + "/v1",
		OpenAIModel: "grok-test",
		OpenAIKey:   "k",
	}, "sys", "user", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"ok":true}` {
		t.Fatalf("got %q", out)
	}
	if hits != 1 {
		t.Fatalf("hits: %d", hits)
	}
}

func TestRunLightweight_AutoUsesOpenAIWhenReachable(t *testing.T) {
	resetOpenAIReachCache()
	var openaiHits int
	openai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			openaiHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"from-openai"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer openai.Close()

	out, err := runLightweight(lightweightConfig{
		Backend:     "auto",
		OpenAIURL:   openai.URL + "/v1",
		OpenAIModel: "grok-test",
		OpenAIKey:   "k",
	}, "sys", "user", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "from-openai" {
		t.Fatalf("got %q", out)
	}
	if openaiHits != 1 {
		t.Fatalf("openaiHits=%d", openaiHits)
	}
}
