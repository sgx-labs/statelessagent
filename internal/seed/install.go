package seed

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// PrintLegalNotice prints the standard legal disclaimer for seed content.
// Call this once after any seed install to avoid duplicating the notice.
func PrintLegalNotice() {
	fmt.Printf("\n  %sNote: Seed content is AI-generated and provided as-is.%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sSee LICENSE in the seed directory for details.%s\n", cli.Dim, cli.Reset)
}

// DefaultSeedDir returns the default parent directory for seed installations.
func DefaultSeedDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "same-seeds")
}

// InstallOptions controls the install behavior.
type InstallOptions struct {
	Name    string // seed name from manifest
	Path    string // custom install path (empty = ~/same-seeds/<name>)
	Force   bool   // overwrite existing directory
	NoIndex bool   // skip reindex step
	Version string // current SAME version for compatibility check

	// Progress callbacks (all optional)
	OnDownloadStart func()
	OnDownloadDone  func(sizeKB int)
	OnExtractDone   func(fileCount int)
	OnIndexDone     func(chunks int)
}

// InstallResult holds the outcome of a successful install.
type InstallResult struct {
	DestDir   string
	FileCount int
	Chunks    int
}

// Install downloads and installs a seed vault.
func Install(opts InstallOptions) (*InstallResult, error) {
	// 1. Fetch manifest
	manifest, err := FetchManifest(false)
	if err != nil {
		return nil, fmt.Errorf("fetch seed list: %w", err)
	}

	// 2. Find seed
	seed := FindSeed(manifest, opts.Name)
	if seed == nil {
		return nil, fmt.Errorf("seed %q not found — run 'same seed list' to see available seeds", opts.Name)
	}

	// 3. Version compatibility check
	if seed.MinSameVersion != "" && opts.Version != "" && opts.Version != "dev" {
		if compareSemver(opts.Version, seed.MinSameVersion) < 0 {
			return nil, fmt.Errorf("seed %q requires SAME v%s or later (you have v%s) — run 'same update'",
				seed.Name, seed.MinSameVersion, opts.Version)
		}
	}

	// 3b. Reject seeds with no content (before download)
	if seed.NoteCount == 0 {
		return nil, fmt.Errorf("seed %q has no content", seed.Name)
	}

	// 4. Resolve destination path
	destDir := opts.Path
	if destDir == "" {
		destDir = filepath.Join(DefaultSeedDir(), seed.Name)
	}
	absDir, err := filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	if isDangerousInstallDestination(absDir) {
		return nil, fmt.Errorf(
			"refusing dangerous install destination %s — choose a dedicated subdirectory (example: %s)",
			absDir,
			filepath.Join(DefaultSeedDir(), seed.Name),
		)
	}

	// 4b. Reject installing into CWD when an explicit path was given
	if opts.Path != "" {
		cwd, _ := os.Getwd()
		if absDir == cwd {
			return nil, fmt.Errorf("refusing to install into current directory — use ~/same-seeds/<name> or a dedicated path")
		}
	}

	// 5. Check if directory exists
	if info, err := os.Stat(absDir); err == nil && info.IsDir() {
		if !opts.Force {
			return nil, fmt.Errorf("directory already exists: %s — use --force to overwrite", absDir)
		}
		// Remove existing to start fresh
		if err := os.RemoveAll(absDir); err != nil {
			return nil, fmt.Errorf("remove existing directory: %w", err)
		}
	}

	// 6. Create directory
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	// Cleanup on failure
	success := false
	defer func() {
		if !success {
			if err := os.RemoveAll(absDir); err != nil {
				fmt.Fprintf(os.Stderr, "same: warning: failed to clean up partial seed install at %q: %v\n", absDir, err)
			}
		}
	}()

	// 7. Download and extract
	if opts.OnDownloadStart != nil {
		opts.OnDownloadStart()
	}

	fileCount, err := DownloadAndExtract(seed.Path, absDir)
	if err != nil {
		return nil, fmt.Errorf("download seed: %w", err)
	}
	if fileCount == 0 {
		return nil, fmt.Errorf("seed %q is empty — no files extracted", seed.Name)
	}

	if opts.OnDownloadDone != nil {
		opts.OnDownloadDone(seed.SizeKB)
	}
	if opts.OnExtractDone != nil {
		opts.OnExtractDone(fileCount)
	}

	// 8. Copy config.toml.example -> .same/config.toml (if config.toml.example exists)
	sameDir := filepath.Join(absDir, ".same")
	dataDir := filepath.Join(sameDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create .same/data: %w", err)
	}

	exampleConfig := filepath.Join(absDir, "config.toml.example")
	configDest := filepath.Join(sameDir, "config.toml")
	if _, err := os.Stat(exampleConfig); err == nil {
		if err := copyFile(exampleConfig, configDest); err != nil {
			return nil, fmt.Errorf("copy config: %w", err)
		}
		// Fix vault path: seed configs often use path = "." which resolves
		// to CWD instead of the seed directory. Rewrite to absolute path.
		fixConfigVaultPath(configDest, absDir)
	} else {
		// Generate a minimal config pointing at this vault
		config.GenerateConfig(absDir)
	}

	// 9. Reindex (unless --no-index)
	var chunks int
	if !opts.NoIndex {
		// Point the config at the seed vault for indexing
		origOverride := config.VaultOverride
		config.VaultOverride = absDir
		defer func() { config.VaultOverride = origOverride }()

		// Set VAULT_PATH env as belt-and-suspenders — ensures the indexer
		// uses the seed directory even if config resolution picks up CWD.
		origEnv := os.Getenv("VAULT_PATH")
		os.Setenv("VAULT_PATH", absDir)
		defer func() {
			if origEnv != "" {
				os.Setenv("VAULT_PATH", origEnv)
			} else {
				os.Unsetenv("VAULT_PATH")
			}
		}()

		dbPath := filepath.Join(dataDir, "vault.db")
		db, err := store.OpenPath(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		indexer.Version = opts.Version

		// Progress bar instead of per-file output
		barWidth := 40
		progress := func(current, total int, path string) {
			if total == 0 {
				return
			}
			filled := current * barWidth / total
			bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
			fmt.Printf("\r  Indexing [%s] %d/%d", bar, current, total)
		}

		stats, err := indexer.ReindexWithProgress(db, true, progress)
		if err != nil {
			// Try lite mode if Ollama isn't available
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "ollama") || strings.Contains(errMsg, "connection") || strings.Contains(errMsg, "refused") {
				stats, err = indexer.ReindexLite(db, true, progress)
				if err != nil {
					return nil, fmt.Errorf("index seed: %w", err)
				}
			} else {
				return nil, fmt.Errorf("index seed: %w", err)
			}
		}
		fmt.Println() // newline after progress bar
		if stats != nil {
			chunks = stats.ChunksInIndex
		}
	}

	if opts.OnIndexDone != nil {
		opts.OnIndexDone(chunks)
	}

	// 10. Register in vault registry
	reg := config.LoadRegistry()
	reg.Vaults[seed.Name] = absDir
	if err := reg.Save(); err != nil {
		return nil, fmt.Errorf("register vault: %w", err)
	}

	success = true
	return &InstallResult{
		DestDir:   absDir,
		FileCount: fileCount,
		Chunks:    chunks,
	}, nil
}

