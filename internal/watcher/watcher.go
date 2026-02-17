// Package watcher monitors a vault for file changes and triggers incremental reindexing.
package watcher

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// Watch starts watching the vault for changes and reindexes modified files.
// It blocks until the context is done or an unrecoverable error occurs.
func Watch(db *store.DB) error {
	vaultPath := config.VaultPath()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	// Add all directories (not skipped ones)
	dirs := walkDirs(vaultPath)
	for _, d := range dirs {
		if err := w.Add(d); err != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] Could not watch %s: %v\n", d, err)
		}
	}

	fmt.Fprintf(os.Stderr, "Watching %d directories in %s\n", len(dirs), vaultPath)
	fmt.Fprintf(os.Stderr, "Press Ctrl+C to stop.\n\n")

	// Debounce: collect changed files over a window before reindexing
	var (
		mu      sync.Mutex
		pending = make(map[string]bool)
		timer   *time.Timer
	)

	const debounceDelay = 2 * time.Second

	flush := func() {
		mu.Lock()
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		pending = make(map[string]bool)
		mu.Unlock()

		if len(paths) == 0 {
			return
		}

		fmt.Fprintf(os.Stderr, "  Reindexing %d changed file(s)...\n", len(paths))
		reindexFiles(db, paths, vaultPath)
	}

	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}

			// Only care about markdown files (skip meta-docs)
			if !strings.HasSuffix(event.Name, ".md") || config.SkipFiles[filepath.Base(event.Name)] {
				// But watch new directories
				if event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						name := filepath.Base(event.Name)
						if !config.SkipDirs[name] {
							if err := w.Add(event.Name); err != nil {
								fmt.Fprintf(os.Stderr, "  [WARN] Could not watch %s: %v\n", event.Name, err)
							}
						}
					}
				}
				continue
			}

			if event.Has(fsnotify.Rename) {
				// fsnotify rename events refer to the old path. Remove that entry
				// from the index so stale paths don't survive file moves.
				removeFromIndex(db, event.Name, vaultPath)
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				mu.Lock()
				pending[event.Name] = true
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(debounceDelay, flush)
				mu.Unlock()
			}

			if event.Has(fsnotify.Remove) {
				removeFromIndex(db, event.Name, vaultPath)
			}

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "  [WARN] Watch error: %v\n", err)
		}
	}
}

func reindexFiles(db *store.DB, paths []string, vaultPath string) {
	ec := config.EmbeddingProviderConfig()
	provCfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
	}
	// For ollama provider, use the legacy [ollama] URL if no base_url is set.
	if (provCfg.Provider == "ollama" || provCfg.Provider == "") && provCfg.BaseURL == "" {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] ollama URL: %v\n", err)
			return
		}
		provCfg.BaseURL = ollamaURL
	}

	liteMode := false
	embedClient, err := embedding.NewProvider(provCfg)
	if err != nil {
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "keyword-only mode") || strings.Contains(errMsg, `provider is "none"`) {
			liteMode = true
		} else {
			fmt.Fprintf(os.Stderr, "  [ERROR] embedding provider: %v\n", err)
			return
		}
	}

	for _, fp := range paths {
		relPath := relativePath(fp, vaultPath)
		info, statErr := os.Stat(fp)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				// File disappeared before debounce flush (common on renames/deletes).
				removeFromIndex(db, fp, vaultPath)
			} else {
				fmt.Fprintf(os.Stderr, "  [ERROR] stat %s: %v\n", relPath, statErr)
			}
			continue
		}
		if info.IsDir() {
			continue
		}

		var err error
		if liteMode {
			err = indexer.IndexSingleFileLite(db, fp, relPath, vaultPath)
		} else {
			err = indexer.IndexSingleFile(db, fp, relPath, vaultPath, embedClient)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s: %v\n", relPath, err)
			continue
		}

		if liteMode {
			fmt.Fprintf(os.Stderr, "  Indexed (lite): %s\n", relPath)
		} else {
			fmt.Fprintf(os.Stderr, "  Indexed: %s\n", relPath)
		}
	}
}

func removeFromIndex(db *store.DB, absPath, vaultPath string) {
	relPath := relativePath(absPath, vaultPath)
	if err := db.DeleteByPath(relPath); err != nil {
		fmt.Fprintf(os.Stderr, "  [ERROR] remove %s: %v\n", relPath, err)
		return
	}
	fmt.Fprintf(os.Stderr, "  Removed from index: %s\n", relPath)
}

func walkDirs(root string) []string {
	var dirs []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if config.SkipDirs[name] {
				return filepath.SkipDir
			}
			dirs = append(dirs, path)
		}
		return nil
	})
	return dirs
}

func relativePath(filePath, vaultPath string) string {
	rel, err := filepath.Rel(vaultPath, filePath)
	if err != nil {
		return filePath
	}
	return filepath.ToSlash(rel)
}
