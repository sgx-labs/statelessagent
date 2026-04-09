//go:build !windows

package setup

import (
	"os"
	"syscall"
)

func statInode(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino, nil
	}
	return 0, nil
}
