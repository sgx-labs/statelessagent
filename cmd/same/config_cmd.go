package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/setup"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or change your settings",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("\n  %sEffective Configuration%s\n", cli.Bold, cli.Reset)
			fmt.Printf("  %sMerged from global config, vault config, environment variables, and defaults.%s\n", cli.Dim, cli.Reset)

			globalPath := config.GlobalConfigPath()
			if _, err := os.Stat(globalPath); err == nil {
				fmt.Printf("  %sGlobal: %s%s\n", cli.Dim, cli.ShortenHome(globalPath), cli.Reset)
			} else {
				fmt.Printf("  %sGlobal: (none)%s\n", cli.Dim, cli.Reset)
			}
			if cf := config.FindConfigFile(); cf != "" {
				fmt.Printf("  %sVault:  %s (overrides global)%s\n", cli.Dim, cli.ShortenHome(cf), cli.Reset)
			} else {
				fmt.Printf("  %sVault:  (none)%s\n", cli.Dim, cli.Reset)
			}

			fmt.Printf("  %sEdit with: same config edit | same config edit --global%s\n\n", cli.Dim, cli.Reset)
			fmt.Println(config.ShowConfig())

			// Resolved values — show what the runtime actually uses after
			// merging config file, env vars, and defaults.
			fmt.Printf("# Resolved values:\n")
			ec := config.EmbeddingProviderConfig()
			provider := ec.Provider
			if provider == "" {
				provider = "ollama"
			}
			if provider == "ollama" || provider == "" {
				ollamaURL, err := config.OllamaURL()
				if err != nil {
					fmt.Printf("# Ollama URL: (error: %v)\n", err)
				} else {
					source := "default"
					if os.Getenv("OLLAMA_URL") != "" {
						source = "from OLLAMA_URL env"
					} else if cf := config.FindConfigFile(); cf != "" {
						source = "from config.toml"
					}
					fmt.Printf("# Ollama URL: %s (%s)\n", ollamaURL, source)
				}
			}
			model := ec.Model
			if model == "" {
				model = config.EmbeddingModel
			}
			fmt.Printf("# Embed model: %s\n", model)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print path to config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return fmt.Errorf("no vault found — run 'same init' or set VAULT_PATH")
			}
			fmt.Println(config.ConfigFilePath(vp))
			return nil
		},
	})

	var editGlobal bool
	editCmd := &cobra.Command{
		Use:   "edit",
		Short: "Open config file in $EDITOR",
		RunE: func(cmd *cobra.Command, args []string) error {
			var configPath string
			if editGlobal {
				configPath = config.GlobalConfigPath()
				// Ensure the global config directory exists
				if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
					return fmt.Errorf("create global config directory: %w", err)
				}
				// Create a starter file if none exists
				if _, err := os.Stat(configPath); os.IsNotExist(err) {
					starter := "# SAME Global Configuration\n# Settings here apply to all vaults. Per-vault config overrides these.\n\n[ollama]\n# url = \"http://localhost:11434\"\n\n[chat]\n# model = \"\"\n\n[display]\n# mode = \"full\"  # full, compact, quiet\n"
					if err := os.WriteFile(configPath, []byte(starter), 0o600); err != nil {
						return fmt.Errorf("create global config: %w", err)
					}
				}
			} else {
				vp := config.VaultPath()
				if vp == "" {
					return fmt.Errorf("no vault found — run 'same init' or set VAULT_PATH")
				}
				configPath = config.ConfigFilePath(vp)
				if _, err := os.Stat(configPath); os.IsNotExist(err) {
					fmt.Println("No config file found. Generating default...")
					if err := config.GenerateConfig(vp); err != nil {
						return err
					}
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			fmt.Printf("Opening %s in %s...\n", configPath, editor)
			return runEditor(editor, configPath)
		},
	}
	editCmd.Flags().BoolVar(&editGlobal, "global", false, "Edit global config (~/.config/same/config.toml)")
	cmd.AddCommand(editCmd)

	return cmd
}

func runEditor(editor, path string) error {
	editor = strings.TrimSpace(editor)
	if editor == "" {
		return fmt.Errorf("empty editor command")
	}
	if strings.ContainsAny(editor, "&;|><`$\n\r") {
		return fmt.Errorf("editor command contains unsupported shell metacharacters")
	}

	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("empty editor command")
	}

	bin, err := exec.LookPath(parts[0])
	if err != nil {
		return fmt.Errorf("editor %q not found in PATH: %w", parts[0], err)
	}

	args := append(parts[1:], path)
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---------- setup ----------

func setupSubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up integrations (hooks, MCP)",
	}

	var removeHooks bool
	hooksCmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install or remove Claude Code hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return config.ErrNoVault
			}
			if removeHooks {
				return setup.RemoveHooks(vp)
			}
			return setup.SetupHooks(vp)
		},
	}
	hooksCmd.Flags().BoolVar(&removeHooks, "remove", false, "Remove SAME hooks")
	cmd.AddCommand(hooksCmd)

	var removeMCP bool
	mcpSetupCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Register or remove SAME MCP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return config.ErrNoVault
			}
			if removeMCP {
				return setup.RemoveMCP(vp)
			}
			if err := setup.SetupMCP(vp); err != nil {
				return err
			}
			fmt.Println("\n  Available MCP tools:")
			fmt.Println("    search_notes          Semantic search across your vault")
			fmt.Println("    search_notes_filtered Search with domain/tag/type filters")
			fmt.Println("    search_across_vaults  Search across multiple vaults")
			fmt.Println("    get_note              Read a note by path")
			fmt.Println("    find_similar_notes    Find related notes by similarity")
			fmt.Println("    save_note             Save a new note to the vault")
			fmt.Println("    save_decision         Record a decision or insight")
			fmt.Println("    create_handoff        Create a session handoff note")
			fmt.Println("    get_session_context   Get current session context")
			fmt.Println("    recent_activity       View recently modified notes")
			fmt.Println("    reindex               Re-index the vault")
			fmt.Println("    index_stats           Index statistics and health")
			fmt.Println("    mem_consolidate       Consolidate related notes (experimental)")
			fmt.Println("    mem_brief             Get an orientation briefing (experimental)")
			fmt.Println("    mem_health            Check vault health score (experimental)")
			fmt.Println("    mem_forget            Suppress a memory from search (experimental)")
			return nil
		},
	}
	mcpSetupCmd.Flags().BoolVar(&removeMCP, "remove", false, "Remove SAME MCP server")
	cmd.AddCommand(mcpSetupCmd)

	return cmd
}
