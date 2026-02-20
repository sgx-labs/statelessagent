package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelCmd_ShowCurrent(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	t.Setenv("SAME_EMBED_PROVIDER", "none")
	t.Setenv("SAME_EMBED_MODEL", "snowflake-arctic-embed2")

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = showCurrentModel()
	})
	if runErr != nil {
		t.Fatalf("showCurrentModel: %v", runErr)
	}
	if !strings.Contains(out, "Embedding Model:") {
		t.Fatalf("expected embedding model heading, got: %q", out)
	}
	if !strings.Contains(out, "snowflake-arctic-embed2") {
		t.Fatalf("expected current model in output, got: %q", out)
	}
}

func TestModelCmd_ListModels(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	t.Setenv("SAME_EMBED_PROVIDER", "none")

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = showCurrentModel()
	})
	if runErr != nil {
		t.Fatalf("showCurrentModel: %v", runErr)
	}
	if !strings.Contains(out, "nomic-embed-text") {
		t.Fatalf("expected nomic-embed-text in available model list, got: %q", out)
	}
	if !strings.Contains(out, "snowflake-arctic-embed2") {
		t.Fatalf("expected snowflake-arctic-embed2 in available model list, got: %q", out)
	}
}

func TestModelCmd_UseInvalidModel(t *testing.T) {
	vault, db := setupCommandTestVault(t)
	_ = db.Close()

	t.Setenv("SAME_EMBED_PROVIDER", "none")

	const invalidModel = "this-model-does-not-exist"

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = setModel(invalidModel)
	})
	if runErr != nil {
		t.Fatalf("setModel: %v", runErr)
	}
	if !strings.Contains(out, "Warning") {
		t.Fatalf("expected warning for unknown model, got: %q", out)
	}
	if !strings.Contains(out, "not a recognized model") {
		t.Fatalf("expected unknown-model warning text, got: %q", out)
	}

	cfgPath := filepath.Join(vault, ".same", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `model = "`+invalidModel+`"`) {
		t.Fatalf("expected config to persist model %q, got: %q", invalidModel, string(data))
	}
}
