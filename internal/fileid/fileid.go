// Package fileid reads the facts JellyTrim uses to recognise a file and
// decide whether it is safe to replace: device, inode, size, modification
// time, link count, owner and whether the path is a symbolic link.
package fileid

import (
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Info describes a file on disk.
type Info struct {
	Dev       uint64
	Inode     uint64
	Size      int64
	MtimeNs   int64
	Nlink     int
	Mode      fs.FileMode
	UID       int
	GID       int
	IsSymlink bool
	ModTime   time.Time
}

// Same reports whether two infos describe the same unchanged file.
func (i Info) Same(o Info) bool {
	return i.Dev == o.Dev && i.Inode == o.Inode && i.Size == o.Size && i.MtimeNs == o.MtimeNs
}

// Stat describes path. IsSymlink reports whether path itself is a link; the
// other fields describe the file it resolves to.
func Stat(path string) (Info, error) {
	lfi, err := os.Lstat(path)
	if err != nil {
		return Info{}, err
	}
	fi := lfi
	isLink := lfi.Mode()&fs.ModeSymlink != 0
	if isLink {
		if fi, err = os.Stat(path); err != nil {
			return Info{}, fmt.Errorf("following symbolic link: %w", err)
		}
	}
	if !fi.Mode().IsRegular() {
		return Info{}, fmt.Errorf("%s is not a regular file", path)
	}
	info := fromSys(fi)
	info.Size = fi.Size()
	info.Mode = fi.Mode()
	info.ModTime = fi.ModTime()
	if info.MtimeNs == 0 {
		info.MtimeNs = fi.ModTime().UnixNano()
	}
	info.IsSymlink = isLink
	return info, nil
}
