package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/seed"
)

func seedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Install pre-built knowledge vaults",
		Long: `Browse and install seed vaults — pre-built collections of research notes,
templates, and decision frameworks that give your AI instant expertise.

Seeds are curated knowledge bases that work out of the box with SAME.
Install one and start searching immediately.`,
	}

	cmd.AddCommand(seedListCmd())
	cmd.AddCommand(seedInstallCmd())
	cmd.AddCommand(seedInfoCmd())
	cmd.AddCommand(seedRemoveCmd())
	return cmd
}

func seedListCmd() *cobra.Command {
	var refresh bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show available seeds",
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := seed.FetchManifest(refresh)
			if err != nil {
				return userError("Could not fetch seed list", "Check your internet connection and try again")
			}

			if jsonOut {
				data, _ := json.MarshalIndent(manifest.Seeds, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			fmt.Println()
			fmt.Printf("  %sAvailable Seeds:%s\n\n", cli.Bold, cli.Reset)
			fmt.Printf("  %-30s %5s  %s\n", "NAME", "NOTES", "DESCRIPTION")

			for _, s := range manifest.Seeds {
				marker := " "
				if s.Featured {
					marker = "*"
				}
				installed := ""
				if seed.IsInstalled(s.Name) {
					installed = " [installed]"
				}
				fmt.Printf("  %s %-28s %5d  %s%s%s%s\n",
					marker, s.Name, s.NoteCount, s.Description, cli.Dim, installed, cli.Reset)
			}

			fmt.Printf("\n  %d seeds available. Install with: %ssame seed install <name>%s\n\n",
				len(manifest.Seeds), cli.Bold, cli.Reset)
			return nil
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Bypass cache and fetch fresh list")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func seedInstallCmd() *cobra.Command {
	var path string
	var force bool
	var noIndex bool

	cmd := &cobra.Command{
		Use:               "install [name]",
		Short:             "Download and install a seed vault",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: seedNameCompleter,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			// Allow selecting by number (e.g. "same seed install 1")
			if n, err := strconv.Atoi(name); err == nil && n >= 1 {
				if m, mErr := seed.FetchManifest(false); mErr == nil && n <= len(m.Seeds) {
					name = m.Seeds[n-1].Name
				}
			}

			fmt.Println()
			fmt.Printf("  %sSeed Install:%s %s\n\n", cli.Bold, cli.Reset, name)

			opts := seed.InstallOptions{
				Name:    name,
				Path:    path,
				Force:   force,
				NoIndex: noIndex,
				Version: Version,
				OnDownloadStart: func() {
					fmt.Printf("  Downloading...               ")
				},
				OnDownloadDone: func(sizeKB int) {
					fmt.Printf("done (%d KB)\n", sizeKB)
				},
				OnExtractDone: func(fileCount int) {
					fmt.Printf("  Extracting %d files...       done\n", fileCount)
				},
				OnIndexDone: func(chunks int) {
					if chunks > 0 {
						fmt.Printf("  Indexed %d chunks\n", chunks)
					} else {
						fmt.Printf("  Indexing skipped (no embeddings provider)\n")
					}
				},
			}

			result, err := seed.Install(opts)
			if err != nil {
				errMsg := err.Error()
				if strings.Contains(errMsg, "already exists") {
					return userError(errMsg, "Use --force to overwrite the existing installation")
				}
				if strings.Contains(errMsg, "not found") {
					return userError(errMsg, "Run 'same seed list' to see available seeds")
				}
				if strings.Contains(errMsg, "requires SAME") {
					return userError(errMsg, "Run 'same update' to get the latest version")
				}
				if strings.Contains(errMsg, "already installed") {
					fmt.Printf("  %s✓%s %s\n", cli.Green, cli.Reset, errMsg)
					return nil
				}
				return fmt.Errorf("install failed: %w", err)
			}

			fmt.Printf("  Registered as vault %q\n", name)
			seed.PrintLegalNotice()
			fmt.Printf("\n  Installed to %s\n", cli.ShortenHome(result.DestDir))
			fmt.Printf("\n  %sNext steps:%s\n", cli.Bold, cli.Reset)
			fmt.Printf("    same search \"your query\" --vault %s\n", name)
			fmt.Printf("    same search \"your query\" --all      %s# search all vaults at once%s\n", cli.Dim, cli.Reset)
			fmt.Printf("    same seed list\n\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "Custom install directory (default: ~/same-seeds/<name>)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing installation")
	cmd.Flags().BoolVar(&noIndex, "no-index", false, "Skip indexing after install")
	return cmd
}

func seedInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "info [name]",
		Short:             "Show details about a seed",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: seedNameCompleter,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			// Allow selecting by number (e.g. "same seed info 1")
			if n, err := strconv.Atoi(name); err == nil && n >= 1 {
				if m, mErr := seed.FetchManifest(false); mErr == nil && n <= len(m.Seeds) {
					name = m.Seeds[n-1].Name
				}
			}

			manifest, err := seed.FetchManifest(false)
			if err != nil {
				return userError("Could not fetch seed list", "Check your internet connection")
			}

			s := seed.FindSeed(manifest, name)
			if s == nil {
				return userError(
					fmt.Sprintf("Seed %q not found", name),
					"Run 'same seed list' to see available seeds",
				)
			}

			installed := "No"
			if seed.IsInstalled(s.Name) {
				installed = "Yes"
			}

			fmt.Println()
			fmt.Printf("  %s%s%s\n", cli.Bold, s.DisplayName, cli.Reset)
			fmt.Printf("  %s%s%s\n\n", cli.Dim, s.Description, cli.Reset)
			fmt.Printf("  %-15s %s\n", "Name:", s.Name)
			fmt.Printf("  %-15s %s\n", "Audience:", s.Audience)
			fmt.Printf("  %-15s %d\n", "Notes:", s.NoteCount)
			fmt.Printf("  %-15s %d KB\n", "Size:", s.SizeKB)
			fmt.Printf("  %-15s %s\n", "Tags:", strings.Join(s.Tags, ", "))
			if s.MinSameVersion != "" {
				fmt.Printf("  %-15s v%s+\n", "Requires:", s.MinSameVersion)
			}
			fmt.Printf("  %-15s %s\n", "Installed:", installed)
			if s.Featured {
				fmt.Printf("  %-15s %s\n", "Featured:", "Yes")
			}
			fmt.Println()
			return nil
		},
	}
}

func seedRemoveCmd() *cobra.Command {
	var yes bool
	var keepFiles bool

	cmd := &cobra.Command{
		Use:               "remove [name]",
		Short:             "Uninstall a seed vault",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: seedNameCompleter,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if !seed.IsInstalled(name) {
				return userError(
					fmt.Sprintf("Seed %q is not installed", name),
					"Run 'same seed list' to see installed seeds",
				)
			}

			deleteFiles := !keepFiles

			if !yes && deleteFiles {
				fmt.Printf("  Remove seed %q and delete all its files? [y/N] ", name)
				var confirm string
				_, _ = fmt.Scanln(&confirm)
				confirm = strings.TrimSpace(strings.ToLower(confirm))
				if confirm != "y" && confirm != "yes" {
					fmt.Println("  Canceled.")
					return nil
				}
			}

			if err := seed.Remove(name, deleteFiles); err != nil {
				return fmt.Errorf("remove failed: %w", err)
			}

			if deleteFiles {
				fmt.Printf("  Removed seed %q and deleted files.\n", name)
			} else {
				fmt.Printf("  Unregistered seed %q (files kept).\n", name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&keepFiles, "keep-files", false, "Keep files on disk, only unregister")
	return cmd
}

// seedNameCompleter provides tab-completion for seed names.
func seedNameCompleter(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	manifest, err := seed.FetchManifest(false)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, s := range manifest.Seeds {
		if strings.HasPrefix(s.Name, toComplete) {
			names = append(names, s.Name+"\t"+s.Description)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
