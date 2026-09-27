package media

import (
	"strings"
	"testing"
)

// hdrStream is a baseline 10-bit HEVC stream with a first-frame probe that
// agrees with it; each case changes what it needs.
func hdrStream(transfer, primaries string, mods ...func(*VideoStream)) VideoStream {
	v := VideoStream{
		Index: 0, Codec: CodecHEVC, BitDepth: 10, PixFmt: "yuv420p10le",
		Transfer: transfer, Primaries: primaries,
		Frame: &FrameInfo{Transfer: transfer, Primaries: primaries},
	}
	for _, m := range mods {
		m(&v)
	}
	return v
}

func withDV(profile, compat int) func(*VideoStream) {
	return func(v *VideoStream) {
		v.DolbyVision = &DolbyVisionConfig{Profile: profile, Level: 6, RPU: true, BL: true, BLCompatibilityID: compat}
	}
}

func withDepth(bits int) func(*VideoStream) { return func(v *VideoStream) { v.BitDepth = bits } }

func withMatrix(m string) func(*VideoStream) { return func(v *VideoStream) { v.Matrix = m } }

func withTag(tag string) func(*VideoStream) { return func(v *VideoStream) { v.CodecTag = tag } }

func noFrame(v *VideoStream) { v.Frame = nil }

func withHDR10Plus(v *VideoStream) { v.HDR10Plus = true }

func withMastering(v *VideoStream) {
	v.Mastering = &MasteringDisplay{RedX: 34000, RedY: 16000, MaxLuminance: 10000000, MinLuminance: 50}
}

func withFrameColour(transfer, primaries string) func(*VideoStream) {
	return func(v *VideoStream) { v.Frame = &FrameInfo{Transfer: transfer, Primaries: primaries} }
}

