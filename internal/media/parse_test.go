package media

import (
	"errors"
	"testing"
	"time"
)

func TestParseErrors(t *testing.T) {
	good := []byte(`{"format":{"filename":"file:/media/movies/A/A.mkv","format_name":"matroska,webm"},"streams":[]}`)
	tests := []struct {
		name   string
		probe  []byte
		frames []byte
		isErr  error
	}{
		{"malformed probe", []byte(`{"format":`), nil, nil},
		{"empty probe object", []byte(`{}`), nil, ErrEmptyProbe},
		{"malformed frames", good, []byte(`{"frames":[`), nil},
		{"wrong type in probe", []byte(`{"format":{"format_name":42}}`), nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Parse(tt.probe, tt.frames, 0)
			if err == nil {
				t.Fatalf("Parse() = %+v, want an error", f)
			}
			if tt.isErr != nil && !errors.Is(err, tt.isErr) {
				t.Errorf("Parse() error = %v, want %v", err, tt.isErr)
			}
		})
	}
}

func TestParseEmptyFrames(t *testing.T) {
	probe := []byte(`{"format":{"filename":"file:/media/movies/A/A.mkv","format_name":"matroska,webm"},
		"streams":[{"index":0,"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p"}]}`)
	for name, frames := range map[string][]byte{
		"nil":          nil,
		"blank":        []byte("  \n"),
		"empty object": []byte(`{}`),
		"no frames":    []byte(`{"frames":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			f, err := Parse(probe, frames, 0)
			if err != nil {
				t.Fatal(err)
			}
			if f.Video[0].Frame != nil {
				t.Errorf("Frame = %+v, want nil when the frame probe has no frame", f.Video[0].Frame)
			}
		})
	}
}

func TestParseFormat(t *testing.T) {
	probe := []byte(`{"chapters":[{"id":0},{"id":1},{"id":2}],
		"format":{"filename":"file:/media/movies/A (2020)/A (2020).mkv","format_name":"matroska,webm",
		"duration":"5400.500000","bit_rate":"8000000","size":"5400500000","tags":{"JellyTrim":"v1 job 7","title":"A"}},
		"streams":[]}`)
	f, err := Parse(probe, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "/media/movies/A (2020)/A (2020).mkv" {
		t.Errorf("Path = %q, want the file: prefix stripped", f.Path)
	}
	if f.Duration != 5400*time.Second+500*time.Millisecond {
		t.Errorf("Duration = %s", f.Duration)
	}
	if f.BitRate != 8_000_000 || f.Chapters != 3 || f.Size != 5_400_500_000 {
		t.Errorf("BitRate %d, Chapters %d, Size %d", f.BitRate, f.Chapters, f.Size)
	}
	if f.JellyTrimTag() != "v1 job 7" {
		t.Errorf("JellyTrimTag() = %q; format tags must keep their original keys", f.JellyTrimTag())
	}
	if f.Tags["JellyTrim"] != "v1 job 7" {
		t.Errorf("Tags = %v, want original keys", f.Tags)
	}

	f, err = Parse(probe, nil, 123)
	if err != nil {
		t.Fatal(err)
	}
	if f.Size != 123 {
		t.Errorf("Size = %d, want the caller's size to win over ffprobe's", f.Size)
	}
}

func TestContainerOf(t *testing.T) {
	const mov = "mov,mp4,m4a,3gp,3g2,mj2"
	tests := []struct {
		name, format, path string
		want               Container
	}{
		{"mkv", "matroska,webm", "/m/a.mkv", Matroska},
		{"mkv upper-case extension", "matroska,webm", "/m/a.MKV", Matroska},
		{"webm is not taken as Matroska", "matroska,webm", "/m/a.webm", OtherContainer},
		{"mp4", mov, "/m/a.mp4", MP4},
		{"m4v", mov, "/m/a.m4v", MP4},
		{"mov is not MP4", mov, "/m/a.mov", OtherContainer},
		{"3gp is not MP4", mov, "/m/a.3gp", OtherContainer},
		{"mp4 extension on an AVI", "avi", "/m/a.mp4", OtherContainer},
		{"avi", "avi", "/m/a.avi", OtherContainer},
		{"mpegts", "mpegts", "/m/a.ts", OtherContainer},
		{"no extension", "matroska,webm", "/m/a", OtherContainer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerOf(tt.format, tt.path); got != tt.want {
				t.Errorf("containerOf(%q, %q) = %q, want %q", tt.format, tt.path, got, tt.want)
			}
		})
	}
}

func TestPixFmtDepth(t *testing.T) {
	tests := []struct {
		pixFmt string
		want   int
	}{
		{"yuv420p", 8},
		{"yuvj420p", 8},
		{"yuv410p", 8},
		{"nv12", 8},
		{"yuv420p10le", 10},
		{"yuv420p10be", 10},
		{"yuv422p10le", 10},
		{"yuv444p12le", 12},
		{"yuva444p16le", 16},
		{"gbrp10le", 10},
		{"p010le", 10},
		{"p016le", 16},
		{"gray10le", 10},
		{"gray", 8},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.pixFmt, func(t *testing.T) {
			if got := pixFmtDepth(tt.pixFmt); got != tt.want {
				t.Errorf("pixFmtDepth(%q) = %d, want %d", tt.pixFmt, got, tt.want)
			}
		})
	}
}

func TestBitDepthPrefersRawSample(t *testing.T) {
	if got := bitDepth("10", "yuv420p"); got != 10 {
		t.Errorf("bitDepth = %d, want bits_per_raw_sample to win", got)
	}
	if got := bitDepth("", "yuv420p10le"); got != 10 {
		t.Errorf("bitDepth = %d, want pix_fmt fallback", got)
	}
	if got := bitDepth("0", "yuv420p10le"); got != 10 {
		t.Errorf("bitDepth = %d, want a zero raw sample ignored", got)
	}
}

func TestStreamBitRate(t *testing.T) {
	tests := []struct {
		name string
		br   flexString
		tags map[string]string
		want int64
	}{
		{"bit_rate", "640000", nil, 640000},
		{"bit_rate wins over BPS", "640000", map[string]string{"BPS": "1"}, 640000},
		{"BPS tag", "", map[string]string{"BPS": "1500000"}, 1500000},
		{"BPS-eng tag", "", map[string]string{"BPS-eng": "1500000"}, 1500000},
		{"plain BPS wins over a language variant", "", map[string]string{"BPS-eng": "1", "BPS": "2"}, 2},
		{"lower-case bps", "", map[string]string{"bps": "3"}, 3},
		{"junk BPS is unknown", "", map[string]string{"BPS": "N/A"}, 0},
		{"N/A bit_rate falls back to BPS", "N/A", map[string]string{"BPS": "4"}, 4},
		{"nothing is unknown", "", map[string]string{"DURATION": "00:00:01"}, 0},
		{"zero is unknown", "0", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := streamBitRate(tt.br, tt.tags); got != tt.want {
				t.Errorf("streamBitRate = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLanguage(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]string
		want string
	}{
		{"eng", map[string]string{"language": "eng"}, "eng"},
		{"upper-case key", map[string]string{"LANGUAGE": "fre"}, "fre"},
		{"und is unknown", map[string]string{"language": "und"}, ""},
		{"UND is unknown", map[string]string{"language": "UND"}, ""},
		{"empty is unknown", map[string]string{"language": ""}, ""},
		{"missing is unknown", map[string]string{"title": "English"}, ""},
		{"nil tags", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := language(tt.tags); got != tt.want {
				t.Errorf("language = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRotation(t *testing.T) {
	tests := []struct {
		name string
		side []sideData
		tags map[string]string
		want int
	}{
		{"display matrix", []sideData{{Type: "Display Matrix", Rotation: "-90"}}, nil, -90},
		{"display matrix as a decimal", []sideData{{Type: "Display Matrix", Rotation: "180.00"}}, nil, 180},
		{"rotate tag", nil, map[string]string{"rotate": "90"}, 90},
		{"display matrix wins over tag", []sideData{{Type: "Display Matrix", Rotation: "270"}}, map[string]string{"rotate": "90"}, 270},
		{"none", []sideData{{Type: "Stereo 3D"}}, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rotation(tt.side, tt.tags); got != tt.want {
				t.Errorf("rotation = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFrameRate(t *testing.T) {
	tests := []struct {
		avg, base string
		want      float64
	}{
		{"24000/1001", "24/1", 24000.0 / 1001},
		{"0/0", "25/1", 25},
		{"", "30/1", 30},
		{"0/0", "0/0", 0},
	}
	for _, tt := range tests {
		if got := frameRate(tt.avg, tt.base); got != tt.want {
			t.Errorf("frameRate(%q, %q) = %v, want %v", tt.avg, tt.base, got, tt.want)
		}
	}
}

func TestMasteringConversion(t *testing.T) {
	sd := sideData{
		Type: sideMastering, RedX: "34000/50000", RedY: "16000/50000", GreenX: "13250/50000", GreenY: "34500/50000",
		BlueX: "7500/50000", BlueY: "3000/50000", WhiteX: "15635/50000", WhiteY: "16450/50000",
		MaxLuminance: "10000000/10000", MinLuminance: "50/10000",
	}
	want := MasteringDisplay{
		RedX: 34000, RedY: 16000, GreenX: 13250, GreenY: 34500, BlueX: 7500, BlueY: 3000,
		WhiteX: 15635, WhiteY: 16450, MaxLuminance: 10000000, MinLuminance: 50,
	}
	if got := masteringFrom(sd); got == nil || *got != want {
		t.Errorf("masteringFrom = %+v, want %+v", got, want)
	}

	// Other denominators convert to the same x265 units.
	sd.RedX, sd.MaxLuminance, sd.MinLuminance = "68/100", "1000/1", "1/200"
	got := masteringFrom(sd)
	if got == nil || got.RedX != 34000 || got.MaxLuminance != 10000000 || got.MinLuminance != 50 {
		t.Errorf("masteringFrom with other denominators = %+v", got)
	}

	sd.MaxLuminance = ""
	if got := masteringFrom(sd); got != nil {
		t.Errorf("masteringFrom with no luminance = %+v, want nil", got)
	}
}

func TestContentLight(t *testing.T) {
	if got := contentLightFrom(sideData{MaxContent: "1000", MaxAverage: "400"}); got == nil || *got != (ContentLight{1000, 400}) {
		t.Errorf("contentLightFrom = %+v", got)
	}
	if got := contentLightFrom(sideData{}); got != nil {
		t.Errorf("contentLightFrom(empty) = %+v, want nil", got)
	}
}

// TestParseFrameFillsGaps checks that first-frame colour and side data fill
// what the stream lacks, and that stream values are not overwritten.
func TestParseFrameFillsGaps(t *testing.T) {

	probe := []byte(`{"format":{"filename":"file:/media/movies/A/A.mkv","format_name":"matroska,webm"},
		"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","pix_fmt":"yuv420p10le","color_primaries":"bt2020",
		"color_transfer":"unknown"}]}`)

	frames := []byte(`{"frames":[{"color_primaries":"bt709","color_transfer":"smpte2084","color_space":"bt2020nc",
		"side_data_list":[{"side_data_type":"HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"},
		{"side_data_type":"Content light level metadata","max_content":1000,"max_average":400},
		{"side_data_type":"Dolby Vision RPU Data"}]}]}`)
	f, err := Parse(probe, frames, 0)
	if err != nil {
		t.Fatal(err)
	}
	v := f.Video[0]
	if v.Transfer != "smpte2084" || v.Matrix != "bt2020nc" {
		t.Errorf("Transfer %q Matrix %q, want frame values filling unset stream values", v.Transfer, v.Matrix)
	}
	if v.Primaries != "bt2020" || v.Frame.Primaries != "bt709" {
		t.Errorf("Primaries %q (frame %q), want the stream value kept and the frame's recorded", v.Primaries, v.Frame.Primaries)
	}
	if !v.HDR10Plus || v.ContentLight == nil || v.ContentLight.MaxCLL != 1000 || !v.Frame.DolbyVisionRPU {
		t.Errorf("frame side data not applied: %+v", v)
	}
}

func TestParseStreamKinds(t *testing.T) {
	probe := []byte(`{"format":{"filename":"file:/media/movies/A/A.mkv","format_name":"matroska,webm"},
		"streams":[
		{"index":0,"codec_type":"video","codec_name":"h264","codec_tag_string":"[0][0][0][0]","profile":"High","pix_fmt":"yuv420p","width":1920,"height":1080,
		 "disposition":{"default":1,"forced":0},"tags":{"TITLE":"Main"},"side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8,"dv_level":6,"rpu_present_flag":1,"el_present_flag":0,"bl_present_flag":1,"dv_bl_signal_compatibility_id":1}]},
		{"index":1,"codec_type":"audio","codec_name":"truehd","profile":"Dolby TrueHD + Dolby Atmos","channels":8,"channel_layout":"7.1","sample_rate":"48000",
		 "disposition":{"original":1,"visual_impaired":1},"tags":{"language":"eng","BPS":"4000000"}},
		{"index":2,"codec_type":"subtitle","codec_name":"subrip","disposition":{"forced":1,"hearing_impaired":1},"tags":{"language":"und","title":"Signs"}},
		{"index":3,"codec_type":"attachment","tags":{"mimetype":"font/ttf","filename":"Font.ttf"}},
		{"index":4,"codec_type":"data","codec_tag_string":"tmcd"},
		{"index":5,"codec_type":"unknown","codec_name":"bin_data"},
		{"index":6,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}
		]}`)
	f, err := Parse(probe, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.StreamCount != 7 || len(f.Video) != 2 || len(f.Audio) != 1 || len(f.Subtitles) != 1 || len(f.Attachments) != 1 || len(f.Data) != 2 {
		t.Fatalf("stream split wrong: %+v", f)
	}
	v := f.Video[0]
	if v.CodecTag != "" || v.Title != "Main" || !v.Disposition.Default || v.DolbyVision == nil ||
		*v.DolbyVision != (DolbyVisionConfig{Profile: 8, Level: 6, RPU: true, BL: true, BLCompatibilityID: 1}) {
		t.Errorf("video = %+v, dv = %+v", v, v.DolbyVision)
	}
	a := f.Audio[0]
	if a.SampleRate != 48000 || a.BitRate != 4_000_000 || a.Language != "eng" || !a.Disposition.Original || !a.Disposition.VisualImpaired || a.Profile == "" {
		t.Errorf("audio = %+v", a)
	}
	s := f.Subtitles[0]
	if s.Language != "" || s.Title != "Signs" || !s.Disposition.Forced || !s.Disposition.HearingImpaired {
		t.Errorf("subtitle = %+v", s)
	}
	if at := f.Attachments[0]; at.Codec != "" || at.MimeType != "font/ttf" || at.FileName != "Font.ttf" {
		t.Errorf("attachment = %+v", at)
	}
	if d := f.Data[0]; d.CodecTag != "tmcd" || d.Index != 4 {
		t.Errorf("data = %+v", d)
	}
	if d := f.Data[1]; d.Codec != "bin_data" || d.Index != 5 {
		t.Errorf("unknown stream kind not kept as data: %+v", d)
	}
	if main, ok := f.MainVideo(); !ok || main.Index != 0 || f.RealVideoCount() != 1 || len(f.CoverArt()) != 1 {
		t.Errorf("main video / cover art wrong")
	}
}

func TestFlexString(t *testing.T) {
	tests := []struct {
		in      flexString
		wantInt int64
		wantOK  bool
	}{
		{"42", 42, true},
		{"-90", -90, true},
		{"-90.4", -90, true},
		{"N/A", 0, false},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range tests {
		n, ok := tt.in.Int64()
		if n != tt.wantInt || ok != tt.wantOK {
			t.Errorf("flexString(%q).Int64() = %d, %t; want %d, %t", tt.in, n, ok, tt.wantInt, tt.wantOK)
		}
	}
	if _, ok := parseRational("1/0"); ok {
		t.Error("parseRational(1/0) should fail")
	}
}
