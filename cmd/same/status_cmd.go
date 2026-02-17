package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/setup"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func statusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "See what SAME is tracking in your project",
		Long: `Shows you the current state of SAME for your project:
  - How many notes are indexed
  - Which embedding/chat providers are active
  - Which AI tool integrations are active

Run this anytime to see if SAME is working.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

type runtimeStatus struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Error    string `json:"error,omitempty"`
}

type graphRuntimeStatus struct {
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Error    string `json:"error,omitempty"`
}

// StatusData represents the status information for JSON output.
type StatusData struct {
	Vault struct {
		Path       string  `json:"path"` // Just the directory name, not full absolute path
		Notes      int     `json:"notes"`
		Chunks     int     `json:"chunks"`
		IndexedAgo string  `json:"indexed_ago,omitempty"`
		DBSizeMB   float64 `json:"db_size_mb,omitempty"`
	} `json:"vault"`
	Embedding runtimeStatus      `json:"embedding"`
	Chat      runtimeStatus      `json:"chat"`
	Graph     graphRuntimeStatus `json:"graph"`
	// Deprecated legacy field kept for backward compatibility with existing consumers.
	Ollama *struct {
		Status string `json:"status"`
		Model  string `json:"model,omitempty"`
		Error  string `json:"error,omitempty"`
	} `json:"ollama,omitempty"`
	Hooks map[string]bool `json:"hooks"`
	MCP   struct {
		Installed bool `json:"installed"`
	} `json:"mcp"`
	Vaults struct {
		Count   int      `json:"count"`
		Default string   `json:"default,omitempty"`
		Names   []string `json:"names,omitempty"`
	} `json:"vaults"`
	Config struct {
		Loaded  string `json:"loaded,omitempty"` // Just the filename, not full path
		Warning string `json:"warning,omitempty"`
	} `json:"config"`
	Initialized bool `json:"initialized"`
}

func runStatus(jsonOut bool) error {
	vp := config.VaultPath()
	if vp == "" {
		return config.ErrNoVault
	}

	embeddingStatus := detectEmbeddingStatus()
	chatStatus := detectChatStatus()
	graphStatus := detectGraphStatus()

	if jsonOut {
		// Collect data for JSON output
		data := StatusData{}
		// SECURITY: Only include vault directory name, not full absolute path
		data.Vault.Path = filepath.Base(vp)
		data.Hooks = make(map[string]bool)
		data.Embedding = embeddingStatus
		data.Chat = chatStatus
		data.Graph = graphStatus

		db, err := store.Open()
		if err != nil {
			data.Initialized = false
		} else {
			defer db.Close()
			data.Initialized = true

			noteCount, _ := db.NoteCount()
			chunkCount, _ := db.ChunkCount()
			data.Vault.Notes = noteCount
			data.Vault.Chunks = chunkCount

			// Index age
			indexAge, _ := db.IndexAge()
			if indexAge > 0 {
				data.Vault.IndexedAgo = formatDuration(indexAge)
			}

			// DB size
			dbPath := config.DBPath()
			if info, err := os.Stat(dbPath); err == nil {
				data.Vault.DBSizeMB = float64(info.Size()) / (1024 * 1024)
			}
		}

		if embeddingStatus.Provider == "ollama" {
			data.Ollama = &struct {
				Status string `json:"status"`
				Model  string `json:"model,omitempty"`
				Error  string `json:"error,omitempty"`
			}{
				Status: embeddingStatus.Status,
				Model:  embeddingStatus.Model,
				Error:  embeddingStatus.Error,
			}
		}

		// Hooks
		hookStatus := setup.HooksInstalled(vp)
		hookNames := []string{
			"context-surfacing",
			"decision-extractor",
			"handoff-generator",
			"feedback-loop",
			"staleness-check",
			"session-bootstrap",
		}
		for _, name := range hookNames {
			data.Hooks[name] = hookStatus[name]
		}

		// MCP
		data.MCP.Installed = setup.MCPInstalled(vp)

		// Vaults
		reg := config.LoadRegistry()
		data.Vaults.Count = len(reg.Vaults)
		data.Vaults.Default = reg.Default
		for name := range reg.Vaults {
			data.Vaults.Names = append(data.Vaults.Names, name)
		}

		// Config
		if w := config.ConfigWarning(); w != "" {
			data.Config.Warning = w
		} else if cf := config.FindConfigFile(); cf != "" {
			// SECURITY: Only include filename, not full path
			data.Config.Loaded = filepath.Base(cf)
		}

		jsonBytes, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal JSON: %w", err)
		}
		fmt.Println(string(jsonBytes))
		return nil
	}

	// Human output
	cli.Header("SAME Status")

	cli.Section("Vault")
	fmt.Printf("  Path:    %s\n", cli.ShortenHome(vp))

	db, err := store.Open()
	if err != nil {
		fmt.Printf("  DB:      %snot initialized%s\n\n", cli.Red, cli.Reset)
		fmt.Printf("  Run 'same init' to set up.\n\n")
		return nil
	}
	defer db.Close()

	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	fmt.Printf("  Notes:   %s indexed\n", cli.FormatNumber(noteCount))
	fmt.Printf("  Chunks:  %s\n", cli.FormatNumber(chunkCount))

	indexAge, _ := db.IndexAge()
	if indexAge > 0 {
		fmt.Printf("  Indexed: %s ago\n", formatDuration(indexAge))
	}

	dbPath := config.DBPath()
	if info, err := os.Stat(dbPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		fmt.Printf("  DB:      %.1f MB\n", sizeMB)
	}

	cli.Section("AI Runtime")
	fmt.Printf("  Embedding: %s\n", summarizeRuntime(embeddingStatus))
	fmt.Printf("  Chat:      %s\n", summarizeRuntime(chatStatus))
	fmt.Printf("  Graph LLM: %s\n", summarizeGraphRuntime(graphStatus))
	if chatStatus.Status == "available" {
		if chatStatus.Model != "" {
			fmt.Printf("  Ask:       %s'same ask \"question\"' available (%s)%s\n", cli.Dim, chatStatus.Model, cli.Reset)
		} else {
			fmt.Printf("  Ask:       %s'same ask \"question\"' available%s\n", cli.Dim, cli.Reset)
		}
	}

	// Hooks
	cli.Section("Hooks")
	hookStatus := setup.HooksInstalled(vp)
	hookNames := []string{
		"context-surfacing",
		"decision-extractor",
		"handoff-generator",
		"feedback-loop",
		"staleness-check",
		"session-bootstrap",
	}
	activeHooks := 0
	for _, name := range hookNames {
		if hookStatus[name] {
			fmt.Printf("  %-24s %s✓ active%s\n", name, cli.Green, cli.Reset)
			activeHooks++
		} else {
			fmt.Printf("  %-24s %s- not configured%s\n", name, cli.Dim, cli.Reset)
		}
	}
	if activeHooks > 0 {
		fmt.Printf("\n  %sView recent activity: same log%s\n", cli.Dim, cli.Reset)
	}

	// MCP
	cli.Section("MCP")
	if setup.MCPInstalled(vp) {
		fmt.Printf("  registered in .mcp.json\n")
	} else {
		fmt.Printf("  %snot registered%s\n", cli.Dim, cli.Reset)
	}

	// Vaults — show active vault prominently, then registered list
	reg := config.LoadRegistry()

	activeName := ""
	for name, path := range reg.Vaults {
		if path == vp {
			activeName = name
			break
		}
	}

	activeSource := ""
	if config.VaultOverride != "" {
		activeSource = "via --vault flag"
	} else if cwd, err := os.Getwd(); err == nil && cwd == vp {
		activeSource = "auto-detected from cwd"
	} else if activeName != "" && activeName == reg.Default {
		activeSource = "registry default"
	}

	cli.Section("Vault")
	if activeName != "" {
		sourceHint := ""
		if activeSource != "" {
			sourceHint = fmt.Sprintf("  %s(%s)%s", cli.Dim, activeSource, cli.Reset)
		}
		fmt.Printf("  Active:  %s  %s%s\n", activeName, cli.ShortenHome(vp), sourceHint)
	} else {
		sourceHint := ""
		if activeSource != "" {
			sourceHint = fmt.Sprintf("  %s(%s)%s", cli.Dim, activeSource, cli.Reset)
		}
		fmt.Printf("  Active:  %s%s\n", cli.ShortenHome(vp), sourceHint)
	}

	if len(reg.Vaults) > 1 {
		cli.Section("Registered Vaults")
		names := make([]string, 0, len(reg.Vaults))
		for name := range reg.Vaults {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			path := reg.Vaults[name]
			marker := "  "
			if name == activeName {
				marker = cli.Green + "→ " + cli.Reset
			} else if name == reg.Default {
				marker = "* "
			}
			fmt.Printf("  %s%-18s %s\n", marker, name, cli.ShortenHome(path))
		}
		fmt.Printf("\n  %s(* = default · → = active · switch with 'same vault default <name>')%s\n", cli.Dim, cli.Reset)
	}

	cli.Section("Config")
	if w := config.ConfigWarning(); w != "" {
		fmt.Printf("  %sconfig error:%s %s\n", cli.Red, cli.Reset, w)
		fmt.Printf("  (using defaults — check .same/config.toml)\n")
	} else if config.FindConfigFile() != "" {
		fmt.Printf("  Loaded:  %s\n", cli.ShortenHome(config.FindConfigFile()))
	} else {
		fmt.Printf("  %sno config file%s (using defaults)\n", cli.Dim, cli.Reset)
	}

	cli.Footer()
	return nil
}

func detectEmbeddingStatus() runtimeStatus {
	ec := config.EmbeddingProviderConfig()
	provider := strings.TrimSpace(ec.Provider)
	if provider == "" {
		provider = "ollama"
	}

	st := runtimeStatus{Provider: provider}
	switch provider {
	case "none":
		st.Status = "disabled"
		return st
	case "ollama":
		st.Model = strings.TrimSpace(ec.Model)
		if st.Model == "" {
			st.Model = config.EmbeddingModel
		}
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			st.Status = "invalid_config"
			st.Error = sanitizeRuntimeError(err)
			return st
		}
		st.Endpoint = endpointHost(ollamaURL)
		httpClient := &http.Client{Timeout: time.Second}
		resp, err := httpClient.Get(ollamaURL + "/api/tags")
		if err != nil {
			st.Status = "not_running"
			st.Error = sanitizeRuntimeError(err)
			return st
		}
		resp.Body.Close()
		st.Status = "running"
		return st
	case "openai":
		st.Model = strings.TrimSpace(ec.Model)
		if st.Model == "" {
			st.Model = "text-embedding-3-small"
		}
		if strings.TrimSpace(ec.BaseURL) != "" {
			st.Endpoint = endpointHost(ec.BaseURL)
		} else {
			st.Endpoint = "api.openai.com"
		}
		if strings.TrimSpace(ec.APIKey) == "" {
			st.Status = "missing_api_key"
			st.Error = "set SAME_EMBED_API_KEY or OPENAI_API_KEY"
			return st
		}
		st.Status = "configured"
		return st
	case "openai-compatible":
		st.Model = strings.TrimSpace(ec.Model)
		if st.Model == "" {
			st.Status = "misconfigured"
			st.Error = "set SAME_EMBED_MODEL"
			return st
		}
		if strings.TrimSpace(ec.BaseURL) == "" {
			st.Status = "misconfigured"
			st.Error = "set SAME_EMBED_BASE_URL or embedding.base_url"
			return st
		}
		st.Endpoint = endpointHost(ec.BaseURL)
		st.Status = "configured"
		return st
	default:
		st.Status = "unknown_provider"
		st.Error = "unsupported embedding provider"
		return st
	}
}

func detectChatStatus() runtimeStatus {
	requested := strings.TrimSpace(os.Getenv("SAME_CHAT_PROVIDER"))
	if requested == "" {
		requested = "auto"
	}
	st := runtimeStatus{Provider: requested}

	client, err := llm.NewClient()
	if err != nil {
		errMsg := sanitizeRuntimeError(err)
		if strings.Contains(errMsg, "disabled") {
			st.Status = "disabled"
		} else {
			st.Status = "unavailable"
		}
		st.Error = errMsg
		return st
	}

	st.Provider = client.Provider()
	st.Status = "available"
	if model, modelErr := client.PickBestModel(); modelErr == nil {
		st.Model = model
	}
	return st
}

func detectGraphStatus() graphRuntimeStatus {
	mode := config.GraphLLMMode()
	st := graphRuntimeStatus{Mode: mode}

	switch mode {
	case "off":
		st.Status = "disabled"
		return st
	case "local-only":
		client, err := llm.NewClientWithOptions(llm.Options{LocalOnly: true})
		if err != nil {
			st.Status = "unavailable"
			st.Error = sanitizeRuntimeError(err)
			return st
		}
		st.Provider = client.Provider()
		st.Status = "enabled"
		if model, modelErr := client.PickBestModel(); modelErr == nil {
			st.Model = model
		}
		return st
	case "on":
		client, err := llm.NewClient()
		if err != nil {
			st.Status = "unavailable"
			st.Error = sanitizeRuntimeError(err)
			return st
		}
		st.Provider = client.Provider()
		st.Status = "enabled"
		if model, modelErr := client.PickBestModel(); modelErr == nil {
			st.Model = model
		}
		return st
	default:
		st.Mode = "off"
		st.Status = "disabled"
		return st
	}
}

func summarizeRuntime(st runtimeStatus) string {
	parts := []string{st.Provider, st.Status}
	if st.Model != "" {
		parts = append(parts, "model="+st.Model)
	}
	if st.Endpoint != "" {
		parts = append(parts, "endpoint="+st.Endpoint)
	}
	if st.Error != "" {
		parts = append(parts, "error="+st.Error)
	}
	return strings.Join(parts, " · ")
}

func summarizeGraphRuntime(st graphRuntimeStatus) string {
	parts := []string{"mode=" + st.Mode, st.Status}
	if st.Provider != "" {
		parts = append(parts, "provider="+st.Provider)
	}
	if st.Model != "" {
		parts = append(parts, "model="+st.Model)
	}
	if st.Error != "" {
		parts = append(parts, "error="+st.Error)
	}
	return strings.Join(parts, " · ")
}

func sanitizeRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 180 {
		return msg[:180] + "..."
	}
	return msg
}

func endpointHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "invalid"
	}
	host := strings.TrimSpace(u.Host)
	if host == "" {
		return "invalid"
	}
	return host
}
