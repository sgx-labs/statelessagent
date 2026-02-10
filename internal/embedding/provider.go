// Package embedding provides embedding providers for vector search.
//
// Supported providers:
//   - ollama (default): Local embeddings via Ollama. No API keys, fully private.
//   - openai: OpenAI text-embedding-3-small/large. Requires OPENAI_API_KEY.
package embedding

import "fmt"

// Provider generates embedding vectors from text.
// All providers must produce vectors of consistent dimensionality
// within a single index — switching providers requires reindexing.
type Provider interface {
	// GetEmbedding returns an embedding vector for the given text.
	// The purpose should be "document" for indexing or "query" for search.
	GetEmbedding(text string, purpose string) ([]float32, error)

	// GetDocumentEmbedding returns an embedding optimized for document storage.
	GetDocumentEmbedding(text string) ([]float32, error)

	// GetQueryEmbedding returns an embedding optimized for search queries.
	GetQueryEmbedding(text string) ([]float32, error)

	// Name returns the provider identifier (e.g., "ollama", "openai").
	Name() string

	// Dimensions returns the embedding vector dimensionality.
	Dimensions() int
}

// ProviderConfig holds embedding provider settings.
type ProviderConfig struct {
	Provider   string // "ollama" (default), "openai"
	Model      string // model name (provider-specific defaults if empty)
	APIKey     string // API key (required for cloud providers)
	BaseURL    string // base URL (provider-specific defaults if empty)
	Dimensions int    // vector dimensions (0 = provider default)
}

// NewProvider creates an embedding provider from the given config.
// Returns an error if the provider is unknown or misconfigured.
func NewProvider(cfg ProviderConfig) (Provider, error) {
	switch cfg.Provider {
	case "", "ollama":
		return newOllamaProvider(cfg)
	case "openai":
		return newOpenAIProvider(cfg)
	default:
		return nil, fmt.Errorf("unknown embedding provider: %q (supported: ollama, openai)", cfg.Provider)
	}
}
