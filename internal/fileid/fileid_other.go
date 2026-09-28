//go:build !unix

package fileid

import "io/fs"

// fromSys has no inode or link count to offer on this platform. Nlink 1
// means "not known to be hard-linked"; replacing files is only supported on
// Unix systems.
func fromSys(fi fs.FileInfo) Info {
	return Info{Nlink: 1, MtimeNs: fi.ModTime().UnixNano()}
}
