//go:build unix

package store

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// prepareDBPath enforces Unix mode-bit security before SQLite sees the path.
// It intentionally makes no claim about Windows ACL equivalence.
func prepareDBPath(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	dirFD, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open data dir without following symlinks: %w", err)
	}
	defer unix.Close(dirFD) //nolint:errcheck

	var dirStat unix.Stat_t
	if err := unix.Fstat(dirFD, &dirStat); err != nil {
		return fmt.Errorf("inspect data dir: %w", err)
	}
	if dirStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("data dir must be a directory")
	}
	if err := unix.Fchmod(dirFD, 0o700); err != nil {
		return fmt.Errorf("secure data dir: %w", err)
	}

	fileFD, err := unix.Openat(dirFD, filepath.Base(path), unix.O_RDONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("open database file without following symlinks: %w", err)
	}
	defer unix.Close(fileFD) //nolint:errcheck

	var fileStat unix.Stat_t
	if err := unix.Fstat(fileFD, &fileStat); err != nil {
		return fmt.Errorf("inspect database file: %w", err)
	}
	if fileStat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("database file must be regular")
	}
	if err := unix.Fchmod(fileFD, 0o600); err != nil {
		return fmt.Errorf("secure database file: %w", err)
	}
	return nil
}
