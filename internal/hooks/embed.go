package hooks

import (
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

	// For ollama, merge the base URL from the [ollama] section
	if cfg.Provider == "ollama" || cfg.Provider == "" {
		cfg.BaseURL = config.OllamaURL()
	}

	return embedding.NewProvider(cfg)
}
