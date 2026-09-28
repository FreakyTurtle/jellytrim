//go:build unix

package queue

import (
	"errors"
	"os"
	"syscall"
)

// deviceOf returns the ID of the filesystem dir is on.
func deviceOf(dir string) (uint64, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no device information for " + dir)
	}
	return uint64(st.Dev), nil //nolint:unconvert // Dev is int32 on some platforms
}
