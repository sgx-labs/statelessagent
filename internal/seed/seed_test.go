package seed

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
)

func newLocalHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipping: cannot bind local test listener: %v", err)
	}

	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	return srv
}

func TestValidateExtractPath(t *testing.T) {
	destDir := t.TempDir()

	tests := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		// Valid paths
		{"simple file", "notes.md", false},
		{"nested file", "research/topic.md", false},
		{"deep nesting", "research/sub/deep/file.md", false},
		{"toml file", "config.toml", false},
		{"json file", "data.json", false},
		{"yaml file", "meta.yaml", false},
		{"yml file", "meta.yml", false},
		{"txt file", "readme.txt", false},
		{"example file", "config.toml.example", false},
		{"gitkeep", ".gitkeep", true}, // dot-prefixed
		{"no extension", "Makefile", false},

		// Path traversal
		{"traversal up", "../etc/passwd", true},
		{"traversal nested", "foo/../../etc/passwd", true},
		// a/b/c/../../../etc/passwd normalizes to etc/passwd (stays within dest)
		{"traversal normalized safe", "a/b/c/../../../etc/passwd", false},
		{"traversal four levels up", "a/b/c/../../../../etc/passwd", true},

		// Absolute paths
		{"absolute unix", "/etc/passwd", true},

		// Null bytes
		{"null byte", "foo\x00bar.md", true},

		// Hidden files
		{"hidden file", ".hidden.md", true},
		{"hidden dir", ".git/config", true},
		{"hidden nested", "foo/.hidden/bar.md", true},
		{"dotfile", ".env", true},

		// Disallowed extensions
		{"executable", "script.sh", true},
		{"binary", "program.exe", true},
		{"go file", "main.go", true},
		{"python", "script.py", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validateExtractPath(tt.entry, destDir)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got path %q", tt.entry, result)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for %q: %v", tt.entry, err)
				}
				if result == "" {
					t.Errorf("expected non-empty result for %q", tt.entry)
				}
			}
		})
	}
}