// Remove unregisters a seed vault and optionally deletes its files.
func Remove(name string, deleteFiles bool) error {
	reg := config.LoadRegistry()
	vaultPath, ok := reg.Vaults[name]
	if !ok {
		return fmt.Errorf("seed %q is not installed — run 'same seed list' to see installed seeds", name)
	}

	wasDefault := reg.Default == name
	absPath := ""
	absSeedDir := ""

	// Pre-flight deletion safety checks before mutating registry state.
	if deleteFiles {
		var err error
		absPath, err = filepath.Abs(vaultPath)
		if err != nil {
			return fmt.Errorf("resolve path: %w", err)
		}
		seedDir := DefaultSeedDir()
		absSeedDir, err = filepath.Abs(seedDir)
		if err != nil {
			return fmt.Errorf("resolve seed dir: %w", err)
		}
		if absPath == absSeedDir || !pathWithin(absSeedDir, absPath) {
			return fmt.Errorf("refusing to delete %s — not under %s (use 'same vault remove %s' instead)",
				absPath, seedDir, name)
		}
	}

	// Unregister
	delete(reg.Vaults, name)
	if wasDefault {
		reg.Default = ""
	}
	if err := reg.Save(); err != nil {
		return fmt.Errorf("update registry: %w", err)
	}

	// Optionally delete files
	if deleteFiles {
		if err := os.RemoveAll(absPath); err != nil {
			// Best-effort rollback to keep registry and filesystem consistent.
			rollback := config.LoadRegistry()
			rollback.Vaults[name] = vaultPath
			if wasDefault && rollback.Default == "" {
				rollback.Default = name
			}
			_ = rollback.Save()
			return fmt.Errorf("delete seed files: %w", err)
		}
	}

	return nil
}

