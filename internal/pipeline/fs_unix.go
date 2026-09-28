//go:build unix

package pipeline

import (
	"errors"
	"syscall"
)

func freeSpace(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil //nolint:unconvert // Bsize differs in type across platforms
}

func isUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EBADF)
}

func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }

func isBusy(err error) bool { return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.ETXTBSY) }