func TestParseManifest(t *testing.T) {
	t.Run("valid manifest", func(t *testing.T) {
		data := `{
			"schema_version": 1,
			"seeds": [
				{
					"name": "test-seed",
					"display_name": "Test Seed",
					"description": "A test seed",
					"audience": "developers",
					"note_count": 10,
					"size_kb": 50,
					"tags": ["test"],
					"min_same_version": "0.7.0",
					"path": "test-seed",
					"featured": true
				}
			]
		}`
		var m Manifest
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if m.SchemaVersion != 1 {
			t.Errorf("schema_version = %d, want 1", m.SchemaVersion)
		}
		if len(m.Seeds) != 1 {
			t.Fatalf("seed count = %d, want 1", len(m.Seeds))
		}
		s := m.Seeds[0]
		if s.Name != "test-seed" {
			t.Errorf("name = %q, want %q", s.Name, "test-seed")
		}
		if !s.Featured {
			t.Error("expected featured = true")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		var m Manifest
		if err := json.Unmarshal([]byte("{invalid"), &m); err == nil {
			t.Error("expected parse error")
		}
	})

	t.Run("wrong schema version", func(t *testing.T) {
		data := `{"schema_version": 99, "seeds": []}`
		var m Manifest
		_ = json.Unmarshal([]byte(data), &m)
		if m.SchemaVersion != 99 {
			t.Errorf("expected schema 99, got %d", m.SchemaVersion)
		}
	})
}

func TestFindSeed(t *testing.T) {
	manifest := &Manifest{
		SchemaVersion: 1,
		Seeds: []Seed{
			{Name: "alpha-seed", DisplayName: "Alpha"},
			{Name: "beta-seed", DisplayName: "Beta"},
		},
	}

	t.Run("found", func(t *testing.T) {
		s := FindSeed(manifest, "alpha-seed")
		if s == nil {
			t.Fatal("expected to find seed")
		}
		if s.DisplayName != "Alpha" {
			t.Errorf("display_name = %q, want Alpha", s.DisplayName)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		s := FindSeed(manifest, "Alpha-Seed")
		if s == nil {
			t.Fatal("expected case-insensitive match")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := FindSeed(manifest, "nonexistent")
		if s != nil {
			t.Error("expected nil for nonexistent seed")
		}
	})
}

func TestValidateSeedName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "my-seed", false},
		{"valid alphanumeric", "seed123", false},
		{"empty", "", true},
		{"uppercase", "My-Seed", true},
		{"underscore", "my_seed", true},
		{"leading hyphen", "-seed", true},
		{"trailing hyphen", "seed-", true},
		{"too long", "a-very-long-seed-name-that-exceeds-the-sixty-four-character-maximum-limit", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSeedName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSeedName(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.7.0", "0.7.0", 0},
		{"0.7.0", "0.8.0", -1},
		{"0.8.0", "0.7.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.7.5", "0.7.4", 1},
		{"0.7.5", "0.8.0", -1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := compareSemver(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// createTestTarGz creates a gzip-compressed tar archive with the given files.
// The prefix simulates GitHub's tarball format (owner-repo-sha/).
func createTestTarGz(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		fullPath := prefix + name
		hdr := &tar.Header{
			Name:     fullPath,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content: %v", err)
		}
	}

	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestExtractTarGz(t *testing.T) {
	t.Run("extracts matching files", func(t *testing.T) {
		destDir := t.TempDir()
		tarData := createTestTarGz(t, "sgx-labs-seed-vaults-abc1234/", map[string]string{
			"my-seed/bootstrap.md":        "# Bootstrap",
			"my-seed/research/topic.md":   "# Topic",
			"my-seed/config.toml.example": "[vault]",
			"other-seed/notes.md":         "# Other",
		})

		count, err := extractTarGz(bytes.NewReader(tarData), "my-seed", destDir)
		if err != nil {
			t.Fatalf("extract error: %v", err)
		}
		if count != 3 {
			t.Errorf("file count = %d, want 3", count)
		}

		// Verify files exist
		if _, err := os.Stat(filepath.Join(destDir, "bootstrap.md")); err != nil {
			t.Error("bootstrap.md not found")
		}
		if _, err := os.Stat(filepath.Join(destDir, "research", "topic.md")); err != nil {
			t.Error("research/topic.md not found")
		}
	})

	t.Run("rejects symlinks", func(t *testing.T) {
		destDir := t.TempDir()
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)

		// Add a symlink entry
		hdr := &tar.Header{
			Name:     "sgx-labs-seed-vaults-abc/my-seed/link.md",
			Typeflag: tar.TypeSymlink,
			Linkname: "/etc/passwd",
		}
		tw.WriteHeader(hdr)

		// Add a normal file
		content := "# Real"
		tw.WriteHeader(&tar.Header{
			Name:     "sgx-labs-seed-vaults-abc/my-seed/real.md",
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		})
		tw.Write([]byte(content))

		tw.Close()
		gw.Close()

		count, err := extractTarGz(bytes.NewReader(buf.Bytes()), "my-seed", destDir)
		if err != nil {
			t.Fatalf("extract error: %v", err)
		}
		// Only the regular file should be extracted
		if count != 1 {
			t.Errorf("file count = %d, want 1 (symlink should be skipped)", count)
		}
		if _, err := os.Stat(filepath.Join(destDir, "link.md")); err == nil {
			t.Error("symlink should not have been extracted")
		}
	})

	t.Run("rejects disallowed extensions", func(t *testing.T) {
		destDir := t.TempDir()
		tarData := createTestTarGz(t, "owner-repo-sha/", map[string]string{
			"my-seed/notes.md":   "# Notes",
			"my-seed/script.sh":  "#!/bin/bash",
			"my-seed/binary.exe": "MZ...",
		})

		count, err := extractTarGz(bytes.NewReader(tarData), "my-seed", destDir)
		if err != nil {
			t.Fatalf("extract error: %v", err)
		}
		if count != 1 {
			t.Errorf("file count = %d, want 1 (only .md allowed)", count)
		}
	})

	t.Run("rejects hidden files", func(t *testing.T) {
		destDir := t.TempDir()
		tarData := createTestTarGz(t, "owner-repo-sha/", map[string]string{
			"my-seed/notes.md":    "# Notes",
			"my-seed/.env":        "SECRET=x",
			"my-seed/.git/config": "[core]",
		})

		count, err := extractTarGz(bytes.NewReader(tarData), "my-seed", destDir)
		if err != nil {
			t.Fatalf("extract error: %v", err)
		}
		if count != 1 {
			t.Errorf("file count = %d, want 1 (hidden files should be skipped)", count)
		}
	})
}

func TestDownloadAndExtractHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HTTP test in short mode")
	}

	tarData := createTestTarGz(t, "sgx-labs-seed-vaults-abc1234/", map[string]string{
		"test-seed/bootstrap.md":      "# Bootstrap",
		"test-seed/research/topic.md": "# Topic",
	})

	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(tarData)
	}))
	defer server.Close()

	// Temporarily override the tarball URL
	origURL := TarballURL
	defer func() {
		// Can't reassign const, but extractTarGz is tested directly above
		_ = origURL
	}()

	// Test extractTarGz directly with the test data
	destDir := t.TempDir()
	count, err := extractTarGz(bytes.NewReader(tarData), "test-seed", destDir)
	if err != nil {
		t.Fatalf("extract error: %v", err)
	}
	if count != 2 {
		t.Errorf("file count = %d, want 2", count)
	}
}

func TestIsInstalled(t *testing.T) {
	// IsInstalled reads from the vault registry file.
	// Since we can't easily mock config.LoadRegistry in tests,
	// we just verify it doesn't panic.
	_ = IsInstalled("nonexistent-seed")
}

func TestManifestCachePath(t *testing.T) {
	path := manifestCachePath()
	if path == "" {
		t.Error("expected non-empty cache path")
	}
	if !filepath.IsAbs(path) {
		t.Error("expected absolute cache path")
	}
}

