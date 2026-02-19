package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
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

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sigCh)

			go func() {
				select {
				case <-ctx.Done():
				case <-sigCh:
					fmt.Fprintln(os.Stderr, "Shutting down...")
					cancel()
				}
			}()

			// Create embed provider (nil is fine — keyword fallback)
			embedClient, _ := newEmbedProvider()

			addr := fmt.Sprintf("127.0.0.1:%d", port)
			fmt.Printf("\n  Dashboard: %shttp://%s%s\n", cli.Bold, addr, cli.Reset)
			fmt.Printf("  Press Ctrl+C to stop\n\n")

			if openFlag {
				go func() {
					time.Sleep(300 * time.Millisecond)
					openBrowser(fmt.Sprintf("http://%s", addr))
				}()
			}

			return web.Serve(ctx, addr, embedClient, Version, vp)
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
