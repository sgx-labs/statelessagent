package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sgx-labs/statelessagent/internal/cli"
)

type mcpConfig struct {
	Servers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// SetupMCP registers SAME as an MCP server in .mcp.json.
func SetupMCP(vaultPath string) error {
	mcpPath := filepath.Join(vaultPath, ".mcp.json")
	binaryPath := detectBinaryPath()

	// Load existing config or create new
	var cfg mcpConfig
	if data, err := os.ReadFile(mcpPath); err == nil {
		json.Unmarshal(data, &cfg)
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]mcpServer)
	}

	cfg.Servers["same"] = mcpServer{
		Command: binaryPath,
		Args:    []string{"mcp"},
		Env: map[string]string{
			"VAULT_PATH": vaultPath,
		},
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(mcpPath, data, 0o644); err != nil {
		return fmt.Errorf("write .mcp.json: %w", err)
	}

	fmt.Println("  → .mcp.json (MCP server)")
	return nil
}

// RemoveMCP removes SAME from .mcp.json.
func RemoveMCP(vaultPath string) error {
	mcpPath := filepath.Join(vaultPath, ".mcp.json")

	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return fmt.Errorf("read .mcp.json: %w", err)
	}

	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse .mcp.json: %w", err)
	}

	if _, ok := cfg.Servers["same"]; !ok {
		fmt.Println("  SAME not registered in .mcp.json")
		return nil
	}

	delete(cfg.Servers, "same")

	data, _ = json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(mcpPath, data, 0o644); err != nil {
		return fmt.Errorf("write .mcp.json: %w", err)
	}

	fmt.Println("  Removed SAME from .mcp.json")
	return nil
}

// MCPInstalled checks if SAME is registered as an MCP server.
func MCPInstalled(vaultPath string) bool {
	mcpPath := filepath.Join(vaultPath, ".mcp.json")

	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return false
	}

	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}

	_, ok := cfg.Servers["same"]
	return ok
}

// setupMCPInteractive prompts and sets up MCP.
func setupMCPInteractive(vaultPath string, autoAccept bool) {
	// Use friendlier prompt text for non-developers
	if autoAccept || confirm("  Connect to Cursor/Windsurf? (recommended)", true) {
		if err := SetupMCP(vaultPath); err != nil {
			fmt.Printf("  %s!%s Could not set up connection: %v\n",
				cli.Yellow, cli.Reset, err)
		}
	} else {
		fmt.Println("  Skipped. Run 'same setup mcp' later if needed.")
	}
}
