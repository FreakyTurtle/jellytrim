//go:build unix

package fileid

import (
	"io/fs"
	"syscall"
)

func fromSys(fi fs.FileInfo) Info {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return Info{Nlink: 1}
	}
	return Info{
		Dev:     uint64(st.Dev), //nolint:unconvert // Dev is int32 on some platforms
		Inode:   st.Ino,
		Nlink:   int(st.Nlink),
		UID:     int(st.Uid),
		GID:     int(st.Gid),
		MtimeNs: fi.ModTime().UnixNano(),
	}
}
