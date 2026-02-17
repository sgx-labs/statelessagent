package llm

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/ollama"
)

// Client is a provider-agnostic interface for chat generation.
type Client interface {
	Generate(model, prompt string) (string, error)
	GenerateJSON(model, prompt string) (string, error)
	PickBestModel() (string, error)
	Provider() string
}

type clientConfig struct {
	Provider  string
	Model     string
	BaseURL   string
	APIKey    string
	Fallbacks []string
}

// Options controls chat client resolution behavior.
type Options struct {
	// LocalOnly restricts provider selection to local endpoints only.
	// Currently this allows:
	//   - ollama
	//   - openai-compatible with localhost/127.0.0.1/::1 base URL
	LocalOnly bool
}

// NewClient constructs a chat client using provider-aware defaults.
//
// Provider selection:
//   - SAME_CHAT_PROVIDER=ollama|openai|openai-compatible|none
//   - SAME_CHAT_PROVIDER=auto (default): follows embedding provider first,
//     then tries configured fallbacks.
func NewClient() (Client, error) {
	return NewClientWithOptions(Options{})
}

// NewClientWithOptions constructs a chat client using provider-aware defaults
// and optional resolution constraints.
func NewClientWithOptions(opts Options) (Client, error) {
	cfg := resolveClientConfig()
	providers := providerOrder(cfg)
	if opts.LocalOnly {
		providers = filterLocalProviders(providers, cfg)
	}

	var errs []string
	for _, provider := range providers {
		client, err := newClientForProvider(provider, cfg)
		if err == nil {
			return client, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", provider, err))
	}

	if len(errs) == 0 {
		if opts.LocalOnly {
			return nil, fmt.Errorf("no local chat provider configured")
		}
		return nil, fmt.Errorf("no chat provider configured")
	}
	return nil, fmt.Errorf("no chat provider available (%s)", strings.Join(errs, "; "))
}

func resolveClientConfig() clientConfig {
	ec := config.EmbeddingProviderConfig()

	cfg := clientConfig{
		Provider: strings.TrimSpace(os.Getenv("SAME_CHAT_PROVIDER")),
		Model:    strings.TrimSpace(os.Getenv("SAME_CHAT_MODEL")),
		BaseURL:  strings.TrimSpace(os.Getenv("SAME_CHAT_BASE_URL")),
		APIKey:   strings.TrimSpace(os.Getenv("SAME_CHAT_API_KEY")),
	}

	if cfg.Provider == "" {
		cfg.Provider = "auto"
	}

	if cfg.BaseURL == "" && (ec.Provider == "openai" || ec.Provider == "openai-compatible") {
		cfg.BaseURL = strings.TrimSpace(ec.BaseURL)
	}

	if cfg.APIKey == "" && (ec.Provider == "openai" || ec.Provider == "openai-compatible") {
		cfg.APIKey = strings.TrimSpace(ec.APIKey)
	}

	if v := strings.TrimSpace(os.Getenv("SAME_CHAT_FALLBACKS")); v != "" {
		for _, p := range strings.Split(v, ",") {
			p = normalizeProvider(p)
			if p != "" {
				cfg.Fallbacks = append(cfg.Fallbacks, p)
			}
		}
	}

	return cfg
}

func providerOrder(cfg clientConfig) []string {
	p := normalizeProvider(cfg.Provider)
	if p != "" && p != "auto" {
		return []string{p}
	}

	var order []string
	add := func(provider string) {
		provider = normalizeProvider(provider)
		if provider == "" || provider == "auto" {
			return
		}
		for _, existing := range order {
			if existing == provider {
				return
			}
		}
		order = append(order, provider)
	}

	// Prefer the currently configured embedding provider when in auto mode.
	ec := config.EmbeddingProvider()
	if ec != "none" {
		add(ec)
	}

	for _, fallback := range cfg.Fallbacks {
		add(fallback)
	}

	// Lightweight local fallback.
	add("ollama")

	// If explicit cloud/local OpenAI-compatible credentials are present,
	// make those routes available as fallback options too.
	if cfg.BaseURL != "" {
		add("openai-compatible")
	}
	if cfg.APIKey != "" || strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		add("openai")
	}

	return order
}

func newClientForProvider(provider string, cfg clientConfig) (Client, error) {
	switch normalizeProvider(provider) {
	case "ollama":
		url, err := config.OllamaURL()
		if err != nil {
			return nil, err
		}
		return &ollamaClient{client: ollama.NewClientWithURL(url)}, nil
	case "openai", "openai-compatible":
		baseURL := cfg.BaseURL
		// In auto mode, openai-compatible may inherit base_url from embedding config.
		// For the real OpenAI provider, default back to api.openai.com unless the
		// user explicitly set SAME_CHAT_BASE_URL.
		if normalizeProvider(provider) == "openai" && strings.TrimSpace(os.Getenv("SAME_CHAT_BASE_URL")) == "" {
			baseURL = ""
		}
		return newOpenAIClient(openAIClientConfig{
			Provider: provider,
			Model:    cfg.Model,
			BaseURL:  baseURL,
			APIKey:   cfg.APIKey,
		})
	case "none":
		return nil, fmt.Errorf("chat provider disabled (SAME_CHAT_PROVIDER=none)")
	default:
		return nil, fmt.Errorf("unknown chat provider: %q", provider)
	}
}

func normalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "", "auto":
		return "auto"
	case "ollama", "openai", "openai-compatible", "none":
		return p
	default:
		return p
	}
}

func filterLocalProviders(providers []string, cfg clientConfig) []string {
	local := make([]string, 0, len(providers))
	for _, provider := range providers {
		switch normalizeProvider(provider) {
		case "ollama":
			local = append(local, provider)
		case "openai-compatible":
			if isLoopbackBaseURL(cfg.BaseURL) {
				local = append(local, provider)
			}
		}
	}
	return local
}

func isLoopbackBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.TrimSpace(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type ollamaClient struct {
	client *ollama.Client
}

func (c *ollamaClient) Provider() string { return "ollama" }

func (c *ollamaClient) Generate(model, prompt string) (string, error) {
	return c.client.Generate(model, prompt)
}

func (c *ollamaClient) GenerateJSON(model, prompt string) (string, error) {
	return c.client.GenerateJSON(model, prompt)
}

func (c *ollamaClient) PickBestModel() (string, error) {
	return c.client.PickBestModel()
}
