package fileid

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(p, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	a, err := Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Size != 5 || a.IsSymlink || a.Nlink != 1 || a.MtimeNs == 0 {
		t.Fatalf("info %+v", a)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Link(p, filepath.Join(dir, "seed.mkv")); err != nil {
		t.Fatal(err)
	}
	b, _ := Stat(p)
	if b.Nlink != 2 || !a.Same(b) {
		t.Fatalf("hard link: %+v", b)
	}
	link := filepath.Join(dir, "link.mkv")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	c, err := Stat(link)
	if err != nil || !c.IsSymlink || c.Inode != a.Inode {
		t.Fatalf("symlink: %+v %v", c, err)
	}
	if err := os.WriteFile(p, []byte("changed!"), 0o640); err != nil {
		t.Fatal(err)
	}
	d, _ := Stat(p)
	if a.Same(d) {
		t.Fatal("a rewritten file must not be the same")
	}
	if _, err := Stat(dir); err == nil {
		t.Fatal("a directory is not a regular file")
	}
}
