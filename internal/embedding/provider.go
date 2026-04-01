// Package embedding provides embedding providers for vector search.
//
// Supported providers:
//   - ollama (default): Local embeddings via Ollama. No API keys, fully private.
//   - openai: OpenAI text-embedding-3-small/large. Requires OPENAI_API_KEY.
//   - openai-compatible: Any server that exposes OpenAI-compatible /v1/embeddings
//     (llama.cpp, VLLM, LM Studio, etc.). API key optional.
package embedding

import (
	"fmt"
	"math"
)

// Provider generates embedding vectors from text.
// All providers must produce vectors of consistent dimensionality
// within a single index — switching providers requires reindexing.
type Provider interface {
	// GetEmbedding returns an embedding vector for the given text.
	// The purpose should be "document" for indexing or "query" for search.
	GetEmbedding(text string, purpose string) ([]float32, error)

	// GetDocumentEmbedding returns an embedding optimized for document storage.
	GetDocumentEmbedding(text string) ([]float32, error)

	// GetDocumentEmbeddings returns embeddings for multiple texts in a batch.
	// Providers that support native batching should override this for efficiency.
	GetDocumentEmbeddings(texts []string) ([][]float32, error)

	// GetQueryEmbedding returns an embedding optimized for search queries.
	GetQueryEmbedding(text string) ([]float32, error)

	// Name returns the provider identifier (e.g., "ollama", "openai").
	Name() string

	// Model returns the embedding model name (e.g., "nomic-embed-text").
	Model() string

	// Dimensions returns the embedding vector dimensionality.
	Dimensions() int
}

// ProviderConfig holds embedding provider settings.
type ProviderConfig struct {
	Provider   string // "ollama" (default), "openai", "openai-compatible", "none"
	Model      string // model name (provider-specific defaults if empty)
	APIKey     string // API key (required for cloud providers)
	BaseURL    string // base URL (provider-specific defaults if empty)
	Dimensions int    // vector dimensions (0 = provider default)

	// SkipRetry disables connection retries for providers that support them.
	// When true, network errors fail immediately instead of retrying.
	// Used to avoid 6-second retry delays when the user hasn't configured
	// this provider (e.g., Ollama is not installed).
	SkipRetry bool
}

// NewProvider creates an embedding provider from the given config.
// Returns an error if the provider is unknown or misconfigured.
func NewProvider(cfg ProviderConfig) (Provider, error) {
	switch cfg.Provider {
	case "", "ollama":
		return newOllamaProvider(cfg)
	case "openai", "openai-compatible":
		return newOpenAIProvider(cfg)
	case "none":
		return nil, fmt.Errorf("embedding provider is \"none\" (keyword-only mode)")
	default:
		return nil, fmt.Errorf("unknown embedding provider: %q (supported: ollama, openai, openai-compatible, none)", cfg.Provider)
	}
}

// Unloader is an optional interface that providers can implement to release
// resources (e.g., unload a model from GPU memory) after bulk operations.
type Unloader interface {
	// UnloadModel tells the provider to release the model from memory.
	// Best-effort: errors are logged but not fatal.
	UnloadModel()
}

// validateEmbedding checks that a returned embedding vector is valid:
//   - correct number of dimensions (if expectedDims > 0)
//   - not all zeros (which indicates a provider error)
//
// Returns an error describing the problem, or nil if valid.
func validateEmbedding(vec []float32, expectedDims int) error {
	if expectedDims > 0 && len(vec) != expectedDims {
		return fmt.Errorf("embedding dimension mismatch: your embedding model changed. Run 'same reindex --force' to rebuild the index")
	}
	// Check for all-zero vector (indicates provider returned garbage)
	allZero := true
	for _, v := range vec {
		if math.Float32bits(v) != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return fmt.Errorf("embedding is all zeros (provider returned invalid vector)")
	}
	return nil
}
