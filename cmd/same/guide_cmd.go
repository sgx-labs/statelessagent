package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
)

func guideCmd() *cobra.Command {
	var agentMode bool

	cmd := &cobra.Command{
		Use:   "guide",
		Short: "Show recommended AI tool configuration for SAME",
		Long: `Prints recommended configuration text for your AI coding tool.
Copy the output into your CLAUDE.md, .cursorrules, or equivalent file.
SAME never modifies these files directly.

Use --agent to get a prompt template for orchestrating subagents.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentMode {
				return runGuideAgent()
			}
			return runGuide()
		},
	}

	cmd.Flags().BoolVar(&agentMode, "agent", false, "Show agent prompt template for multi-agent workflows")
	return cmd
}

func runGuide() error {
	fmt.Printf("\n  %sAdd this to your CLAUDE.md, .cursorrules, or equivalent:%s\n\n", cli.Bold, cli.Reset)

	fmt.Println("  ────────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Printf("  %s## SAME Memory System%s\n", cli.Bold, cli.Reset)
	fmt.Println()
	fmt.Println("  This project uses SAME for persistent memory (19 MCP tools).")
	fmt.Println()
	fmt.Println("  Key behaviors for AI agents:")
	fmt.Println("  - Search results include trust_state (validated, stale, contradicted)")
	fmt.Println("  - Caveat answers that rely on notes with trust_state: stale")
	fmt.Println("  - After saving decisions that change prior work, check if")
	fmt.Println("    contradictions were detected in the response")
	fmt.Println("  - Use search_notes_filtered with trust_state parameter to")
	fmt.Println("    find only validated or only stale context")
	fmt.Println("  - Run mem_health periodically for vault quality overview")
	fmt.Println()
	fmt.Println("  Useful commands:")
	fmt.Println("  - same search \"query\"       Search notes semantically")
	fmt.Println("  - same stale                Check for outdated notes")
	fmt.Println("  - same health               Vault health + trust overview")
	fmt.Println("  - same brief                AI-generated orientation briefing")
	fmt.Println("  - same web --open           Visual dashboard with knowledge graph")
	fmt.Println("  - same tips                 Model recommendations + hardware guide")
	fmt.Println()
	fmt.Println("  ────────────────────────────────────────────────────────")
	fmt.Printf("\n  %sCopy the section above into your config file.%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sSAME never modifies your CLAUDE.md or .cursorrules directly.%s\n\n", cli.Dim, cli.Reset)

	return nil
}

func runGuideAgent() error {
	fmt.Printf("\n  %sAgent Prompt Template%s\n", cli.Bold, cli.Reset)
	fmt.Printf("  %sInclude this in prompts when launching subagents:%s\n\n", cli.Dim, cli.Reset)

	fmt.Println("  ────────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("  ## Before starting:")
	fmt.Println("  - Run `same search \"<your task topic>\"` to check if")
	fmt.Println("    relevant context or prior work already exists")
	fmt.Println("  - Check `same stale` for any outdated notes in your area")
	fmt.Println()
	fmt.Println("  ## While working:")
	fmt.Println("  - When you make a key decision, save it:")
	fmt.Println("    `same add \"<decision>\" --type decision --tags <tags>`")
	fmt.Println("  - When you encounter friction, log it:")
	fmt.Println("    `same add \"<issue>\" --type kaizen --tags <area>`")
	fmt.Println()
	fmt.Println("  ## When done:")
	fmt.Println("  - Report what you changed, what you learned, and any")
	fmt.Println("    issues encountered")
	fmt.Println("  - Do NOT push to git without explicit approval")
	fmt.Println()
	fmt.Println("  ## Trust awareness:")
	fmt.Println("  - Search results include trust_state (validated/stale/contradicted)")
	fmt.Println("  - Caveat any work based on notes with trust_state: stale")
	fmt.Println("  - If you save a note that contradicts existing knowledge,")
	fmt.Println("    SAME will flag the contradiction automatically")
	fmt.Println()
	fmt.Println("  ────────────────────────────────────────────────────────")
	fmt.Printf("\n  %sAdapt this to your workflow. The key principles:%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %s1. Search before starting (don't redo work)%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %s2. Save decisions and friction as you go%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %s3. Respect trust state on search results%s\n\n", cli.Dim, cli.Reset)

	return nil
}
