package testutil

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoRootHasGoMod(t *testing.T) {
	root := RepoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.Contains(string(b), "module github.com/freakyturtle/jellytrim") {
		t.Fatalf("go.mod at %s: %v", root, err)
	}
}

func TestFixturesMatchMakeFixturesScript(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(RepoRoot(t), "scripts", "make-fixtures.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for name, rel := range fixtures {
		// The script names clips by their folder and file name.
		base := filepath.Base(rel)
		if !bytes.Contains(script, []byte(strings.TrimSuffix(base, filepath.Ext(base)))) &&
			!strings.HasPrefix(name, "tv-") {
			t.Errorf("fixture %s (%s) is not made by scripts/make-fixtures.sh", name, rel)
		}
	}
}

func TestCopyFixtureCopiesIntoTempDir(t *testing.T) {
	RequireFFmpeg(t)
	src := Fixture(t, "h264-1080p")
	dst := CopyFixture(t, "h264-1080p")
	if dst == src || filepath.Base(dst) != filepath.Base(src) {
		t.Fatalf("copy %s of %s", dst, src)
	}
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(dst)
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Errorf("copy differs (%d vs %d bytes)", len(a), len(b))
	}
}

func TestProbeJSONLoadsBoth(t *testing.T) {
	probe, frames := ProbeJSON(t, "hevc-2160p-hdr10")
	if !bytes.Contains(probe, []byte(`"streams"`)) || !bytes.Contains(frames, []byte("Mastering display")) {
		t.Errorf("unexpected probe JSON")
	}
}

func TestFindToolRejectsMissingExplicitPath(t *testing.T) {
	if got := findTool(filepath.Join(t.TempDir(), "nope"), "ffmpeg"); got != "" {
		t.Errorf("findTool = %q", got)
	}
}
