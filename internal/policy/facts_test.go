package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/testutil"
)

// fileConditions covers every condition that reads the probed file, on both
// sides of the values the fixtures have.
func fileConditions() []Condition {
	return []Condition{
		{Field: FieldResolution, Op: OpAbove, Text: "1080p"},
		{Field: FieldResolution, Op: OpAtMost, Text: "1080p"},
		{Field: FieldResolution, Op: OpIs, Text: "2160p"},
		{Field: FieldCodec, Op: OpIs, List: []string{"h264"}},
		{Field: FieldCodec, Op: OpIsNot, List: []string{"hevc", "av1"}},
		{Field: FieldBitrate, Op: OpAbove, Number: 8},
		{Field: FieldBitrate, Op: OpBelow, Number: 0.5},
		{Field: FieldSize, Op: OpAbove, Number: 0.001},
		{Field: FieldSize, Op: OpBelow, Number: 20},
		{Field: FieldHDR, Op: OpIs, List: []string{"sdr"}},
		{Field: FieldHDR, Op: OpIsNot, List: []string{"hdr10", "dv-hdr10"}},
	}
}

// TestFactsGiveTheSameExplanationAsTheFile checks that an item evaluated
// from its facts alone explains itself byte for byte as it does from the
// parsed file, for every committed probe fixture.
func TestFactsGiveTheSameExplanationAsTheFile(t *testing.T) {
	dir := filepath.Join(testutil.RepoRoot(t), "internal", "media", "testdata", "probe")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{ID: 1, Name: "every file condition", Enabled: true,
		Conditions: Conditions{Version: 1, All: fileConditions()}}
	checked := 0
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || strings.HasSuffix(name, ".frames") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			probe, frames := testutil.ProbeJSON(t, name)
			f, err := media.Parse(probe, frames, 0)
			if err != nil {
				t.Skipf("fixture does not parse: %v", err)
			}
			byFile := baseItem()
			byFile.File = f
			byFacts := baseItem()
			byFacts.File, byFacts.Facts = nil, FactsFromFile(f)
			want, got := Check(p, byFile, now), Check(p, byFacts, now)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("from facts:\n%+v\nfrom file:\n%+v", got.Lines, want.Lines)
			}
			if !reflect.DeepEqual(Evaluate([]Policy{p}, byFile, now), Evaluate([]Policy{p}, byFacts, now)) {
				t.Fatal("Evaluate differs between facts and file")
			}
		})
		checked++
	}
	if checked < 10 {
		t.Fatalf("only %d fixtures checked", checked)
	}
}

func TestFactsFromFile(t *testing.T) {
	cases := []struct {
		name string
		file *media.File
		want Facts
	}{
		{"nil file is not probed", nil, Facts{}},
		{"stream bitrate", file(media.CodecH264, 1920, 800, 6_000_000), Facts{Probed: true, Codec: media.CodecH264,
			Width: 1920, Height: 800, VideoBitrate: 6_000_000, BitrateKnown: true, HDR: media.SDR, Size: 50_000_000_000}},
		{"no video stream", &media.File{Size: 10}, Facts{Probed: true, HDR: media.Unclear, Size: 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FactsFromFile(tc.file); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestFactsWinOverFile: callers that supply facts are trusted; File is then
// only carried along for plan.Decide.
func TestFactsWinOverFile(t *testing.T) {
	it := baseItem() // a 2160p H.264 file
	it.Facts = Facts{Probed: true, Codec: media.CodecHEVC, Width: 1920, Height: 1080, HDR: media.SDR}
	l := checkCondition(Condition{Field: FieldCodec, Op: OpIs, List: []string{"hevc"}}, it, now)
	if !l.Pass || l.Text != "codec is HEVC" {
		t.Fatalf("got %+v", l)
	}
}

func TestUnprobedFactsExplainNotInspected(t *testing.T) {
	it := baseItem()
	it.File, it.Size = nil, 0
	want := []string{
		"resolution unknown (not inspected yet)", "resolution unknown (not inspected yet)",
		"resolution unknown (not inspected yet)", "codec unknown (not inspected yet)",
		"codec unknown (not inspected yet)", "bitrate unknown (not inspected yet)",
		"bitrate unknown (not inspected yet)", "size unknown", "size unknown",
		"dynamic range unknown (not inspected yet)", "dynamic range unknown (not inspected yet)",
	}
	for i, c := range fileConditions() {
		if l := checkCondition(c, it, now); l.Pass || l.Text != want[i] {
			t.Errorf("%s %s: got %+v, want %q", c.Field, c.Op, l, want[i])
		}
	}
}