func pathWithin(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

func isDangerousInstallDestination(absDir string) bool {
	clean := filepath.Clean(absDir)
	if clean == string(filepath.Separator) {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && samePath(clean, filepath.Clean(home)) {
		return true
	}
	if samePath(clean, filepath.Clean(DefaultSeedDir())) {
		return true
	}
	return false
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// IsInstalled checks if a seed is registered in the vault registry.
func IsInstalled(name string) bool {
	reg := config.LoadRegistry()
	_, ok := reg.Vaults[name]
	return ok
}

// fixConfigVaultPath reads a TOML config file and rewrites any relative
// vault path to the given absolute path. Seed configs commonly ship with
// path = "." which would resolve to CWD at runtime instead of the seed dir.
// Only rewrites the path key inside the [vault] section; skips comments
// and keys in other sections.
func fixConfigVaultPath(configPath, absVaultPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	content := string(data)

	lines := strings.Split(content, "\n")
	inVaultSection := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track TOML sections
		if strings.HasPrefix(trimmed, "[") {
			inVaultSection = (trimmed == "[vault]")
			continue
		}

		// Skip comments and non-vault sections
		if !inVaultSection || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Match exact "path" key (not path_override, pathology, etc.)
		if !strings.HasPrefix(trimmed, "path") {
			continue
		}
		after := trimmed[len("path"):]
		if len(after) == 0 || (after[0] != ' ' && after[0] != '=' && after[0] != '\t') {
			continue // not the path key
		}

		// Extract the value after "path ="
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		if val == "" {
			continue
		}
		// Rewrite if relative (doesn't start with /)
		if !filepath.IsAbs(val) {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf("%spath = %q", indent, absVaultPath)
		}
	}

	fixed := strings.Join(lines, "\n")
	if fixed != content {
		if err := os.WriteFile(configPath, []byte(fixed), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to rewrite seed config path in %q: %v\n", configPath, err)
		}
	}
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = in.Close()
		return err
	}

	_, copyErr := io.Copy(out, in)
	outCloseErr := out.Close()
	inCloseErr := in.Close()
	if copyErr != nil {
		return copyErr
	}
	if outCloseErr != nil {
		return outCloseErr
	}
	if inCloseErr != nil {
		return inCloseErr
	}
	return nil
}

// compareSemver compares two semver strings (without "v" prefix).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareSemver(a, b string) int {
	parse := func(s string) (int, int, int) {
		s = strings.TrimPrefix(s, "v")
		if idx := strings.IndexByte(s, '-'); idx >= 0 {
			s = s[:idx]
		}
		parts := strings.Split(s, ".")
		var major, minor, patch int
		if len(parts) >= 1 {
			fmt.Sscanf(parts[0], "%d", &major)
		}
		if len(parts) >= 2 {
			fmt.Sscanf(parts[1], "%d", &minor)
		}
		if len(parts) >= 3 {
			fmt.Sscanf(parts[2], "%d", &patch)
		}
		return major, minor, patch
	}

	aMaj, aMin, aPat := parse(a)
	bMaj, bMin, bPat := parse(b)

	if aMaj != bMaj {
		if aMaj < bMaj {
			return -1
		}
		return 1
	}
	if aMin != bMin {
		if aMin < bMin {
			return -1
		}
		return 1
	}
	if aPat != bPat {
		if aPat < bPat {
			return -1
		}
		return 1
	}
	return 0
}
