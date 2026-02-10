package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

// --- validatePlugin ---

func TestValidatePlugin_EmptyCommand(t *testing.T) {
	p := PluginConfig{Command: ""}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for empty command")
	}
}

func TestValidatePlugin_NullByteInCommand(t *testing.T) {
	p := PluginConfig{Command: "my-plugin\x00injected"}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for null byte in command")
	}
}

func TestValidatePlugin_NullByteInArgs(t *testing.T) {
	p := PluginConfig{Command: "echo", Args: []string{"safe", "bad\x00arg"}}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for null byte in args")
	}
}

func TestValidatePlugin_ShellMetaInCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{"semicolon", "cmd; rm -rf /"},
		{"pipe", "cmd | cat"},
		{"ampersand", "cmd & background"},
		{"dollar", "cmd$HOME"},
		{"backtick", "cmd`whoami`"},
		{"exclamation", "cmd!event"},
		{"parentheses open", "cmd(subshell)"},
		{"parentheses close", "cmd)"},
		{"curly open", "cmd{expansion}"},
		{"curly close", "cmd}"},
		{"angle open", "cmd<input"},
		{"angle close", "cmd>output"},
		{"backslash", "cmd\\escaped"},
		{"newline", "cmd\ninjected"},
		{"carriage return", "cmd\rinjected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PluginConfig{Command: tt.cmd}
			if err := validatePlugin(p); err == nil {
				t.Errorf("expected error for shell meta in command %q", tt.cmd)
			}
		})
	}
}

func TestValidatePlugin_ShellMetaInArgs(t *testing.T) {
	tests := []struct {
		name string
		arg  string
	}{
		{"semicolon", "arg;rm -rf /"},
		{"pipe", "arg|cat /etc/passwd"},
		{"dollar expansion", "$HOME"},
		{"backtick", "`whoami`"},
		{"redirect", ">output.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PluginConfig{Command: "echo", Args: []string{tt.arg}}
			if err := validatePlugin(p); err == nil {
				t.Errorf("expected error for shell meta in arg %q", tt.arg)
			}
		})
	}
}

func TestValidatePlugin_PathTraversalInCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{"parent dir", "../bin/evil"},
		{"deep traversal", "../../etc/evil"},
		{"embedded traversal", "safe/../evil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PluginConfig{Command: tt.cmd}
			if err := validatePlugin(p); err == nil {
				t.Errorf("expected error for path traversal in command %q", tt.cmd)
			}
		})
	}
}

func TestValidatePlugin_PathTraversalInArgs(t *testing.T) {
	p := PluginConfig{Command: "echo", Args: []string{"safe", "../../etc/passwd"}}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for path traversal in args")
	}
}

func TestValidatePlugin_RelativeWithSlash(t *testing.T) {
	// Relative paths with slashes should be rejected
	p := PluginConfig{Command: "subdir/plugin"}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for relative path with slash")
	}
}

func TestValidatePlugin_RelativeWithBackslash(t *testing.T) {
	p := PluginConfig{Command: "subdir\\plugin"}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for relative path with backslash")
	}
}

func TestValidatePlugin_InvalidCommandName(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{"space in name", "my plugin"},
		{"tab in name", "my\tplugin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PluginConfig{Command: tt.cmd}
			if err := validatePlugin(p); err == nil {
				t.Errorf("expected error for invalid command name %q", tt.cmd)
			}
		})
	}
}

func TestValidatePlugin_ValidSimpleCommand(t *testing.T) {
	// "echo" should be in PATH on all systems
	p := PluginConfig{Command: "echo", Args: []string{"hello", "world"}}
	if err := validatePlugin(p); err != nil {
		t.Errorf("expected valid for 'echo': %v", err)
	}
}

func TestValidatePlugin_ValidAbsoluteCommand(t *testing.T) {
	// Create a temporary executable
	dir := t.TempDir()
	script := filepath.Join(dir, "test-plugin.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := PluginConfig{Command: script}
	if err := validatePlugin(p); err != nil {
		t.Errorf("expected valid for absolute path: %v", err)
	}
}

func TestValidatePlugin_AbsoluteNonexistent(t *testing.T) {
	p := PluginConfig{Command: "/nonexistent/path/plugin"}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for nonexistent absolute path")
	}
}

func TestValidatePlugin_AbsoluteNotExecutable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notexec.sh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := PluginConfig{Command: file}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for non-executable file")
	}
}

func TestValidatePlugin_AbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	p := PluginConfig{Command: dir}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for directory as command")
	}
}

func TestValidatePlugin_NotFoundInPath(t *testing.T) {
	p := PluginConfig{Command: "nonexistent-command-xyz-12345"}
	if err := validatePlugin(p); err == nil {
		t.Error("expected error for command not in PATH")
	}
}

func TestValidatePlugin_SafeCommandNames(t *testing.T) {
	// These names should pass the regex check (though they may not be in PATH)
	valid := []string{
		"my-plugin",
		"my_plugin",
		"plugin.sh",
		"python3",
		"MyPlugin",
		"test123",
	}
	for _, cmd := range valid {
		if !safeCommandNameRe.MatchString(cmd) {
			t.Errorf("expected %q to match safe command name regex", cmd)
		}
	}
}

// --- shellMetaRe ---

func TestShellMetaRe_Detects(t *testing.T) {
	dangerous := []string{";", "|", "&", "$", "`", "!", "(", ")", "{", "}", "<", ">", "\\", "\n", "\r"}
	for _, c := range dangerous {
		if !shellMetaRe.MatchString("test" + c + "input") {
			t.Errorf("expected shellMetaRe to match %q", c)
		}
	}
}

func TestShellMetaRe_AllowsSafe(t *testing.T) {
	safe := []string{"hello", "my-plugin", "test_file.sh", "plugin123", "PATH", "/usr/bin/echo"}
	for _, s := range safe {
		if shellMetaRe.MatchString(s) {
			t.Errorf("expected shellMetaRe to NOT match %q", s)
		}
	}
}

// --- maxPluginOutput constant ---

func TestMaxPluginOutput(t *testing.T) {
	if maxPluginOutput != 1024*1024 {
		t.Errorf("expected maxPluginOutput=1MB, got %d", maxPluginOutput)
	}
}
