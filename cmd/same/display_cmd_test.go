package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisplayCmd_SetFull(t *testing.T) {
	vault, db := setupCommandTestVault(t)
	_ = db.Close()

	cmd := displayCmd()
	cmd.SetArgs([]string{"full"})

	var execErr error
	out := captureCommandStdout(t, func() {
		execErr = cmd.Execute()
	})
	if execErr != nil {
		t.Fatalf("display full: %v", execErr)
	}
	if !strings.Contains(out, "Display mode: full") {
		t.Fatalf("expected full mode confirmation, got: %q", out)
	}

	cfgPath := filepath.Join(vault, ".same", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `mode = "full"`) {
		t.Fatalf("expected config mode=full, got: %q", string(data))
	}
}

func TestDisplayCmd_SetCompact(t *testing.T) {
	vault, db := setupCommandTestVault(t)
	_ = db.Close()

	cmd := displayCmd()
	cmd.SetArgs([]string{"compact"})

	var execErr error
	out := captureCommandStdout(t, func() {
		execErr = cmd.Execute()
	})
	if execErr != nil {
		t.Fatalf("display compact: %v", execErr)
	}
	if !strings.Contains(out, "Display mode: compact") {
		t.Fatalf("expected compact mode confirmation, got: %q", out)
	}

	cfgPath := filepath.Join(vault, ".same", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `mode = "compact"`) {
		t.Fatalf("expected config mode=compact, got: %q", string(data))
	}
}

func TestDisplayCmd_InvalidMode(t *testing.T) {
	cmd := displayCmd()
	cmd.SetArgs([]string{"invalid"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unknown subcommand error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown-command error, got: %v", err)
	}
}