func TestHDRClassification(t *testing.T) {
	tests := []struct {
		name     string
		video    VideoStream
		want     HDRClass
		wantFact string // a substring one of the facts must contain
	}{
		// Dolby Vision.
		{"DV profile 5 has no usable base", hdrStream("", "", withDV(5, 0)), DolbyVision, "profile 5 has no HDR10 base layer"},
		{"DV profile 5 stays DV even with PQ tags", hdrStream(transferPQ, primaries2020, withDV(5, 0)), DolbyVision, "profile 5"},
		{"DV profile 7 has an HDR10 base", hdrStream(transferPQ, primaries2020, withDV(7, 6)), DolbyVisionHDR10, "base layer is HDR10"},
		{"DV profile 8.1 has an HDR10 base", hdrStream(transferPQ, primaries2020, withDV(8, 1)), DolbyVisionHDR10, "compatibility ID 1"},
		{"DV profile 8.4 HLG base is skipped", hdrStream(transferHLG, primaries2020, withDV(8, 4)), DolbyVision, "HLG base layer"},
		{"DV profile 8.2 SDR base is skipped", hdrStream("bt709", "bt709", withDV(8, 2)), DolbyVision, "SDR base layer"},
		{"DV profile 8.0 is skipped", hdrStream(transferPQ, primaries2020, withDV(8, 0)), DolbyVision, "compatibility ID 0"},
		{"DV profile 8 with an unknown compatibility ID", hdrStream(transferPQ, primaries2020, withDV(8, 9)), DolbyVision, "not recognised"},
		{"DV profile 4 is not supported", hdrStream(transferPQ, primaries2020, withDV(4, 2)), DolbyVision, "profile 4 is not supported"},
		{"DV profile 10 (AV1) is not supported", hdrStream(transferPQ, primaries2020, withDV(10, 1)), DolbyVision, "profile 10"},
		{"DV profile 8.1 without PQ is unclear", hdrStream("", "", withDV(8, 1)), Unclear, "should be PQ"},
		{"DV profile 7 with bt709 primaries is unclear", hdrStream(transferPQ, "bt709", withDV(7, 6)), Unclear, "should be PQ with BT.2020"},
		{"DV profile 8.1 without a base layer is unclear", hdrStream(transferPQ, primaries2020, withDV(8, 1), func(v *VideoStream) { v.DolbyVision.BL = false }), Unclear, "no base layer"},
		{"DV profile 8.1 with stream and frame disagreeing is unclear", hdrStream(transferPQ, primaries2020, withDV(8, 1), withFrameColour(transferHLG, primaries2020)), Unclear, "first frame says arib-std-b67"},
		{"DV takes precedence over HDR10+", hdrStream(transferPQ, primaries2020, withDV(8, 1), withHDR10Plus), DolbyVisionHDR10, ""},
		{"dvh1 tag without a DV record is unclear", hdrStream(transferPQ, primaries2020, withTag("dvh1")), Unclear, "codec tag dvh1"},
		{"dvhe tag without a DV record is unclear", hdrStream("", "", withTag("dvhe")), Unclear, "codec tag dvhe"},
		{"dav1 tag without a DV record is unclear", hdrStream("", "", withTag("dav1")), Unclear, "codec tag dav1"},
		{"dva1 tag in upper case is unclear", hdrStream("", "", withTag("DVA1")), Unclear, "codec tag dva1"},
		{"hvc1 tag is not Dolby Vision", hdrStream(transferPQ, primaries2020, withTag("hvc1")), HDR10, ""},
		{"DV RPU in the frame without a record is unclear", hdrStream(transferPQ, primaries2020, func(v *VideoStream) { v.Frame.DolbyVisionRPU = true }), Unclear, "Dolby Vision metadata"},

		// HDR10 and HDR10+.
		{"PQ with BT.2020 is HDR10", hdrStream(transferPQ, primaries2020, withMastering), HDR10, "transfer is PQ (smpte2084)"},
		{"HDR10 without static metadata is still HDR10", hdrStream(transferPQ, primaries2020), HDR10, "no static HDR metadata"},
		{"PQ with BT.709 primaries is unclear", hdrStream(transferPQ, "bt709"), Unclear, "should have BT.2020 primaries"},
		{"PQ with no primaries is unclear", hdrStream(transferPQ, ""), Unclear, "no colour primaries"},
		{"PQ on 8-bit video is unclear", hdrStream(transferPQ, primaries2020, withDepth(8)), Unclear, "PQ transfer on 8-bit video"},
		{"PQ without a frame probe is unclear", hdrStream(transferPQ, primaries2020, noFrame), Unclear, "HDR10+ cannot be ruled out"},
		{"HDR10+ side data with PQ is HDR10+", hdrStream(transferPQ, primaries2020, withHDR10Plus), HDR10Plus, "SMPTE 2094-40"},
		{"HDR10+ side data without PQ is unclear", hdrStream("bt709", "bt709", withHDR10Plus), Unclear, "without a PQ transfer"},
		{"HDR10+ with HLG is unclear", hdrStream(transferHLG, primaries2020, withHDR10Plus), Unclear, "without a PQ transfer"},

		// HLG.
		{"HLG", hdrStream(transferHLG, primaries2020), HLG, "transfer is HLG (arib-std-b67)"},
		{"HLG without a frame probe is still HLG", hdrStream(transferHLG, primaries2020, noFrame), HLG, ""},

		// SDR.
		{"BT.709 is SDR", hdrStream("bt709", "bt709"), SDR, "transfer is BT.709 (bt709)"},
		{"BT.601 NTSC is SDR", hdrStream("smpte170m", "smpte170m", withDepth(8)), SDR, "BT.601"},
		{"BT.470 BG is SDR", hdrStream("bt470bg", "bt470bg", withDepth(8)), SDR, ""},
		{"gamma28 (ffprobe's BT.470 BG) is SDR", hdrStream("gamma28", "bt470bg", withDepth(8)), SDR, ""},
		{"BT.470 M is SDR", hdrStream("bt470m", "bt470m", withDepth(8)), SDR, ""},
		{"gamma22 (ffprobe's BT.470 M) is SDR", hdrStream("gamma22", "", withDepth(8)), SDR, ""},
		{"BT.601 name is SDR", hdrStream("bt601", "", withDepth(8)), SDR, ""},
		{"sRGB is SDR", hdrStream("iec61966-2-1", "bt709", withDepth(8)), SDR, "sRGB"},
		{"SDR transfer with no primaries is SDR", hdrStream("bt709", ""), SDR, ""},
		{"8-bit with nothing set is SDR", hdrStream("", "", withDepth(8)), SDR, "8-bit video with no transfer characteristics is treated as SDR"},
		{"10-bit with nothing set and no HDR evidence is SDR", hdrStream("", ""), SDR, "10-bit video without colour tags or HDR metadata; treated as SDR"},
		{"12-bit with nothing set is SDR", hdrStream("", "", withDepth(12)), SDR, "12-bit video without colour tags"},
		{"10-bit with unset transfer and BT.709 primaries is SDR", hdrStream("", "bt709"), SDR, "primaries are BT.709"},
		{"10-bit with unset transfer and BT.601 primaries is SDR", hdrStream("", "smpte170m"), SDR, ""},
		{"SDR stream with an agreeing frame and no frame colour", hdrStream("bt709", "bt709", withFrameColour("", "")), SDR, ""},

		// Unclear.
		{"BT.2020 primaries with an SDR transfer", hdrStream("bt709", primaries2020), Unclear, "BT.2020 primaries with an SDR transfer"},
		{"BT.2020 primaries with the bt2020-10 SDR transfer", hdrStream("bt2020-10", primaries2020), Unclear, "SDR transfer"},
		{"BT.2020 primaries with unset transfer", hdrStream("", primaries2020), Unclear, "BT.2020 primaries with no transfer"},
		{"BT.2020 non-constant matrix with unset transfer", hdrStream("", "", withMatrix("bt2020nc")), Unclear, "BT.2020 matrix (bt2020nc)"},
		{"BT.2020 constant matrix with unset transfer on 8-bit", hdrStream("", "", withMatrix("bt2020c"), withDepth(8)), Unclear, "BT.2020 matrix (bt2020c)"},
		{"mastering metadata with unset transfer", hdrStream("", "", withMastering), Unclear, "HDR static metadata without a PQ or HLG transfer"},
		{"content light with an SDR transfer", hdrStream("bt709", "bt709", func(v *VideoStream) { v.ContentLight = &ContentLight{MaxCLL: 1000} }), Unclear, "HDR static metadata"},
		{"unknown bit depth with unset transfer", hdrStream("", "", withDepth(0)), Unclear, "bit depth unknown"},
		{"unset transfer with DCI-P3 primaries", hdrStream("", "smpte432", withDepth(8)), Unclear, "primaries are smpte432"},
		{"unrecognised transfer", hdrStream("linear", "bt709"), Unclear, "transfer linear is not one JellyTrim recognises"},
		{"stream says SDR, frame says PQ", hdrStream("bt709", "bt709", withFrameColour(transferPQ, primaries2020)), Unclear, "the first frame says smpte2084"},
		{"stream and frame primaries disagree", hdrStream(transferPQ, primaries2020, withFrameColour(transferPQ, "bt709")), Unclear, "primaries bt2020 but the first frame says bt709"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{Video: []VideoStream{tt.video}}
			got := f.HDR()
			if got.Class != tt.want {
				t.Fatalf("HDR().Class = %q, want %q (facts %q)", got.Class, tt.want, got.Facts)
			}
			if len(got.Facts) == 0 {
				t.Fatal("HDR() gave no facts; every classification must explain itself")
			}
			if tt.wantFact != "" && !containsFact(got.Facts, tt.wantFact) {
				t.Errorf("no fact contains %q; facts %q", tt.wantFact, got.Facts)
			}
		})
	}
}

