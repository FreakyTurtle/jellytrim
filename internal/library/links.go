package library

import (
	"os"
	"path/filepath"
	"sync"
)

// linkCache resolves symbolic links in file paths for one evaluation run,
// remembering each folder it resolved. filepath.EvalSymlinks checks every
// element of a path, and a library's files share their parent folders, so
// without the cache a large library repeats the same checks many times
// over. It is only for evaluating the whole library: reassessing an item
// before a job resolves its path afresh.
type linkCache struct {
	mu   sync.Mutex
	dirs map[string]resolvedPath
}

type resolvedPath struct {
	path string
	err  error
}

func newLinkCache() *linkCache { return &linkCache{dirs: map[string]resolvedPath{}} }

// evalSymlinks gives what filepath.EvalSymlinks gives for p.
func (lc *linkCache) evalSymlinks(p string) (string, error) {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return filepath.EvalSymlinks(p)
	}
	dir := filepath.Dir(p)
	if dir == p {
		return p, nil
	}
	parent, err := lc.dir(dir)
	if err != nil {
		return "", err
	}
	return resolveLast(parent, p)
}

// dir resolves a folder, using and filling the cache.
func (lc *linkCache) dir(p string) (string, error) {
	lc.mu.Lock()
	r, ok := lc.dirs[p]
	lc.mu.Unlock()
	if ok {
		return r.path, r.err
	}
	r.path, r.err = lc.evalSymlinks(p)
	lc.mu.Lock()
	lc.dirs[p] = r
	lc.mu.Unlock()
	return r.path, r.err
}

// resolveLast resolves the last element of p, whose parent folder resolves
// to parent. Only a link as the last element needs a full resolution.
func resolveLast(parent, p string) (string, error) {
	joined := filepath.Join(parent, filepath.Base(p))
	fi, err := os.Lstat(joined)
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return filepath.EvalSymlinks(joined)
	}
	return joined, nil
}
