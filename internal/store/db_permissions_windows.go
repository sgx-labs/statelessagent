//go:build windows

package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// prepareDBPath preserves ordinary Windows path creation behavior. The 0700
// and 0600 values are Unix mode-bit policy, not a Windows ACL guarantee.
func prepareDBPath(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close database file: %w", err)
	}
	return nil
}
