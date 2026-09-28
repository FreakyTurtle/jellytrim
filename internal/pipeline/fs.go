package pipeline

import (
	"io/fs"
	"os"

	"github.com/freakyturtle/jellytrim/internal/fileid"
)

// FS is every filesystem operation the pipeline performs. Production uses
// OSFS; the safety tests inject a failure into each operation in turn.
type FS interface {
	Stat(path string) (fileid.Info, error)
	Exists(path string) bool
	Link(oldname, newname string) error
	Rename(oldname, newname string) error
	Remove(name string) error
	Chmod(name string, mode fs.FileMode) error
	Chown(name string, uid, gid int) error
	SyncFile(name string) error
	SyncDir(dir string) error
	Free(dir string) (uint64, error)
}

// OSFS is the real filesystem.
type OSFS struct{}

// Stat describes a file; see fileid.Stat.
func (OSFS) Stat(path string) (fileid.Info, error) { return fileid.Stat(path) }

// Exists reports whether anything exists at path (without following links).
func (OSFS) Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Link creates a hard link.
func (OSFS) Link(oldname, newname string) error { return os.Link(oldname, newname) }

// Rename renames atomically within a filesystem.
func (OSFS) Rename(oldname, newname string) error { return os.Rename(oldname, newname) }

// Remove deletes one file.
func (OSFS) Remove(name string) error { return os.Remove(name) }

// Chmod sets permission bits.
func (OSFS) Chmod(name string, mode fs.FileMode) error { return os.Chmod(name, mode) }

// Chown sets the owner and group.
func (OSFS) Chown(name string, uid, gid int) error { return os.Chown(name, uid, gid) }

// SyncFile flushes a file's data to disk.
func (OSFS) SyncFile(name string) error {
	f, err := os.Open(name) // #nosec G304 -- a path the pipeline created
	if err != nil {
		return err
	}
	serr := f.Sync()
	cerr := f.Close()
	if serr != nil {
		return serr
	}
	return cerr
}

// SyncDir flushes a directory entry so a rename survives a power cut.
func (OSFS) SyncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- the source file's directory
	if err != nil {
		return err
	}
	serr := d.Sync()
	cerr := d.Close()
	// Some filesystems (and macOS for directories) reject fsync on a
	// directory; the rename itself is still atomic.
	if serr != nil && !isUnsupported(serr) {
		return serr
	}
	return cerr
}

// Free returns the bytes available to this user in dir's filesystem.
func (OSFS) Free(dir string) (uint64, error) { return freeSpace(dir) }
