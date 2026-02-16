package main

import (
	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/setup"
)

func initCmd() *cobra.Command {
	var (
		yes       bool
		mcpOnly   bool
		hooksOnly bool
		verbose   bool
		provider  string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up SAME for your project (start here)",
		Long: `The setup wizard walks you through connecting SAME to your project.

What it does:
  1. Checks that Ollama is running (needed for local AI processing)
  2. Finds your notes/markdown files
  3. Indexes them so your AI can search them
  4. Connects to your AI tools (Claude, Cursor, etc.)

Run this command from inside your project folder.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return setup.RunInit(setup.InitOptions{
				Yes:       yes,
				MCPOnly:   mcpOnly,
				HooksOnly: hooksOnly,
				Verbose:   verbose,
				Version:   Version,
				Provider:  provider,
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Accept all defaults without prompting")
	cmd.Flags().BoolVar(&mcpOnly, "mcp-only", false, "Skip hooks setup (for Cursor/Windsurf users)")
	cmd.Flags().BoolVar(&hooksOnly, "hooks-only", false, "Skip MCP setup (Claude Code only)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show each file being processed")
	cmd.Flags().StringVar(&provider, "provider", "", "Embedding provider: ollama, openai, openai-compatible")
	return cmd
}
