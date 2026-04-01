package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	mcpserver "github.com/sgx-labs/statelessagent/internal/mcp"
	"github.com/sgx-labs/statelessagent/internal/web"
)

func webCmd() *cobra.Command {
	var (
		port       int
		openFlag   bool
		foreground bool
		mcpFlag    bool
	)
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open the vault dashboard in your browser",
		Long: `Start a local web server for the vault dashboard.

The dashboard is read-only and only accessible from localhost.
The server runs in the background by default.

Use --mcp to also enable the Streamable HTTP MCP endpoint on /mcp,
which allows MCP clients like Claude Code and Cursor to connect
over HTTP instead of stdio.

Examples:
  same web                  # Start background server, open browser
  same web --port 8080      # Custom port
  same web --fg             # Run in foreground (blocks terminal)
  same web --mcp --fg       # Dashboard + MCP endpoint (foreground)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return config.ErrNoVault
			}
			// Verify the vault actually exists on disk
			if _, err := os.Stat(vp); err != nil {
				return fmt.Errorf("vault path does not exist: %s", vp)
			}

			addr := fmt.Sprintf("127.0.0.1:%d", port)

			// Background mode: re-exec ourselves as a detached process
			if !foreground && os.Getenv("_SAME_WEB_BG") == "" {
				return launchBackground(addr, port, vp)
			}

			// Foreground mode (or background child process)
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sigCh)

			go func() {
				select {
				case <-ctx.Done():
				case <-sigCh:
					cancel()
				}
			}()

			// Create embed provider (nil is fine — keyword fallback)
			embedClient, embedErr := newEmbedProvider()
			if embedErr != nil {
				fmt.Fprintf(os.Stderr, "  %s⚠ Embedding unavailable: %v — using keyword search%s\n", cli.Dim, embedErr, cli.Reset)
			}

			if foreground {
				fmt.Printf("\n  Dashboard: %shttp://%s%s\n", cli.Bold, addr, cli.Reset)
				fmt.Printf("  %sPress Ctrl+C to stop%s\n\n", cli.Dim, cli.Reset)
			}

			webVersion := Version
			if CommitHash != "" && CommitHash != "unknown" {
				webVersion = Version + "+" + CommitHash
			}

			// Set up MCP endpoint if requested
			var mcpOpts *web.MCPOptions
			if mcpFlag {
				// Initialize MCP globals (db, embedClient, vaultRoot)
				mcpDB, initErr := mcpserver.InitGlobals()
				if initErr != nil {
					return fmt.Errorf("initialize MCP server: %w", initErr)
				}
				defer mcpDB.Close()

				mcpserver.Version = webVersion

				token := config.AuthToken()
				if token == "" {
					// Generate a random token for this session
					token = generateToken()
					fmt.Fprintf(os.Stderr, "  Generated MCP auth token (session-only): %s\n", token)
					fmt.Fprintf(os.Stderr, "  %sSet SAME_MCP_TOKEN or auth.token in config for a persistent token%s\n\n", cli.Dim, cli.Reset)
				}

				mcpOpts = &web.MCPOptions{
					Server: mcpserver.NewMCPServer(),
					Token:  token,
				}

				mcpURL := fmt.Sprintf("http://%s/mcp", addr)
				fmt.Fprintf(os.Stderr, "  MCP endpoint: %s%s%s\n\n", cli.Bold, mcpURL, cli.Reset)
				printMCPConfigSnippets(mcpURL, token)
			}

			return web.Serve(ctx, addr, embedClient, webVersion, vp, mcpOpts)
		},
	}
	cmd.Flags().IntVar(&port, "port", 4078, "Port to listen on")
	cmd.Flags().BoolVar(&openFlag, "open", false, "Auto-open browser (default in background mode)")
	cmd.Flags().BoolVar(&foreground, "fg", false, "Run in foreground (blocks terminal)")
	cmd.Flags().BoolVar(&mcpFlag, "mcp", false, "Enable Streamable HTTP MCP endpoint on /mcp")
	return cmd
}

// launchBackground re-execs `same web --fg` as a detached background process,
// waits for the server to start, opens the browser, and returns.
func launchBackground(addr string, port int, vaultPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	child := exec.Command(exe, "web", "--fg", "--port", fmt.Sprintf("%d", port))
	child.Env = append(os.Environ(), "_SAME_WEB_BG=1", "VAULT_PATH="+vaultPath)
	child.SysProcAttr = backgroundProcessSysProcAttr()
	child.Stdout = nil
	child.Stderr = nil

	if err := child.Start(); err != nil {
		return fmt.Errorf("start background server: %w", err)
	}

	// Wait for server to be ready (up to 3 seconds)
	url := fmt.Sprintf("http://%s", addr)
	ready := false
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
	}

	if !ready {
		// Check if port is already in use (another instance)
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			fmt.Printf("\n  %s!%s Port %d is already in use.\n", cli.Yellow, cli.Reset, port)
			fmt.Printf("  %sTry a different port: same web --port %d%s\n\n", cli.Dim, port+1, cli.Reset)
			return nil
		}
		fmt.Printf("  %s!%s Server may not have started — check port %d\n", cli.Yellow, cli.Reset, port)
		return nil
	}

	// Write PID file for `same web stop` (future feature)
	pidPath := config.DataDir() + "/web.pid"
	_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", child.Process.Pid)), 0o600)

	fmt.Printf("\n  %s✓%s Dashboard running at %s%s%s\n", cli.Green, cli.Reset, cli.Bold, url, cli.Reset)
	fmt.Printf("  %sPID %d • Stop with: kill %d%s\n\n", cli.Dim, child.Process.Pid, child.Process.Pid, cli.Reset)

	openBrowser(url)

	// Detach — don't wait for child
	_ = child.Process.Release()
	return nil
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
	_ = cmd.Run()
}

// generateToken creates a random 32-byte hex token for session-only use.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a less-random but still usable token
		return "same-dev-token-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// printMCPConfigSnippets prints client configuration snippets for common MCP clients.
func printMCPConfigSnippets(mcpURL, token string) {
	fmt.Fprintf(os.Stderr, "  %sAdd to your MCP client config:%s\n\n", cli.Dim, cli.Reset)

	// Claude Code
	fmt.Fprintf(os.Stderr, "  %sClaude Code%s (.claude.json):\n", cli.Bold, cli.Reset)
	fmt.Fprintf(os.Stderr, "    \"same\": {\n")
	fmt.Fprintf(os.Stderr, "      \"type\": \"streamable-http\",\n")
	fmt.Fprintf(os.Stderr, "      \"url\": \"%s\",\n", mcpURL)
	fmt.Fprintf(os.Stderr, "      \"headers\": {\n")
	fmt.Fprintf(os.Stderr, "        \"Authorization\": \"Bearer %s\"\n", token)
	fmt.Fprintf(os.Stderr, "      }\n")
	fmt.Fprintf(os.Stderr, "    }\n\n")

	// Cursor
	fmt.Fprintf(os.Stderr, "  %sCursor%s (.cursor/mcp.json):\n", cli.Bold, cli.Reset)
	fmt.Fprintf(os.Stderr, "    \"same\": {\n")
	fmt.Fprintf(os.Stderr, "      \"url\": \"%s\",\n", mcpURL)
	fmt.Fprintf(os.Stderr, "      \"headers\": {\n")
	fmt.Fprintf(os.Stderr, "        \"Authorization\": \"Bearer %s\"\n", token)
	fmt.Fprintf(os.Stderr, "      }\n")
	fmt.Fprintf(os.Stderr, "    }\n\n")
}
