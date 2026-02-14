package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
)

func modelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Show or switch embedding models",
		Long: `Show the current embedding model and available alternatives.

Example:
  same model                              Show current model
  same model use snowflake-arctic-embed2  Switch to a different model`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showCurrentModel()
		},
	}

	cmd.AddCommand(modelUseCmd())
	return cmd
}

func modelUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use [model]",
		Short: "Switch to a different embedding model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setModel(args[0])
		},
	}
}

func showCurrentModel() error {
	ec := config.EmbeddingProviderConfig()

	currentModel := ec.Model
	if currentModel == "" {
		currentModel = config.EmbeddingModel
	}

	fmt.Println()
	fmt.Printf("  %sEmbedding Model:%s %s\n", cli.Bold, cli.Reset, currentModel)
	fmt.Printf("  %sProvider:%s       %s\n", cli.Bold, cli.Reset, ec.Provider)

	// Find current model dims
	for _, m := range config.KnownModels {
		if m.Name == currentModel {
			fmt.Printf("  %sDimensions:%s    %d\n", cli.Bold, cli.Reset, m.Dims)
			break
		}
	}

	fmt.Printf("\n  %sAvailable models:%s\n\n", cli.Bold, cli.Reset)
	fmt.Printf("  %-28s %5s  %s\n", "MODEL", "DIMS", "DESCRIPTION")

	for _, m := range config.KnownModels {
		if m.Provider == "openai" && ec.Provider != "openai" {
			continue // skip OpenAI models when using Ollama
		}
		marker := " "
		if m.Name == currentModel {
			marker = fmt.Sprintf("%s→%s", cli.Cyan, cli.Reset)
		}
		fmt.Printf("  %s %-26s %5d  %s%s%s\n",
			marker, m.Name, m.Dims, cli.Dim, m.Description, cli.Reset)
	}

	fmt.Printf("\n  Switch with: %ssame model use <name>%s\n\n", cli.Bold, cli.Reset)
	return nil
}

func setModel(model string) error {
	vp := config.VaultPath()
	if vp == "" {
		return config.ErrNoVault
	}

	ec := config.EmbeddingProviderConfig()
	currentModel := ec.Model
	if currentModel == "" {
		currentModel = config.EmbeddingModel
	}

	if model == currentModel {
		fmt.Printf("\n  Already using %s%s%s. No changes needed.\n\n", cli.Bold, model, cli.Reset)
		return nil
	}

	if !config.IsKnownModel(model) {
		fmt.Printf("\n  %sWarning:%s %q is not a recognized model.\n", cli.Yellow, cli.Reset, model)
		fmt.Println("  It may still work if your embedding provider supports it.")
		fmt.Println()
	}

	if err := config.SetEmbeddingModel(vp, model); err != nil {
		return fmt.Errorf("update config: %w", err)
	}

	// Find new model info
	desc := ""
	dims := 0
	for _, m := range config.KnownModels {
		if m.Name == model {
			desc = m.Description
			dims = m.Dims
			break
		}
	}

	fmt.Printf("\n  %s✓%s Model set to: %s%s%s\n", cli.Green, cli.Reset, cli.Bold, model, cli.Reset)
	if desc != "" {
		fmt.Printf("    %s%s%s\n", cli.Dim, desc, cli.Reset)
	}
	if dims > 0 {
		fmt.Printf("    Dimensions: %d\n", dims)
	}

	fmt.Printf("\n  %sIMPORTANT:%s Run %ssame reindex --force%s to re-embed all notes.\n",
		cli.Yellow, cli.Reset, cli.Bold, cli.Reset)
	fmt.Println("  The old embeddings won't work with the new model.")
	fmt.Println()

	return nil
}
