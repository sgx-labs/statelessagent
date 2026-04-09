//go:build windows

package setup

func statInode(path string) (uint64, error) {
	// Windows doesn't expose inodes via os.FileInfo. Tests that depend
	// on inode comparison should skip on Windows.
	return 0, nil
}
