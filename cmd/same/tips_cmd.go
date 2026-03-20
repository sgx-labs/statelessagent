package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
)

func tipsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tips [topic]",
		Short: "Best practices for vault hygiene, safety, and usage",
		Long: `Show best practices and tips for getting the most out of SAME.

Topics:
  same tips              Show all best practices
  same tips vault        Vault organization tips
  same tips security     Security and privacy tips
  same tips models       Model selection guidance`,
		ValidArgs: []string{"vault", "security", "models"},
		Args:      cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return showTipsTopic(args[0])
			}
			return showAllTips()
		},
	}
	return cmd
}

func showAllTips() error {
	fmt.Printf("\n  %s✦ SAME Best Practices%s\n", cli.Bold+cli.Cyan, cli.Reset)

	printVaultTips()
	printSecurityTips()
	printModelTips()

	printTipsFooter()
	return nil
}

func showTipsTopic(topic string) error {
	switch topic {
	case "vault":
		fmt.Printf("\n  %s✦ SAME Best Practices%s\n", cli.Bold+cli.Cyan, cli.Reset)
		printVaultTips()
	case "security":
		fmt.Printf("\n  %s✦ SAME Best Practices%s\n", cli.Bold+cli.Cyan, cli.Reset)
		printSecurityTips()
	case "models":
		fmt.Printf("\n  %s✦ SAME Best Practices%s\n", cli.Bold+cli.Cyan, cli.Reset)
		printModelTips()
	default:
		return fmt.Errorf("unknown topic: %s (available: vault, security, models)", topic)
	}

	printTipsFooter()
	return nil
}

func printVaultTips() {
	cli.Section("Vault Organization")

	fmt.Printf("  %s1.%s Keep decisions in their own notes\n", cli.Cyan, cli.Reset)
	fmt.Printf("     One decision per note with rationale. Makes them searchable\n")
	fmt.Printf("     and lets SAME surface the right decision at the right time.\n\n")

	fmt.Printf("  %s2.%s Use %s_PRIVATE/%s for anything sensitive\n", cli.Cyan, cli.Reset, cli.Bold, cli.Reset)
	fmt.Printf("     API keys, personal info, credentials. Never indexed,\n")
	fmt.Printf("     never searchable, never exposed via MCP.\n\n")

	fmt.Printf("  %s3.%s Write handoffs at end of session\n", cli.Cyan, cli.Reset)
	fmt.Printf("     What's done, what's next, what's blocked. Your AI picks\n")
	fmt.Printf("     up exactly where you left off.\n\n")

	fmt.Printf("  %s4.%s Organize by topic, not by date\n", cli.Cyan, cli.Reset)
	fmt.Printf("     SAME handles time via timestamps. Group notes by what\n")
	fmt.Printf("     they're about, not when you wrote them.\n\n")

	fmt.Printf("  %s5.%s Keep notes atomic\n", cli.Cyan, cli.Reset)
	fmt.Printf("     One concept per note, like Zettelkasten. Smaller notes\n")
	fmt.Printf("     get more precise search results.\n\n")

	fmt.Printf("  %s6.%s Name files descriptively\n", cli.Cyan, cli.Reset)
	fmt.Printf("     SAME indexes content, but good names help you too.\n")
	fmt.Printf("     %sauth-jwt-rotation.md%s beats %snote-47.md%s.\n",
		cli.Cyan, cli.Reset, cli.Dim, cli.Reset)
}

func printSecurityTips() {
	cli.Section("Security & Privacy")

	fmt.Printf("  %s1.%s %s_PRIVATE/%s is your safe zone\n", cli.Cyan, cli.Reset, cli.Bold, cli.Reset)
	fmt.Printf("     Never indexed, never searchable, never exposed via MCP.\n")
	fmt.Printf("     Put credentials, API keys, and personal info here.\n\n")

	fmt.Printf("  %s2.%s The guard system scans for PII before commits\n", cli.Cyan, cli.Reset)
	fmt.Printf("     Enable push protection: %ssame guard settings set push-protect on%s\n\n",
		cli.Cyan, cli.Reset)

	fmt.Printf("  %s3.%s Use %s.same/blocklist%s for custom blocked terms\n", cli.Cyan, cli.Reset, cli.Bold, cli.Reset)
	fmt.Printf("     Add terms that should never appear in output.\n")
	fmt.Printf("     One term per line. See %ssame guard%s for details.\n\n",
		cli.Cyan, cli.Reset)

	fmt.Printf("  %s4.%s Review before sharing vaults\n", cli.Cyan, cli.Reset)
	fmt.Printf("     SAME doesn't auto-scrub. If you share a vault with\n")
	fmt.Printf("     someone, review the contents first.\n\n")

	fmt.Printf("  %s5.%s Embedding data stays local by default\n", cli.Cyan, cli.Reset)
	fmt.Printf("     Unless you configure a remote embedding provider,\n")
	fmt.Printf("     everything stays on your machine.\n\n")

	fmt.Printf("  %s6.%s Your vault is just files\n", cli.Cyan, cli.Reset)
	fmt.Printf("     You can encrypt the directory with your OS tools\n")
	fmt.Printf("     (FileVault, LUKS, BitLocker) for extra protection.\n")
}

