package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
)

// maxPluginOutput is the maximum size of plugin stdout we'll read (1 MB).
// Prevents a misbehaving plugin from causing excessive memory usage.
const maxPluginOutput = 1024 * 1024

// shellMetaRe matches characters that have special meaning in shell contexts.
// Used to reject commands/args that could enable shell injection.
var shellMetaRe = regexp.MustCompile(`[;|&$` + "`" + `!(){}<>\\\n\r]`)

// safeCommandNameRe matches a simple command name (no path separators, no metacharacters).
// Allows alphanumeric, hyphens, underscores, and dots (e.g. "python3", "my-plugin.sh").
var safeCommandNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// PluginConfig defines a custom hook plugin.
type PluginConfig struct {
	Name    string   `json:"name"`
	Event   string   `json:"event"`   // e.g. "UserPromptSubmit", "Stop", "SessionStart"
	Command string   `json:"command"` // path to executable
	Args    []string `json:"args,omitempty"`
	Timeout int      `json:"timeout_ms,omitempty"` // default 10000
	Enabled bool     `json:"enabled"`
}

// PluginsFile holds all registered plugins.
type PluginsFile struct {
	Plugins []PluginConfig `json:"plugins"`
}

// trustedPluginsPath returns the path to the user-local trusted plugins registry.
func trustedPluginsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "same", "trusted-plugins.json")
}

// trustedPluginsRegistry maps vault plugin file paths to their SHA-256 hash
// at the time the user explicitly trusted them.
type trustedPluginsRegistry struct {
	Trusted map[string]string `json:"trusted"` // vault_path -> sha256 of plugins.json
}

func loadTrustedRegistry() trustedPluginsRegistry {
	data, err := os.ReadFile(trustedPluginsPath())
	if err != nil {
		return trustedPluginsRegistry{Trusted: map[string]string{}}
	}
	var reg trustedPluginsRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return trustedPluginsRegistry{Trusted: map[string]string{}}
	}
	if reg.Trusted == nil {
		reg.Trusted = map[string]string{}
	}
	return reg
}

func saveTrustedRegistry(reg trustedPluginsRegistry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(trustedPluginsPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(trustedPluginsPath(), data, 0o600)
}

// hashFile returns the hex-encoded SHA-256 of the given file.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// TrustVaultPlugins explicitly trusts the current plugins.json for the active vault.
// Called by 'same plugins trust'.
func TrustVaultPlugins() error {
	vp := config.VaultPath()
	if vp == "" {
		return fmt.Errorf("no vault found")
	}
	pluginPath := filepath.Join(vp, ".same", "plugins.json")
	hash, err := hashFile(pluginPath)
	if err != nil {
		return fmt.Errorf("cannot read plugins file: %w", err)
	}
	reg := loadTrustedRegistry()
	absVault, _ := filepath.Abs(vp)
	reg.Trusted[absVault] = hash
	return saveTrustedRegistry(reg)
}

// isPluginsTrusted checks if the vault's plugins.json has been explicitly trusted
// and has not been modified since trust was granted.
func isPluginsTrusted(vaultPath, pluginFilePath string) bool {
	reg := loadTrustedRegistry()
	absVault, _ := filepath.Abs(vaultPath)
	trustedHash, ok := reg.Trusted[absVault]
	if !ok {
		return false
	}
	currentHash, err := hashFile(pluginFilePath)
	if err != nil {
		return false
	}
	return trustedHash == currentHash
}

// LoadPlugins reads the plugins config from the vault.
// SECURITY: Requires explicit user trust via 'same plugins trust' before
// loading vault-local plugins. This prevents supply-chain attacks where a
// malicious repo ships a .same/plugins.json that auto-executes commands.
func LoadPlugins() []PluginConfig {
	vp := config.VaultPath()
	if vp == "" {
		return nil
	}
	path := filepath.Join(vp, ".same", "plugins.json")
	if _, err := os.Stat(path); err != nil {
		return nil
	}

	// SECURITY: check trust before loading
	if !isPluginsTrusted(vp, path) {
		fmt.Fprintf(os.Stderr, "\n  \u26a0 Untrusted plugin manifest found: .same/plugins.json\n")
		fmt.Fprintf(os.Stderr, "    Run 'same plugin trust' to review and enable plugins for this vault.\n")
		fmt.Fprintf(os.Stderr, "    Skipping plugin loading for safety.\n\n")
		return nil
	}

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

// validatePlugin checks that a plugin's command and args are safe to execute.
// Returns an error describing the problem if validation fails.
//
// Rules:
//   - Command must be either an absolute path to an existing executable, or a
//     simple command name (resolved via PATH) with no shell metacharacters.
//   - Path traversal sequences ("..") are rejected in command paths.
//   - Shell metacharacters (;|&$`!(){}<>\) are rejected in both command and args.
//   - Null bytes are rejected everywhere.
func validatePlugin(p PluginConfig) error {
	if p.Command == "" {
		return fmt.Errorf("empty command")
	}

	// Reject null bytes anywhere in command or args.
	if strings.ContainsRune(p.Command, 0) {
		return fmt.Errorf("command contains null byte")
	}
	if hasControlChars(p.Command) {
		return fmt.Errorf("command contains control characters")
	}
	for i, arg := range p.Args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("arg[%d] contains null byte", i)
		}
		if hasControlChars(arg) {
			return fmt.Errorf("arg[%d] contains control characters", i)
		}
	}

	// Reject shell metacharacters in command.
	if shellMetaRe.MatchString(p.Command) {
		return fmt.Errorf("command contains shell metacharacters")
	}

	// Reject path traversal in command.
	if strings.Contains(p.Command, "..") {
		return fmt.Errorf("command contains path traversal")
	}

	if filepath.IsAbs(p.Command) {
		// Absolute path: must point to an existing regular file that is executable.
		info, err := os.Stat(p.Command)
		if err != nil {
			return fmt.Errorf("command not found: %s", p.Command)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("command is not a regular file: %s", p.Command)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("command is not executable: %s", p.Command)
		}
	} else {
		// Relative/bare name: must be a simple command name (no path separators).
		if strings.ContainsAny(p.Command, "/\\") {
			return fmt.Errorf("relative command paths not allowed (use absolute path): %s", p.Command)
		}
		if !safeCommandNameRe.MatchString(p.Command) {
			return fmt.Errorf("command name contains invalid characters: %s", p.Command)
		}
		// Verify it resolves via PATH.
		if _, err := exec.LookPath(p.Command); err != nil {
			return fmt.Errorf("command not found in PATH: %s", p.Command)
		}
	}

	// Validate args: reject shell metacharacters and path traversal.
	for i, arg := range p.Args {
		if shellMetaRe.MatchString(arg) {
			return fmt.Errorf("arg[%d] contains shell metacharacters", i)
		}
		if strings.Contains(arg, "..") {
			return fmt.Errorf("arg[%d] contains path traversal", i)
		}
	}

	return nil
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

		// SECURITY (S1): Validate command and args before execution.
		if err := validatePlugin(p); err != nil {
			fmt.Fprintf(os.Stderr, "same plugin %s: rejected: %v\n", p.Name, err)
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
			_ = cmd.Process.Kill()
		}
		return "", fmt.Errorf("timeout after %v", timeout)
	}

	if len(output) == 0 {
		return "", nil
	}

	// SECURITY (S9): Enforce output size limit to prevent OOM from misbehaving plugins.
	if len(output) > maxPluginOutput {
		return "", fmt.Errorf("output too large (%d bytes, max %d)", len(output), maxPluginOutput)
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
