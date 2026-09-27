package media

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/golden")

// TestParseGolden parses every fixture pair in testdata/probe and compares a
// stable text summary with testdata/golden/<name>.golden.
func TestParseGolden(t *testing.T) {
	probes, err := filepath.Glob(filepath.Join("testdata", "probe", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range probes {
		if strings.HasSuffix(p, ".frames.json") {
			continue
		}
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".json"))
	}
	if len(names) == 0 {
		t.Fatal("no fixtures in testdata/probe")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			probe := readFile(t, filepath.Join("testdata", "probe", name+".json"))
			frames := readFile(t, filepath.Join("testdata", "probe", name+".frames.json"))
			f, err := Parse(probe, frames, 0)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := summary(f)
			golden := filepath.Join("testdata", "golden", name+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want := string(readFile(t, golden))
			if got != want {
				t.Errorf("summary differs from %s (run go test ./internal/media/ -update and review the diff)\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
			}
		})
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// summary renders every field of the model plus the derived facts, in a
// fixed order, so a golden diff shows exactly what a parser change moved.
func summary(f *File) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("path: %s", f.Path)
	w("container: %s (format %s)", f.Container, f.FormatName)
	w("size: %d", f.Size)
	w("duration: %s", f.Duration)
	w("bitrate: %d", f.BitRate)
	w("chapters: %d", f.Chapters)
	w("stream count: %d", f.StreamCount)
	w("tags: %s", tagsText(f.Tags))
	w("jellytrim tag: %q", f.JellyTrimTag())
	for _, v := range f.Video {
		writeVideo(w, v)
	}
	for _, a := range f.Audio {
		w("audio #%d: codec=%s profile=%q channels=%d layout=%q rate=%d bitrate=%d lang=%q title=%q disp=%s commentary=%t",
			a.Index, a.Codec, a.Profile, a.Channels, a.Layout, a.SampleRate, a.BitRate, a.Language, a.Title,
			dispText(a.Disposition), a.Commentary())
	}
	for _, s := range f.Subtitles {
		w("subtitle #%d: codec=%s lang=%q title=%q disp=%s bitmap=%t",
			s.Index, s.Codec, s.Language, s.Title, dispText(s.Disposition), s.Bitmap())
	}
	for _, a := range f.Attachments {
		w("attachment #%d: codec=%q mime=%q file=%q", a.Index, a.Codec, a.MimeType, a.FileName)
	}
	for _, d := range f.Data {
		w("data #%d: codec=%q tag=%q", d.Index, d.Codec, d.CodecTag)
	}
	w("real video streams: %d, cover art: %d", f.RealVideoCount(), len(f.CoverArt()))
	bps, known := f.VideoBitrate()
	w("video bitrate: %d known=%t source=%q (%s)", bps, known, f.VideoBitrateSource(), f.VideoBitrateSource().Label())
	if main, ok := f.MainVideo(); ok {
		w("main video: #%d resolution=%s interlaced=%t", main.Index, main.Resolution().Label(), main.Interlaced())
	} else {
		w("main video: none")
	}
	hdr := f.HDR()
	w("hdr: %s (%s)", hdr.Class, hdr.Class.Label())
	for _, fact := range hdr.Facts {
		w("  fact: %s", fact)
	}
	return b.String()
}

func writeVideo(w func(string, ...any), v VideoStream) {
	w("video #%d: codec=%s profile=%q tag=%q %dx%d (%s) sar=%s dar=%s fps=%.3f field=%q depth=%d pix=%s",
		v.Index, v.Codec, v.Profile, v.CodecTag, v.Width, v.Height, v.Resolution().Label(), v.SAR, v.DAR,
		v.FrameRate, v.FieldOrder, v.BitDepth, v.PixFmt)
	w("  colour: primaries=%q transfer=%q matrix=%q range=%q", v.Primaries, v.Transfer, v.Matrix, v.Range)
	w("  bitrate=%d rotation=%d lang=%q title=%q disp=%s", v.BitRate, v.Rotation, v.Language, v.Title, dispText(v.Disposition))
	w("  tags: %s", tagsText(v.Tags))
	if dv := v.DolbyVision; dv != nil {
		w("  dolby vision: profile=%d level=%d rpu=%t el=%t bl=%t compat=%d", dv.Profile, dv.Level, dv.RPU, dv.EL, dv.BL, dv.BLCompatibilityID)
	}
	if m := v.Mastering; m != nil {
		w("  mastering: G(%d,%d)B(%d,%d)R(%d,%d)WP(%d,%d)L(%d,%d)",
			m.GreenX, m.GreenY, m.BlueX, m.BlueY, m.RedX, m.RedY, m.WhiteX, m.WhiteY, m.MaxLuminance, m.MinLuminance)
	}
	if cl := v.ContentLight; cl != nil {
		w("  content light: MaxCLL=%d MaxFALL=%d", cl.MaxCLL, cl.MaxFALL)
	}
	w("  hdr10+: %t", v.HDR10Plus)
	if fr := v.Frame; fr != nil {
		w("  first frame: primaries=%q transfer=%q matrix=%q dolby vision rpu=%t", fr.Primaries, fr.Transfer, fr.Matrix, fr.DolbyVisionRPU)
	} else {
		w("  first frame: none")
	}
}

func dispText(d Disposition) string {
	var on []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"default", d.Default}, {"forced", d.Forced}, {"hearing_impaired", d.HearingImpaired},
		{"visual_impaired", d.VisualImpaired}, {"comment", d.Comment}, {"original", d.Original},
		{"attached_pic", d.AttachedPic},
	} {
		if f.set {
			on = append(on, f.name)
		}
	}
	return "[" + strings.Join(on, ",") + "]"
}

func tagsText(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, tags[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}
