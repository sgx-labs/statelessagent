package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNewClient_AutoUsesOpenAICompatibleFromEmbeddingProvider(t *testing.T) {
	t.Setenv("SAME_CHAT_PROVIDER", "auto")
	t.Setenv("SAME_CHAT_MODEL", "")
	t.Setenv("SAME_CHAT_BASE_URL", "")
	t.Setenv("SAME_CHAT_API_KEY", "")
	t.Setenv("SAME_CHAT_FALLBACKS", "")
	t.Setenv("SAME_EMBED_PROVIDER", "openai-compatible")
	t.Setenv("SAME_EMBED_BASE_URL", "http://localhost:1234")
	t.Setenv("SAME_EMBED_API_KEY", "")

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Provider() != "openai-compatible" {
		t.Fatalf("expected openai-compatible provider, got %q", client.Provider())
	}
}

func TestOpenAICompatible_GenerateJSONFallsBackWhenResponseFormatUnsupported(t *testing.T) {
	client, err := newOpenAIClient(openAIClientConfig{
		Provider: "openai-compatible",
		BaseURL:  "http://localhost:1234",
		Model:    "llama3.2",
	})
	if err != nil {
		t.Fatalf("newOpenAIClient: %v", err)
	}
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			defer req.Body.Close()

			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			if _, ok := payload["response_format"]; ok {
				return jsonResponse(http.StatusBadRequest, `{"error":{"message":"response_format unsupported"}}`), nil
			}
			return jsonResponse(http.StatusOK, "{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"nodes\\\": []}\\n```\"}}]}"), nil
		}),
	}

	got, err := client.GenerateJSON("", "extract graph")
	if err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	if got != `{"nodes": []}` {
		t.Fatalf("unexpected JSON output: %q", got)
	}
}

func TestNewClient_ExplicitOpenAIRequiresAPIKey(t *testing.T) {
	t.Setenv("SAME_CHAT_PROVIDER", "openai")
	t.Setenv("SAME_CHAT_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	_, err := NewClient()
	if err == nil {
		t.Fatal("expected error for missing openai API key")
	}
	if !strings.Contains(err.Error(), "requires") {
		t.Fatalf("expected missing-key error, got: %v", err)
	}
}

func TestNewClientWithOptions_LocalOnlyRejectsRemoteProvider(t *testing.T) {
	t.Setenv("SAME_CHAT_PROVIDER", "openai")
	t.Setenv("SAME_CHAT_API_KEY", "sk-test")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("SAME_CHAT_BASE_URL", "")
	t.Setenv("SAME_CHAT_FALLBACKS", "")

	_, err := NewClientWithOptions(Options{LocalOnly: true})
	if err == nil {
		t.Fatal("expected error when local-only blocks remote-only provider")
	}
	if !strings.Contains(err.Error(), "no local chat provider configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewClientWithOptions_LocalOnlyAllowsLocalOpenAICompatible(t *testing.T) {
	t.Setenv("SAME_CHAT_PROVIDER", "openai-compatible")
	t.Setenv("SAME_CHAT_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("SAME_CHAT_MODEL", "llama3.2")
	t.Setenv("SAME_CHAT_API_KEY", "")
	t.Setenv("SAME_CHAT_FALLBACKS", "")

	client, err := NewClientWithOptions(Options{LocalOnly: true})
	if err != nil {
		t.Fatalf("NewClientWithOptions: %v", err)
	}
	if client.Provider() != "openai-compatible" {
		t.Fatalf("expected openai-compatible provider, got %q", client.Provider())
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
