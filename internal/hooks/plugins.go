package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
)

// PluginConfig defines a custom hook plugin.
type PluginConfig struct {
	Name    string `json:"name"`
	Event   string `json:"event"`   // e.g. "UserPromptSubmit", "Stop", "SessionStart"
	Command string `json:"command"` // path to executable
	Args    []string `json:"args,omitempty"`
	Timeout int    `json:"timeout_ms,omitempty"` // default 10000
	Enabled bool   `json:"enabled"`
}

// PluginsFile holds all registered plugins.
type PluginsFile struct {
	Plugins []PluginConfig `json:"plugins"`
}

// LoadPlugins reads the plugins config from the vault.
func LoadPlugins() []PluginConfig {
	path := filepath.Join(config.VaultPath(), ".same", "plugins.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pf PluginsFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil
	}
	return pf.Plugins
}

// RunPlugins executes all enabled plugins matching the given event.
// Each plugin receives the same stdin JSON as built-in hooks.
// Plugin stdout is merged into the output context.
func RunPlugins(event string, inputJSON []byte) []string {
	plugins := LoadPlugins()
	if len(plugins) == 0 {
		return nil
	}

	var contexts []string
	for _, p := range plugins {
		if !p.Enabled || !strings.EqualFold(p.Event, event) {
			continue
		}

		timeout := time.Duration(p.Timeout) * time.Millisecond
		if timeout <= 0 {
			timeout = 10 * time.Second
		}

		ctx, err := runPlugin(p, inputJSON, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "same plugin %s: %v\n", p.Name, err)
			continue
		}
		if ctx != "" {
			contexts = append(contexts, ctx)
		}
	}

	return contexts
}

func runPlugin(p PluginConfig, inputJSON []byte, timeout time.Duration) (string, error) {
	cmd := exec.Command(p.Command, p.Args...)
	cmd.Stdin = strings.NewReader(string(inputJSON))
	cmd.Stderr = os.Stderr

	done := make(chan struct{})
	var output []byte
	var cmdErr error

	go func() {
		output, cmdErr = cmd.Output()
		close(done)
	}()

	select {
	case <-done:
		if cmdErr != nil {
			return "", fmt.Errorf("command failed: %w", cmdErr)
		}
	case <-time.After(timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", fmt.Errorf("timeout after %v", timeout)
	}

	if len(output) == 0 {
		return "", nil
	}

	// Try to parse as hook output JSON
	var hookOut HookOutput
	if err := json.Unmarshal(output, &hookOut); err == nil {
		if hookOut.HookSpecificOutput != nil && hookOut.HookSpecificOutput.AdditionalContext != "" {
			return hookOut.HookSpecificOutput.AdditionalContext, nil
		}
	}

	// Otherwise treat raw stdout as context text
	return strings.TrimSpace(string(output)), nil
}
