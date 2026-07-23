package main

import (
	"os"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
)

func TestWebCmd_NoVault(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can create the nonexistent vault path")
	}

	oldOverride := config.VaultOverride
	config.VaultOverride = "/definitely/nonexistent/same-vault-path"
	t.Cleanup(func() { config.VaultOverride = oldOverride })

	t.Setenv("SAME_EMBED_PROVIDER", "none")

	cmd := webCmd()
	cmd.SetArgs([]string{"--port", "4079"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when vault path is invalid")
	}
}
