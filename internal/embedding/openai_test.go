package embedding

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestNewOpenAIProvider_RequiresKeyForOpenAI(t *testing.T) {
	_, err := newOpenAIProvider(ProviderConfig{
		Provider: "openai",
		// No API key, no base URL → defaults to api.openai.com
	})
	if err == nil {
		t.Error("expected error when using openai without API key")
	}
}

func TestNewOpenAIProvider_NoKeyNeededForCompatible(t *testing.T) {
	p, err := newOpenAIProvider(ProviderConfig{
		Provider: "openai-compatible",
		BaseURL:  "http://localhost:8080",
		Model:    "nomic-embed-text",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.name != "openai-compatible" {
		t.Errorf("expected name openai-compatible, got %q", p.name)
	}
	if p.apiKey != "" {
		t.Errorf("expected empty API key, got %q", p.apiKey)
	}
	if p.dims != 0 {
		t.Errorf("expected 0 dims (server-determined), got %d", p.dims)
	}
}

func TestNewOpenAIProvider_CompatibleRequiresModel(t *testing.T) {
	_, err := newOpenAIProvider(ProviderConfig{
		Provider: "openai-compatible",
		BaseURL:  "http://localhost:8080",
		// No model
	})
	if err == nil {
		t.Error("expected error when using openai-compatible without model")
	}
}

func TestNewOpenAIProvider_CompatibleWithDims(t *testing.T) {
	p, err := newOpenAIProvider(ProviderConfig{
		Provider:   "openai-compatible",
		BaseURL:    "http://localhost:8080",
		Model:      "nomic-embed-text",
		Dimensions: 768,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.dims != 768 {
		t.Errorf("expected 768 dims, got %d", p.dims)
	}
}

func TestNewOpenAIProvider_CompatibleWithAPIKey(t *testing.T) {
	// Some local servers support optional API keys for auth
	p, err := newOpenAIProvider(ProviderConfig{
		Provider: "openai-compatible",
		BaseURL:  "http://localhost:8080",
		Model:    "nomic-embed-text",
		APIKey:   "local-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "local-key" {
		t.Errorf("expected API key to be passed through, got %q", p.apiKey)
	}
}

func TestOpenAIProvider_SkipsAuthHeader(t *testing.T) {
	var gotAuth string
	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := openaiEmbeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: make([]float32, 768), Index: 0},
			},
		}
		// Non-zero vector to pass validation
		for i := range resp.Data[0].Embedding {
			resp.Data[0].Embedding[i] = float32(i+1) * 0.001
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := newOpenAIProvider(ProviderConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		// No API key
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = p.GetEmbedding("test", "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestOpenAIProvider_SendsAuthHeader(t *testing.T) {
	var gotAuth string
	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := openaiEmbeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: make([]float32, 768), Index: 0},
			},
		}
		for i := range resp.Data[0].Embedding {
			resp.Data[0].Embedding[i] = float32(i+1) * 0.001
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := newOpenAIProvider(ProviderConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		APIKey:  "test-key-123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = p.GetEmbedding("test", "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer test-key-123" {
		t.Errorf("expected Bearer auth header, got %q", gotAuth)
	}
}

func TestOpenAIProvider_CompatibleEndToEnd(t *testing.T) {
	// Simulate a llama.cpp /v1/embeddings endpoint
	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected /v1/embeddings, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req openaiEmbeddingRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "nomic-embed-text" {
			t.Errorf("expected model nomic-embed-text, got %q", req.Model)
		}

		vec := make([]float32, 768)
		for i := range vec {
			vec[i] = float32(i+1) * 0.001
		}
		resp := openaiEmbeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: vec, Index: 0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := newOpenAIProvider(ProviderConfig{
		Provider:   "openai-compatible",
		BaseURL:    server.URL,
		Model:      "nomic-embed-text",
		Dimensions: 768,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vec, err := p.GetDocumentEmbedding("test document")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 768 {
		t.Errorf("expected 768 dims, got %d", len(vec))
	}
}

func TestNewProvider_OpenAICompatible(t *testing.T) {
	// Verify the factory routes "openai-compatible" correctly
	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vec := make([]float32, 768)
		for i := range vec {
			vec[i] = float32(i+1) * 0.001
		}
		resp := openaiEmbeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: vec, Index: 0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := NewProvider(ProviderConfig{
		Provider:   "openai-compatible",
		BaseURL:    server.URL,
		Model:      "test-model",
		Dimensions: 768,
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	if p.Name() != "openai-compatible" {
		t.Errorf("expected name openai-compatible, got %q", p.Name())
	}
}