func printModelTips() {
	cli.Section("Embedding Models")

	fmt.Printf("  %sDefault: nomic-embed-text%s (274 MB)\n", cli.Bold, cli.Reset)
	fmt.Printf("  Works on any machine. Good quality for most vaults.\n\n")

	fmt.Printf("  %sUpgrade: nomic-embed-text-v2-moe%s (957 MB)\n", cli.Bold, cli.Reset)
	fmt.Printf("  Better retrieval quality. Same dimensions — drop-in replacement.\n")
	fmt.Printf("  %sollama pull nomic-embed-text-v2-moe && same model use nomic-embed-text-v2-moe%s\n\n",
		cli.Cyan, cli.Reset)

	fmt.Printf("  %sBest: qwen3-embedding%s (4.7 GB)\n", cli.Bold, cli.Reset)
	fmt.Printf("  Top-ranked embedding model. Needs 8GB+ free RAM.\n")
	fmt.Printf("  %sollama pull qwen3-embedding && same model use qwen3-embedding%s\n",
		cli.Cyan, cli.Reset)

	cli.Section("Graph Extraction")

	fmt.Printf("  Graph enrichment extracts entities and relationships from your notes.\n")
	fmt.Printf("  Quality depends heavily on the LLM model size.\n\n")

	fmt.Printf("  %sMinimum: qwen2.5-coder:3b%s (1.9 GB)\n", cli.Bold, cli.Reset)
	fmt.Printf("  Produces basic extractions. ~7 entities per 35 notes.\n")
	fmt.Printf("  Fine for getting started. Limited relationship detection.\n\n")

	fmt.Printf("  %sRecommended: 7B+ model%s (4-5 GB)\n", cli.Bold, cli.Reset)
	fmt.Printf("  Significantly richer extraction. 50+ entities per 35 notes.\n")
	fmt.Printf("  %sollama pull qwen2.5:7b%s\n\n", cli.Cyan, cli.Reset)

	fmt.Printf("  %sEnable:%s %ssame graph enable%s\n",
		cli.Bold, cli.Reset, cli.Cyan, cli.Reset)

	cli.Section("Chat Model (for brief and ask)")

	fmt.Printf("  Default: uses whatever Ollama model is available.\n")
	fmt.Printf("  Recommended: %sqwen2.5-coder:3b%s or larger.\n",
		cli.Cyan, cli.Reset)

	cli.Section("Hardware Guide")

	fmt.Printf("  %s 8 GB RAM:%s  nomic-embed-text + 3B chat model %s(minimum viable)%s\n",
		cli.Bold, cli.Reset, cli.Dim, cli.Reset)
	fmt.Printf("  %s16 GB RAM:%s  nomic-embed-text-v2-moe + 7B models %s(recommended)%s\n",
		cli.Bold, cli.Reset, cli.Dim, cli.Reset)
	fmt.Printf("  %s32 GB+ RAM:%s qwen3-embedding + 14B+ models %s(best quality)%s\n",
		cli.Bold, cli.Reset, cli.Dim, cli.Reset)
	fmt.Printf("  %sNo GPU:%s     Works on CPU. Embedding is slower but functional.\n",
		cli.Bold, cli.Reset)
	fmt.Printf("  %sContainer:%s  Expect ~3s/chunk for embedding. Use keyword-only mode\n",
		cli.Bold, cli.Reset)
	fmt.Printf("              for instant indexing: %ssame model use none%s\n",
		cli.Cyan, cli.Reset)
}

func printTipsFooter() {
	cli.Footer()
}
