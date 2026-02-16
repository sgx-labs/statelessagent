package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/web"
)

func webCmd() *cobra.Command {
	var (
		port     int
		openFlag bool
	)
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open the vault dashboard in your browser",
		Long: `Start a local web server for the vault dashboard.

The dashboard is read-only and only accessible from localhost.

Examples:
  same web                  # Start on port 4078
  same web --port 8080      # Custom port
  same web --open           # Auto-open browser`,
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return config.ErrNoVault
			}

			// Create embed provider (nil is fine — keyword fallback)
			embedClient, _ := newEmbedProvider()

			addr := fmt.Sprintf("127.0.0.1:%d", port)

			if openFlag {
				go func() {
					time.Sleep(300 * time.Millisecond)
					openBrowser(fmt.Sprintf("http://%s", addr))
				}()
			}

			return web.Serve(addr, embedClient, Version, vp)
		},
	}
	cmd.Flags().IntVar(&port, "port", 4078, "Port to listen on")
	cmd.Flags().BoolVar(&openFlag, "open", false, "Auto-open browser")
	return cmd
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	cmd.Run()
}
