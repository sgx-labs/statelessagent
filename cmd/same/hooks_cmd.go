package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/hooks"
	"github.com/sgx-labs/statelessagent/internal/setup"
)

func hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Run a hook handler",
	}
	cmd.AddCommand(hookSubCmd("context-surfacing", "UserPromptSubmit hook: surface relevant vault context"))
	cmd.AddCommand(hookSubCmd("decision-extractor", "Stop hook: extract decisions from transcript"))
	cmd.AddCommand(hookSubCmd("handoff-generator", "PreCompact/Stop hook: generate handoff notes"))
	cmd.AddCommand(hookSubCmd("feedback-loop", "Stop hook: track which surfaced notes were actually used"))
	cmd.AddCommand(hookSubCmd("staleness-check", "SessionStart hook: surface stale notes"))
	cmd.AddCommand(hookSubCmd("session-bootstrap", "SessionStart hook: bootstrap session with handoff + decisions + stale notes"))
	return cmd
}

func hookSubCmd(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			hooks.Run(name)
			return nil
		},
	}
}

func hooksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hooks",
		Short: "List available hooks and their installation status",
		Long: `Shows all available SAME hooks, what they do, and whether they are installed
in your .claude/settings.json file.

Hooks connect SAME to Claude Code to provide automatic context injection,
decision extraction, and session handoffs.

To install hooks, run: same init`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHooksList()
		},
	}
}

func runHooksList() error {
	// Define hook metadata: name, event, description
	type hookInfo struct {
		name        string
		event       string
		description string
	}

	hooks := []hookInfo{
		{
			name:        "context-surfacing",
			event:       "UserPromptSubmit",
			description: "Injects relevant notes into Claude's context before tool use",
		},
		{
			name:        "decision-extractor",
			event:       "Stop",
			description: "Captures decisions and insights from the conversation",
		},
		{
			name:        "handoff-generator",
			event:       "Stop",
			description: "Creates session handoff notes when Claude stops",
		},
		{
			name:        "feedback-loop",
			event:       "Stop",
			description: "Tracks which surfaced notes were actually used",
		},
		{
			name:        "session-bootstrap",
			event:       "SessionStart",
			description: "Orients the agent with vault context and previous session state",
		},
		{
			name:        "staleness-check",
			event:       "SessionStart",
			description: "Flags notes that may be outdated",
		},
	}

	cli.Header("SAME Hooks")

	// Check installation status
	vp := config.VaultPath()
	var status map[string]bool
	if vp != "" {
		status = setup.HooksInstalled(vp)
	} else {
		// No vault — show all as not installed
		status = map[string]bool{
			"context-surfacing":  false,
			"decision-extractor": false,
			"handoff-generator":  false,
			"feedback-loop":      false,
			"session-bootstrap":  false,
			"staleness-check":    false,
		}
	}

	// Print table header
	fmt.Printf("  %-24s %-18s %s\n", "Hook", "Event", "Status")
	fmt.Printf("  %s\n", strings.Repeat("-", 70))

	// Print each hook
	for _, h := range hooks {
		installed := status[h.name]
		var statusStr string
		if installed {
			statusStr = fmt.Sprintf("%s\u2713 installed%s", cli.Green, cli.Reset)
		} else {
			statusStr = fmt.Sprintf("%s\u2717 not installed%s", cli.Dim, cli.Reset)
		}

		fmt.Printf("  %-24s %-18s %s\n", h.name, h.event, statusStr)
		fmt.Printf("  %s%s%s\n\n", cli.Dim, h.description, cli.Reset)
	}

	// Footer with installation instructions
	if vp == "" {
		fmt.Printf("  %sNo vault detected. Run 'same init' to set up.%s\n\n", cli.Yellow, cli.Reset)
	} else {
		hasAny := false
		for _, installed := range status {
			if installed {
				hasAny = true
				break
			}
		}

		if !hasAny {
			fmt.Printf("  %sTo install hooks, run: same init%s\n\n", cli.Yellow, cli.Reset)
		}
	}

	cli.Footer()
	return nil
}
