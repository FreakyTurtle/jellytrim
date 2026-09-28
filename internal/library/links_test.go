package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkCacheResolvesLikeEvalSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "real", "dir")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "file.mkv"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(outside, "x.mkv"), nil, 0o644))
	for link, target := range map[string]string{
		"linkdir":                "real/dir",
		"l2":                     "linkdir",
		"out":                    outside,
		"real/dir/linkfile.mkv":  "file.mkv",
		"real/dir/relative.mkv":  "../dir/file.mkv",
		"real/dir/dangling.mkv":  "nothing.mkv",
		"real/dir/loop.mkv":      "loop.mkv",
		"real/dir/outfile.mkv":   filepath.Join(outside, "x.mkv"),
		"real/dir/up":            "..",
		"real/dir/linkedup.mkv":  "up/dir/file.mkv",
		"real/dir/file-link-dir": "../../linkdir",
	} {
		must(t, os.Symlink(target, filepath.Join(root, link)))
	}
	paths := []string{
		filepath.Join(dir, "file.mkv"),
		filepath.Join(root, "linkdir", "file.mkv"),
		filepath.Join(root, "l2", "file.mkv"),
		filepath.Join(root, "out", "x.mkv"),
		filepath.Join(dir, "linkfile.mkv"),
		filepath.Join(dir, "relative.mkv"),
		filepath.Join(dir, "dangling.mkv"),
		filepath.Join(dir, "loop.mkv"),
		filepath.Join(dir, "outfile.mkv"),
		filepath.Join(dir, "linkedup.mkv"),
		filepath.Join(dir, "up", "dir", "file.mkv"),
		filepath.Join(dir, "file-link-dir", "file.mkv"),
		filepath.Join(dir, "missing.mkv"),
		filepath.Join(root, "nope", "file.mkv"),
		root + "//real/./dir/file.mkv",
		dir,
		"/",
		"relative/file.mkv",
	}
	lc := newLinkCache()
	for pass := range 2 { // the second pass answers from the cache
		for _, p := range paths {
			want, wantErr := filepath.EvalSymlinks(p)
			got, err := lc.evalSymlinks(p)
			if got != want || (err == nil) != (wantErr == nil) {
				t.Errorf("pass %d, %s: got %q (%v), want %q (%v)", pass, p, got, err, want, wantErr)
			}
		}
	}
}