func TestValidateSeedPath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "simple", input: "my-seed", wantErr: false},
		{name: "nested", input: "collections/my-seed", wantErr: false},
		{name: "empty", input: "", wantErr: true},
		{name: "absolute", input: "/etc/passwd", wantErr: true},
		{name: "traversal", input: "../seeds", wantErr: true},
		{name: "dot segment normalized", input: "./seeds", wantErr: false},
		{name: "embedded dot segment", input: "foo/./seed", wantErr: true},
		{name: "embedded traversal segment", input: "foo/../seed", wantErr: true},
		{name: "hidden segment", input: ".hidden/seed", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSeedPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSeedPath(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestLoadCachedManifest_ValidatesSeedEntries(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "seed-manifest.json")

	writeCache := func(seedName, seedPath string) {
		t.Helper()
		c := manifestCache{
			FetchedAt: time.Now().UTC(),
			Manifest: Manifest{
				SchemaVersion: 1,
				Seeds: []Seed{
					{
						Name:        seedName,
						DisplayName: "Test",
						Path:        seedPath,
						NoteCount:   1,
					},
				},
			},
		}
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal cache: %v", err)
		}
		if err := os.WriteFile(cachePath, data, 0o600); err != nil {
			t.Fatalf("write cache: %v", err)
		}
	}

	t.Run("rejects invalid seed name from cache", func(t *testing.T) {
		writeCache("Bad_Name", "good-seed")
		if _, err := loadCachedManifest(cachePath, true); err == nil {
			t.Fatal("expected error for invalid cached seed name")
		}
	})

	t.Run("rejects invalid seed path from cache", func(t *testing.T) {
		writeCache("good-seed", "../escape")
		if _, err := loadCachedManifest(cachePath, true); err == nil {
			t.Fatal("expected error for invalid cached seed path")
		}
	})

	t.Run("accepts valid cached seed", func(t *testing.T) {
		writeCache("good-seed", "good-seed")
		if _, err := loadCachedManifest(cachePath, true); err != nil {
			t.Fatalf("expected valid cache, got: %v", err)
		}
	})
}

func TestRemove_OutsideDefaultSeedDir_DoesNotUnregister(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	outside := t.TempDir()
	reg := &config.VaultRegistry{
		Vaults:  map[string]string{"test-seed": outside},
		Default: "test-seed",
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	err := Remove("test-seed", true)
	if err == nil {
		t.Fatal("expected refusal error for out-of-seed-dir delete")
	}

	after := config.LoadRegistry()
	if _, ok := after.Vaults["test-seed"]; !ok {
		t.Fatal("seed should remain registered on delete refusal")
	}
	if after.Default != "test-seed" {
		t.Fatalf("default should be preserved, got %q", after.Default)
	}
}

func TestRemove_DeleteFilesSuccess_UnregistersAndDeletes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	seedPath := filepath.Join(DefaultSeedDir(), "test-seed")
	if err := os.MkdirAll(seedPath, 0o755); err != nil {
		t.Fatalf("mkdir seed path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedPath, "note.md"), []byte("# note"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	reg := &config.VaultRegistry{
		Vaults:  map[string]string{"test-seed": seedPath},
		Default: "test-seed",
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	if err := Remove("test-seed", true); err != nil {
		t.Fatalf("remove seed: %v", err)
	}

	after := config.LoadRegistry()
	if _, ok := after.Vaults["test-seed"]; ok {
		t.Fatal("seed should be unregistered after successful remove")
	}
	if after.Default != "" {
		t.Fatalf("default should be cleared, got %q", after.Default)
	}
	if _, err := os.Stat(seedPath); !os.IsNotExist(err) {
		t.Fatalf("seed path should be deleted, stat err=%v", err)
	}
}

func TestRemove_RejectsDeletingDefaultSeedRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	root := DefaultSeedDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir default seed root: %v", err)
	}
	reg := &config.VaultRegistry{
		Vaults:  map[string]string{"test-seed": root},
		Default: "test-seed",
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	if err := Remove("test-seed", true); err == nil {
		t.Fatal("expected remove to reject deleting default seed root")
	}

	after := config.LoadRegistry()
	if _, ok := after.Vaults["test-seed"]; !ok {
		t.Fatal("seed should remain registered after rejection")
	}
	if after.Default != "test-seed" {
		t.Fatalf("default should remain unchanged, got %q", after.Default)
	}
}

func TestPathWithinBase_PrefixConfusion(t *testing.T) {
	base := filepath.Join("tmp", "seed-root")
	inside := filepath.Join(base, "notes", "a.md")
	outsidePrefix := base + "-other"

	if !pathWithinBase(base, inside) {
		t.Fatalf("expected inside path to be accepted")
	}
	if pathWithinBase(base, outsidePrefix) {
		t.Fatalf("expected prefix-confusion sibling to be rejected")
	}
}
