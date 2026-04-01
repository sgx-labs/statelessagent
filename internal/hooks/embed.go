package hooks

import (
	"fmt"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
)

// newEmbedProvider creates an embedding provider from the current config.
func newEmbedProvider() (embedding.Provider, error) {
	ec := config.EmbeddingProviderConfig()
	cfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		Dimensions: ec.Dimensions,
	}

	// Skip connection retries when the user hasn't explicitly configured
	// an embedding provider. This avoids 6-second retry delays on every
	// hook invocation when Ollama isn't installed.
	if !config.IsEmbeddingProviderExplicit() {
		cfg.SkipRetry = true
	}

	// For ollama, merge the base URL from the [ollama] section
	if cfg.Provider == "ollama" || cfg.Provider == "" {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			return nil, fmt.Errorf("can't connect to Ollama: %w", err)
		}
		cfg.BaseURL = ollamaURL
	}

	return embedding.NewProvider(cfg)
}
