package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	// Use bare "same" command to rely on PATH, making the config portable
	// across machines (codespaces, containers, different OS). Only fall back
	// to absolute path if "same" is not in PATH at all.
	binaryPath := "same"
	if _, err := exec.LookPath("same"); err != nil {
		binaryPath = detectBinaryPath()
	}

	// Load existing config or create new
	var cfg mcpConfig
	if data, err := os.ReadFile(mcpPath); err == nil {
		if jsonErr := json.Unmarshal(data, &cfg); jsonErr != nil {
			// SECURITY: if the file exists but has invalid JSON (e.g., trailing comma),
			// do NOT overwrite it with a fresh object — that would destroy the user's config.
			return fmt.Errorf("parse %s: %w (fix the JSON manually to avoid data loss)", mcpPath, jsonErr)
		}
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

	fmt.Println("  → .mcp.json (MCP server registered with 12 tools)")
	fmt.Println()
	fmt.Println("  Available tools:")
	tools := []struct{ name, desc string }{
		{"search_notes", "Search your knowledge base"},
		{"search_notes_filtered", "Search with domain/tag filters"},
		{"get_note", "Read full note content"},
		{"find_similar_notes", "Find related notes by topic"},
		{"save_note", "Create or update a note"},
		{"save_decision", "Log a project decision"},
		{"create_handoff", "Write a session handoff"},
		{"get_session_context", "Get orientation for a new session"},
		{"recent_activity", "See recently modified notes"},
		{"reindex", "Rebuild the search index"},
		{"index_stats", "Check index health and size"},
		{"search_across_vaults", "Search across all vaults"},
	}
	for _, t := range tools {
		fmt.Printf("    %-24s %s\n", t.name, t.desc)
	}
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

// MCPUsesPortablePath checks if the MCP config uses a portable binary path.
// Returns true if the command is "same" (from PATH), false if it's an absolute path.
// Returns false, false if no MCP config exists.
func MCPUsesPortablePath(vaultPath string) (portable bool, exists bool) {
	mcpPath := filepath.Join(vaultPath, ".mcp.json")

	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return false, false
	}

	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, false
	}

	server, ok := cfg.Servers["same"]
	if !ok {
		return false, false
	}

	// A portable path is just "same" (relies on PATH resolution).
	// An absolute path like "/usr/local/bin/same" is not portable.
	return server.Command == "same", true
}

// setupMCPInteractive prompts and sets up MCP.
func setupMCPInteractive(vaultPath string, autoAccept bool) {
	// Use friendlier prompt text for non-developers
	if autoAccept || confirm("  Set up MCP server for AI tools? (recommended)", true) {
		if err := SetupMCP(vaultPath); err != nil {
			fmt.Printf("  %s!%s Could not set up connection: %v\n",
				cli.Yellow, cli.Reset, err)
		}
	} else {
		fmt.Println("  Skipped. Run 'same setup mcp' later if needed.")
	}
}