func TestHDRNoVideo(t *testing.T) {
	for name, f := range map[string]*File{
		"no streams":      {},
		"cover art only":  {Video: []VideoStream{{Disposition: Disposition{AttachedPic: true}, Transfer: "bt709"}}},
		"audio only file": {Audio: []AudioStream{{Codec: "flac"}}},
	} {
		t.Run(name, func(t *testing.T) {
			got := f.HDR()
			if got.Class != Unclear || !containsFact(got.Facts, "no video stream") {
				t.Errorf("HDR() = %+v, want unclear with fact \"no video stream\"", got)
			}
		})
	}
}

// TestHDRUsesMainVideo checks that cover art in front of the main video is
// ignored, and that a main video without a frame probe (only v:0 is probed)
// is not assumed to be free of HDR10+.
func TestHDRUsesMainVideo(t *testing.T) {
	cover := VideoStream{Index: 0, Codec: "mjpeg", BitDepth: 8, Disposition: Disposition{AttachedPic: true}}
	main := hdrStream(transferPQ, primaries2020, noFrame)
	main.Index = 1
	f := &File{Video: []VideoStream{cover, main}}
	if got := f.HDR(); got.Class != Unclear {
		t.Errorf("HDR().Class = %q, want unclear when the main video had no frame probe", got.Class)
	}
}

func TestHDRClassLabelAndIsHDR(t *testing.T) {
	tests := []struct {
		class HDRClass
		label string
		isHDR bool
	}{
		{SDR, "SDR", false},
		{HDR10, "HDR10", true},
		{HLG, "HLG", true},
		{HDR10Plus, "HDR10+", true},
		{DolbyVisionHDR10, "Dolby Vision (HDR10 base)", true},
		{DolbyVision, "Dolby Vision", true},
		{Unclear, "Unclear HDR", true},
		{"", "Unknown", false},
	}
	for _, tt := range tests {
		if got := tt.class.Label(); got != tt.label {
			t.Errorf("%q.Label() = %q, want %q", tt.class, got, tt.label)
		}
		if got := tt.class.IsHDR(); got != tt.isHDR {
			t.Errorf("%q.IsHDR() = %t, want %t", tt.class, got, tt.isHDR)
		}
	}
}

func containsFact(facts []string, sub string) bool {
	for _, f := range facts {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}
