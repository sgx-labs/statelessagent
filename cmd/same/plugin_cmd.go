package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/hooks"
)

func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage hook extensions",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			plugins := hooks.LoadPlugins()
			if len(plugins) == 0 {
				pluginsPath := filepath.Join(config.VaultPath(), ".same", "plugins.json")
				fmt.Printf("No plugins registered.\n")
				fmt.Printf("Create %s to add custom hooks.\n\n", pluginsPath)
				fmt.Println("Example plugins.json:")
				fmt.Println(`{
  "plugins": [
    {
      "name": "my-custom-hook",
      "event": "UserPromptSubmit",
      "command": "/path/to/script.sh",
      "args": [],
      "timeout_ms": 5000,
      "enabled": true
    }
  ]
}`)
				return nil
			}
			fmt.Println("Registered plugins:")
			for _, p := range plugins {
				status := "enabled"
				if !p.Enabled {
					status = "disabled"
				}
				fmt.Printf("  %-20s  event=%-20s  %s  %s %s\n",
					p.Name, p.Event, status, p.Command, strings.Join(p.Args, " "))
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "trust",
		Short: "Trust the plugin manifest for the current vault",
		Long: `Review and trust the .same/plugins.json in the current vault.
Plugins will not execute until explicitly trusted. If the file is modified
after trust is granted, trust is revoked and must be re-granted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return fmt.Errorf("no vault found — run 'same init' first")
			}
			pluginPath := filepath.Join(vp, ".same", "plugins.json")
			data, err := os.ReadFile(pluginPath)
			if err != nil {
				return fmt.Errorf("no plugins.json found at %s", pluginPath)
			}
			fmt.Printf("Plugin manifest: %s\n\n", pluginPath)
			fmt.Println(string(data))
			fmt.Println()
			fmt.Println("Trusting this plugin manifest for the current vault.")
			if err := hooks.TrustVaultPlugins(); err != nil {
				return fmt.Errorf("failed to save trust: %w", err)
			}
			fmt.Println("Done. Plugins will now load for this vault.")
			return nil
		},
	})

	return cmd
}
