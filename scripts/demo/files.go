package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The demo only ever writes inside its own folder, which it marks so that
// -reset can never delete a folder the demo did not make. Media files are
// sparse: truncated to their size, with no data written, so a 60 GB film
// takes no disk space.

const markerName = ".jellytrim-demo"

// demoFolder is the resolved demo folder.
type demoFolder struct {
	root string
}

func (d demoFolder) config() string { return filepath.Join(d.root, "config") }
func (d demoFolder) media() string  { return filepath.Join(d.root, "media") }

// openFolder creates or checks the demo folder. A folder that exists must
// be empty or carry the demo's marker. With reset, the config and media
// folders are deleted first.
func openFolder(dir string, reset bool) (demoFolder, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return demoFolder{}, err
	}
	entries, err := os.ReadDir(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(abs, 0o750); err != nil {
			return demoFolder{}, err
		}
	case err != nil:
		return demoFolder{}, err
	case len(entries) > 0:
		if _, err := os.Stat(filepath.Join(abs, markerName)); err != nil {
			return demoFolder{}, fmt.Errorf("%s is not empty and was not made by the demo; choose another -dir", abs)
		}
	}
	if err := os.WriteFile(filepath.Join(abs, markerName), []byte("Made by go run ./scripts/demo. Safe to delete.\n"), 0o600); err != nil {
		return demoFolder{}, err
	}
	// Not resolved through symbolic links, so pages show the path as given
	// (JellyTrim resolves its roots itself).
	d := demoFolder{root: abs}
	if reset {
		for _, sub := range []string{d.config(), d.media()} {
			if err := os.RemoveAll(sub); err != nil {
				return d, fmt.Errorf("resetting the demo: %w", err)
			}
		}
	}
	return d, nil
}

// inside refuses any path outside the demo folder.
func (d demoFolder) inside(path string) error {
	rel, err := filepath.Rel(d.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to touch %s: it is outside the demo folder", path)
	}
	return nil
}

// sparseFile makes a file of size bytes with no data, unless it exists.
func (d demoFolder) sparseFile(path string, size int64, mtime time.Time) error {
	if err := d.inside(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- inside the demo folder
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, mtime, mtime)
}

// resize changes a demo file's size, as replacing it with a smaller
// encode would, without writing any data.
func (d demoFolder) resize(path string, size int64, mtime time.Time) error {
	if err := d.inside(path); err != nil {
		return err
	}
	if err := os.Truncate(path, size); err != nil {
		return err
	}
	return os.Chtimes(path, mtime, mtime)
}

// hardLink gives a demo file a second name in the demo's downloads folder,
// like a torrent client's seeding copy.
func (d demoFolder) hardLink(path string) error {
	link := filepath.Join(d.media(), "downloads", filepath.Base(path))
	if err := d.inside(path); err != nil {
		return err
	}
	if err := d.inside(link); err != nil {
		return err
	}
	if _, err := os.Lstat(link); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		return err
	}
	return os.Link(path, link)
}

// fileSize is a file's size now, or 0.
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
