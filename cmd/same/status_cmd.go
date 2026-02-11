package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
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
  - Whether Ollama is running
  - Which AI tool integrations are active

Run this anytime to see if SAME is working.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

// StatusData represents the status information for JSON output
type StatusData struct {
	Vault struct {
		Path       string  `json:"path"` // Just the directory name, not full absolute path
		Notes      int     `json:"notes"`
		Chunks     int     `json:"chunks"`
		IndexedAgo string  `json:"indexed_ago,omitempty"`
		DBSizeMB   float64 `json:"db_size_mb,omitempty"`
	} `json:"vault"`
	Ollama struct {
		Status string `json:"status"` // "running", "not_running", "invalid_url"
		Model  string `json:"model,omitempty"`
		Error  string `json:"error,omitempty"`
	} `json:"ollama"`
	Hooks map[string]bool `json:"hooks"`
	MCP   struct {
		Installed bool `json:"installed"`
	} `json:"mcp"`
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

	if jsonOut {
		// Collect data for JSON output
		data := StatusData{}
		// SECURITY: Only include vault directory name, not full absolute path
		data.Vault.Path = filepath.Base(vp)
		data.Hooks = make(map[string]bool)

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

		// Ollama status
		ollamaURL, ollamaErr := config.OllamaURL()
		if ollamaErr != nil {
			data.Ollama.Status = "invalid_url"
			// SECURITY: Sanitize error message to avoid leaking URL details
			if strings.Contains(ollamaErr.Error(), "invalid OLLAMA_URL") {
				data.Ollama.Error = "invalid OLLAMA_URL format"
			} else {
				data.Ollama.Error = ollamaErr.Error()
			}
		} else {
			httpClient := &http.Client{Timeout: time.Second}
			resp, err := httpClient.Get(ollamaURL + "/api/tags")
			if err != nil {
				data.Ollama.Status = "not_running"
			} else {
				resp.Body.Close()
				data.Ollama.Status = "running"
				data.Ollama.Model = config.EmbeddingModel
			}
		}

		// Hooks
		hookStatus := setup.HooksInstalled(vp)
		hookNames := []string{
			"context-surfacing",
			"decision-extractor",
			"handoff-generator",
			"staleness-check",
		}
		for _, name := range hookNames {
			data.Hooks[name] = hookStatus[name]
		}

		// MCP
		data.MCP.Installed = setup.MCPInstalled(vp)

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

	// Original display logic for non-JSON output
	cli.Header("SAME Status")

	cli.Section("Vault")
	fmt.Printf("  Path:    %s\n", cli.ShortenHome(vp))

	db, err := store.Open()
	if err != nil {
		fmt.Printf("  DB:      %snot initialized%s\n\n",
			cli.Red, cli.Reset)
		fmt.Printf("  Run 'same init' to set up.\n\n")
		return nil
	}
	defer db.Close()

	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	fmt.Printf("  Notes:   %s indexed\n", cli.FormatNumber(noteCount))
	fmt.Printf("  Chunks:  %s\n", cli.FormatNumber(chunkCount))

	// Index age
	indexAge, _ := db.IndexAge()
	if indexAge > 0 {
		fmt.Printf("  Indexed: %s ago\n", formatDuration(indexAge))
	}

	// DB size
	dbPath := config.DBPath()
	if info, err := os.Stat(dbPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		fmt.Printf("  DB:      %.1f MB\n", sizeMB)
	}

	// Ollama (same line block, no extra blank line)
	ollamaURL, ollamaErr := config.OllamaURL()
	if ollamaErr != nil {
		fmt.Printf("  Ollama:  %sinvalid URL%s (%v)\n",
			cli.Red, cli.Reset, ollamaErr)
	} else {
		httpClient := &http.Client{Timeout: time.Second}
		resp, err := httpClient.Get(ollamaURL + "/api/tags")
		if err != nil {
			fmt.Printf("  Ollama:  %snot running%s\n",
				cli.Red, cli.Reset)
		} else {
			resp.Body.Close()
			fmt.Printf("  Ollama:  %srunning%s (%s)\n",
				cli.Green, cli.Reset, config.EmbeddingModel)
		}
	}

	// Hooks
	cli.Section("Hooks")
	hookStatus := setup.HooksInstalled(vp)
	hookNames := []string{
		"context-surfacing",
		"decision-extractor",
		"handoff-generator",
		"staleness-check",
	}
	for _, name := range hookNames {
		if hookStatus[name] {
			fmt.Printf("  %-24s %s✓ active%s\n",
				name, cli.Green, cli.Reset)
		} else {
			fmt.Printf("  %-24s %s- not configured%s\n",
				name, cli.Dim, cli.Reset)
		}
	}

	// MCP
	cli.Section("MCP")
	if setup.MCPInstalled(vp) {
		fmt.Printf("  registered in .mcp.json\n")
	} else {
		fmt.Printf("  %snot registered%s\n",
			cli.Dim, cli.Reset)
	}

	// Config
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
