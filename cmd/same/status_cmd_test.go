package main

import (
	"strings"
	"testing"
)

func TestActiveVaultSource(t *testing.T) {
	tests := []struct {
		name          string
		vaultOverride string
		cwd           string
		vaultPath     string
		activeName    string
		defaultName   string
		want          string
	}{
		{
			name:          "override wins",
			vaultOverride: "demo-vault",
			cwd:           "/tmp/project",
			vaultPath:     "/tmp/project",
			activeName:    "main",
			defaultName:   "main",
			want:          "via --vault flag",
		},
		{
			name:        "cwd auto-detect",
			cwd:         "/tmp/project",
			vaultPath:   "/tmp/project",
			activeName:  "main",
			defaultName: "other",
			want:        "auto-detected from cwd",
		},
		{
			name:        "registry default",
			cwd:         "/tmp/other",
			vaultPath:   "/tmp/project",
			activeName:  "main",
			defaultName: "main",
			want:        "registry default",
		},
		{
			name:        "no source hint",
			cwd:         "/tmp/other",
			vaultPath:   "/tmp/project",
			activeName:  "scratch",
			defaultName: "main",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := activeVaultSource(tt.vaultOverride, tt.cwd, tt.vaultPath, tt.activeName, tt.defaultName)
			if got != tt.want {
				t.Fatalf("activeVaultSource() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectChatStatusDisabled(t *testing.T) {
	t.Setenv("SAME_CHAT_PROVIDER", "none")
	t.Setenv("SAME_CHAT_MODEL", "")
	t.Setenv("SAME_CHAT_BASE_URL", "")
	t.Setenv("SAME_CHAT_API_KEY", "")

	st := detectChatStatus()
	if st.Status != "disabled" {
		t.Fatalf("status = %q, want disabled", st.Status)
	}
	if st.Provider != "none" {
		t.Fatalf("provider = %q, want none", st.Provider)
	}
}

func TestDetectChatStatusMissingOpenAIKey(t *testing.T) {
	t.Setenv("SAME_CHAT_PROVIDER", "openai")
	t.Setenv("SAME_CHAT_MODEL", "")
	t.Setenv("SAME_CHAT_BASE_URL", "")
	t.Setenv("SAME_CHAT_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	st := detectChatStatus()
	if st.Status != "unavailable" {
		t.Fatalf("status = %q, want unavailable", st.Status)
	}
	if st.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", st.Provider)
	}
	errLower := strings.ToLower(st.Error)
	if !strings.Contains(errLower, "api_key") && !strings.Contains(errLower, "openai_api_key") {
		t.Fatalf("error = %q, want api key hint", st.Error)
	}
}
