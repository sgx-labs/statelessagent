package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
)

// ---------- display ----------

func displayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "display",
		Short: "Change how much SAME shows you",
		Long: `Control how much detail SAME shows when surfacing notes.

Modes:
  full     Show the full box with all details (default)
  compact  Show just a one-line summary
  quiet    Don't show anything

Example: same display compact`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "full",
		Short: "Show full details when surfacing (default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setDisplayMode("full")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "compact",
		Short: "Show just a one-line summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setDisplayMode("compact")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "quiet",
		Short: "Don't show surfacing output",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setDisplayMode("quiet")
		},
	})

	return cmd
}

func setDisplayMode(mode string) error {
	vp := config.VaultPath()
	if vp == "" {
		return config.ErrNoVault
	}

	// Update config file
	cfgPath := config.ConfigFilePath(vp)
	if err := config.SetDisplayMode(vp, mode); err != nil {
		return fmt.Errorf("update config: %w", err)
	}

	switch mode {
	case "full":
		fmt.Println("Display mode: full (show all details)")
		fmt.Println("\nSAME will show the complete box with included/excluded notes.")
	case "compact":
		fmt.Println("Display mode: compact (one-liner)")
		fmt.Println("\nSAME will show: ✦ SAME surfaced 3 of 847 memories")
	case "quiet":
		fmt.Println("Display mode: quiet (hidden)")
		fmt.Println("\nSAME will work silently in the background.")
	}

	fmt.Printf("\nSaved to: %s\n", cli.ShortenHome(cfgPath))
	fmt.Println("Change takes effect on next prompt.")
	return nil
}

// ---------- profile ----------

func profileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Switch between search precision presets",
		Long: `Control how SAME balances precision vs coverage when surfacing notes.

Profiles:
  precise   Fewer results, higher relevance threshold (uses fewer tokens)
  balanced  Default balance of relevance and coverage
  broad     More results, lower threshold (uses ~2x more tokens)

Example: same profile use precise`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showCurrentProfile()
		},
	}

	useCmd := &cobra.Command{
		Use:   "use [profile]",
		Short: "Switch to a profile (precise, balanced, broad)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setProfile(args[0])
		},
	}
	cmd.AddCommand(useCmd)

	return cmd
}

func showCurrentProfile() error {
	current := config.CurrentProfile()

	cli.Header("SAME Profile")
	fmt.Println()

	for _, name := range []string{"precise", "balanced", "broad"} {
		p := config.BuiltinProfiles[name]
		marker := "  "
		if name == current {
			marker = fmt.Sprintf("%s→%s ", cli.Cyan, cli.Reset)
		}

		tokenNote := ""
		if p.TokenWarning != "" {
			tokenNote = fmt.Sprintf(" %s(%s)%s", cli.Dim, p.TokenWarning, cli.Reset)
		}

		fmt.Printf("  %s%-10s %s%s\n", marker, name, p.Description, tokenNote)
	}

	if current == "custom" {
		fmt.Printf("\n  %s→ custom%s (manually configured values)\n", cli.Cyan, cli.Reset)
	}

	fmt.Println()
	fmt.Printf("  Change with: %ssame profile use <name>%s\n", cli.Bold, cli.Reset)
	fmt.Println()

	return nil
}

func setProfile(profileName string) error {
	vp := config.VaultPath()
	if vp == "" {
		return config.ErrNoVault
	}

	profile, ok := config.BuiltinProfiles[profileName]
	if !ok {
		return userError(
			fmt.Sprintf("Unknown profile: %s", profileName),
			"Available: precise, balanced, broad",
		)
	}

	// Show warning for broad profile
	if profileName == "broad" {
		fmt.Printf("\n  %s⚠ Token usage warning:%s\n", cli.Yellow, cli.Reset)
		fmt.Println("  The 'broad' profile surfaces more notes per query,")
		fmt.Println("  which uses approximately 2x more tokens.")
		fmt.Println()
	}

	if err := config.SetProfile(vp, profileName); err != nil {
		return fmt.Errorf("update config: %w", err)
	}

	fmt.Printf("\n  %s✓%s Profile set to: %s%s%s\n", cli.Green, cli.Reset, cli.Bold, profileName, cli.Reset)
	fmt.Printf("    %s\n", profile.Description)

	if profile.TokenWarning != "" {
		fmt.Printf("    %s%s%s\n", cli.Dim, profile.TokenWarning, cli.Reset)
	}

	fmt.Println()
	fmt.Printf("  Settings applied:\n")
	fmt.Printf("    max_results:         %d\n", profile.MaxResults)
	fmt.Printf("    distance_threshold:  %.1f\n", profile.DistanceThreshold)
	fmt.Printf("    composite_threshold: %.2f\n", profile.CompositeThreshold)
	fmt.Println()
	fmt.Println("  Change takes effect on next prompt.")

	return nil
}
