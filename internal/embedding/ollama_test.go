package embedding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateLocalhostOnly(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"localhost", "http://localhost:11434", false},
		{"127.0.0.1", "http://127.0.0.1:11434", false},
		{"ipv6", "http://[::1]:11434", false},
		{"remote host", "http://example.com:11434", true},
		{"remote IP", "http://192.168.1.100:11434", true},
		{"invalid URL", "://bad", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLocalhostOnly(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateLocalhostOnly(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestNewOllamaProvider_RejectsRemote(t *testing.T) {
	_, err := newOllamaProvider(ProviderConfig{
		BaseURL: "http://remote-server.example.com:11434",
	})
	if err == nil {
		t.Error("expected error for remote URL")
	}
}

func TestNewOllamaProvider_DefaultModel(t *testing.T) {
	p, err := newOllamaProvider(ProviderConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "nomic-embed-text" {
		t.Errorf("expected default model, got %q", p.model)
	}
	if p.dims != 768 {
		t.Errorf("expected 768 dims, got %d", p.dims)
	}
}

func TestNewOllamaProvider_CustomModel(t *testing.T) {
	p, err := newOllamaProvider(ProviderConfig{
		Model: "mxbai-embed-large",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "mxbai-embed-large" {
		t.Errorf("expected mxbai-embed-large, got %q", p.model)
	}
	if p.dims != 1024 {
		t.Errorf("expected 1024 dims, got %d", p.dims)
	}
}

func TestGetEmbedding_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaEmbeddingResponse{
			Embedding: make([]float32, 768),
		}
		for i := range resp.Embedding {
			resp.Embedding[i] = float32(i) * 0.001
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := newOllamaProvider(ProviderConfig{
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vec, err := p.GetEmbedding("test text", "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 768 {
		t.Errorf("expected 768 dimensions, got %d", len(vec))
	}
}

func TestGetEmbedding_4xxNoRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	p, err := newOllamaProvider(ProviderConfig{
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = p.GetEmbedding("test", "query")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry on 4xx), got %d", attempts)
	}
}

func TestGetEmbedding_5xxRetries(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("service unavailable"))
			return
		}
		// Succeed on third attempt
		resp := ollamaEmbeddingResponse{
			Embedding: make([]float32, 768),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := newOllamaProvider(ProviderConfig{
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vec, err := p.GetEmbedding("test", "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 768 {
		t.Errorf("expected 768 dims, got %d", len(vec))
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestGetEmbedding_EmptyEmbedding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaEmbeddingResponse{Embedding: nil}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := newOllamaProvider(ProviderConfig{
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = p.GetEmbedding("test", "query")
	if err == nil {
		t.Fatal("expected error for empty embedding")
	}
}

func TestGetEmbedding_500WithLongText_Truncates(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var req ollamaEmbeddingRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Simulate context overflow: reject prompts > 8000 chars, accept shorter.
		// GetEmbedding truncation halves the text on 500, so 10000 → 5000 → succeeds.
		if len(req.Prompt) > 8000 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("context too long"))
			return
		}

		resp := ollamaEmbeddingResponse{
			Embedding: make([]float32, 768),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := newOllamaProvider(ProviderConfig{
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 10000 chars > 3000 threshold for truncation
	longText := strings.Repeat("word ", 2000)
	vec, err := p.GetEmbedding(longText, "document")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 768 {
		t.Errorf("expected 768 dims, got %d", len(vec))
	}
}

func TestHttpError_IsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		retryable bool
	}{
		{"network error", 0, true},
		{"server error", 500, true},
		{"service unavailable", 503, true},
		{"bad request", 400, false},
		{"not found", 404, false},
		{"unauthorized", 401, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &httpError{StatusCode: tt.status, Body: "test"}
			if e.isRetryable() != tt.retryable {
				t.Errorf("httpError{%d}.isRetryable() = %v, want %v", tt.status, e.isRetryable(), tt.retryable)
			}
		})
	}
}

func TestOllamaDefaultDims(t *testing.T) {
	tests := []struct {
		model string
		dims  int
	}{
		{"nomic-embed-text", 768},
		{"mxbai-embed-large", 1024},
		{"all-minilm", 384},
		{"snowflake-arctic-embed", 1024},
		{"unknown-model", 768},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := ollamaDefaultDims(tt.model)
			if got != tt.dims {
				t.Errorf("ollamaDefaultDims(%q) = %d, want %d", tt.model, got, tt.dims)
			}
		})
	}
}
