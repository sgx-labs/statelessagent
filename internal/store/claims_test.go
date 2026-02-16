package store

import (
	"testing"
	"time"
)

func TestNormalizeClaimPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "simple", path: "cmd/same/main.go", want: "cmd/same/main.go"},
		{name: "cleans dots", path: "cmd/./same/../same/main.go", want: "cmd/same/main.go"},
		{name: "windows separators normalized", path: `cmd\same\main.go`, want: "cmd/same/main.go"},
		{name: "empty", path: "", wantErr: true},
		{name: "absolute", path: "/etc/passwd", wantErr: true},
		{name: "windows absolute", path: `C:\Windows\system32\drivers\etc\hosts`, wantErr: true},
		{name: "traversal", path: "../secret.md", wantErr: true},
		{name: "nested traversal", path: "notes/../../secret.md", wantErr: true},
		{name: "windows traversal", path: `notes\..\..\secret.md`, wantErr: true},
		{name: "null byte", path: "a\x00b.md", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeClaimPath(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeClaimPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestClaimsCRUD(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if err := db.UpsertClaim("internal/mcp/server.go", "codex", ClaimTypeWrite, 30*time.Minute); err != nil {
		t.Fatalf("UpsertClaim(write): %v", err)
	}
	if err := db.UpsertClaim("internal/mcp/server.go", "claude", ClaimTypeRead, 30*time.Minute); err != nil {
		t.Fatalf("UpsertClaim(read): %v", err)
	}

	active, err := db.ListActiveClaims()
	if err != nil {
		t.Fatalf("ListActiveClaims: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active claims, got %d", len(active))
	}

	readClaims, err := db.GetActiveReadClaimsForPath("internal/mcp/server.go", "codex")
	if err != nil {
		t.Fatalf("GetActiveReadClaimsForPath: %v", err)
	}
	if len(readClaims) != 1 || readClaims[0].Agent != "claude" {
		t.Fatalf("expected 1 read claim from claude, got %+v", readClaims)
	}

	removed, err := db.ReleaseClaims("internal/mcp/server.go", "claude")
	if err != nil {
		t.Fatalf("ReleaseClaims(agent): %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 row removed, got %d", removed)
	}

	removed, err = db.ReleaseClaims("internal/mcp/server.go", "")
	if err != nil {
		t.Fatalf("ReleaseClaims(path): %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 row removed, got %d", removed)
	}

	active, err = db.ListActiveClaims()
	if err != nil {
		t.Fatalf("ListActiveClaims after release: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active claims after release, got %d", len(active))
	}
}

func TestClaimExpiryAndPurge(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if err := db.UpsertClaim("README.md", "codex", ClaimTypeWrite, 1*time.Second); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)

	active, err := db.ListActiveClaims()
	if err != nil {
		t.Fatalf("ListActiveClaims: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected expired claim to be hidden, got %d active", len(active))
	}

	purged, err := db.PurgeExpiredClaims()
	if err != nil {
		t.Fatalf("PurgeExpiredClaims: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged claim, got %d", purged)
	}
}
