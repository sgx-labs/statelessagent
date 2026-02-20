package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCompletionCmd_Bash(t *testing.T) {
	root := &cobra.Command{Use: "same"}
	root.AddCommand(completionCmd())

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"completion", "bash"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !strings.Contains(out.String(), "bash completion for same") {
		t.Fatalf("expected bash completion output, got: %q", out.String())
	}
}

func TestCompletionCmd_Zsh(t *testing.T) {
	root := &cobra.Command{Use: "same"}
	root.AddCommand(completionCmd())

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"completion", "zsh"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("expected zsh completion output")
	}
}

func TestCompletionCmd_Fish(t *testing.T) {
	root := &cobra.Command{Use: "same"}
	root.AddCommand(completionCmd())

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"completion", "fish"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("expected fish completion output")
	}
}

func TestCompletionCmd_InvalidShell(t *testing.T) {
	root := &cobra.Command{Use: "same"}
	root.AddCommand(completionCmd())

	root.SetArgs([]string{"completion", "powershell"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unsupported shell")
	}
}

func TestCompletionCmd_NoArgs(t *testing.T) {
	root := &cobra.Command{Use: "same"}
	root.AddCommand(completionCmd())

	root.SetArgs([]string{"completion"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for missing shell argument")
	}
}
