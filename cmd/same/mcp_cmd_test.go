package main

import (
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
)

func TestMCPCmd_NoVault(t *testing.T) {
	oldOverride := config.VaultOverride
	config.VaultOverride = "/dev/null/statelessagent-invalid-vault"
	t.Cleanup(func() { config.VaultOverride = oldOverride })

	t.Setenv("SAME_EMBED_PROVIDER", "none")

	cmd := mcpCmd()
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected MCP command to fail without a valid vault")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "open database") && !strings.Contains(errMsg, "not initialized") {
		t.Fatalf("expected vault init error, got: %v", err)
	}
}
