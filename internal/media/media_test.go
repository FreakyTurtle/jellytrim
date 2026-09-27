package media

import "testing"

func TestClassOf(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		want          Resolution
	}{
		{"zero width is unknown", 0, 1080, 0},
		{"zero height is unknown", 1920, 0, 0},
		{"negative is unknown", -1, -1, 0},
		{"8K", 7680, 4320, Res4320},
		{"width 6400 is 4320p", 6400, 100, Res4320},
		{"height 3600 is 4320p", 100, 3600, Res4320},
		{"just under 4320p by width and height", 6399, 3599, Res2160},
		{"UHD", 3840, 2160, Res2160},
		{"scope UHD counts by width", 3840, 1600, Res2160},
		{"width 3200 is 2160p", 3200, 10, Res2160},
		{"height 1800 is 2160p", 10, 1800, Res2160},
		{"just under 2160p", 3199, 1799, Res1440},
		{"1440p", 2560, 1440, Res1440},
		{"width 2240 is 1440p", 2240, 10, Res1440},
		{"height 1260 is 1440p", 10, 1260, Res1440},
		{"just under 1440p", 2239, 1259, Res1080},
		{"1080p", 1920, 1080, Res1080},
		{"scope 1080p counts by width", 1920, 800, Res1080},
		{"width 1600 is 1080p", 1600, 10, Res1080},
		{"height 900 is 1080p", 10, 900, Res1080},
		{"just under 1080p", 1599, 899, Res720},
		{"720p", 1280, 720, Res720},
		{"width 1120 is 720p", 1120, 10, Res720},
		{"height 630 is 720p", 10, 630, Res720},
		{"just under 720p", 1119, 629, Res576},
		{"PAL DVD", 720, 576, Res576},
		{"width 900 is 576p", 900, 10, Res576},
		{"height 500 is 576p", 10, 500, Res576},
		{"just under 576p", 899, 499, Res480},
		{"NTSC DVD", 720, 480, Res480},
		{"tiny", 1, 1, Res480},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassOf(tt.width, tt.height); got != tt.want {
				t.Errorf("ClassOf(%d, %d) = %d, want %d", tt.width, tt.height, got, tt.want)
			}
		})
	}
}

func TestCodecEfficiency(t *testing.T) {
	tests := []struct {
		codec Codec
		want  int
	}{
		{CodecAV1, 3},
		{CodecHEVC, 2},
		{CodecVP9, 2},
		{CodecH264, 1},
		{CodecMPEG2, 0},
		{CodecVC1, 0},
		{CodecMPEG4, 0},
		{"", 0},
		{"prores", 0},
	}
	for _, tt := range tests {
		t.Run(string(tt.codec), func(t *testing.T) {
			if got := tt.codec.Efficiency(); got != tt.want {
				t.Errorf("%q.Efficiency() = %d, want %d", tt.codec, got, tt.want)
			}
		})
	}
	if CodecAV1.Efficiency() <= CodecHEVC.Efficiency() || CodecHEVC.Efficiency() <= CodecH264.Efficiency() {
		t.Error("efficiency order must be AV1 > HEVC > H.264")
	}
}

func TestInterlaced(t *testing.T) {
	tests := []struct {
		fieldOrder string
		want       bool
	}{
		{"", false},
		{"progressive", false},
		{"unknown", false},
		{"tt", true},
		{"bb", true},
		{"tb", true},
		{"bt", true},
		{"something new", true},
	}
	for _, tt := range tests {
		t.Run("field order "+tt.fieldOrder, func(t *testing.T) {
			if got := (VideoStream{FieldOrder: tt.fieldOrder}).Interlaced(); got != tt.want {
				t.Errorf("Interlaced() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCommentary(t *testing.T) {
	tests := []struct {
		name  string
		title string
		comm  bool
		want  bool
	}{
		{"comment disposition", "", true, true},
		{"title says commentary", "Director's Commentary", false, true},
		{"title in upper case", "COMMENTARY TRACK", false, true},
		{"ordinary title", "Surround 5.1", false, false},
		{"no title", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := AudioStream{Title: tt.title, Disposition: Disposition{Comment: tt.comm}}
			if got := a.Commentary(); got != tt.want {
				t.Errorf("Commentary() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestBitmap(t *testing.T) {
	tests := []struct {
		codec string
		want  bool
	}{
		{"hdmv_pgs_subtitle", true},
		{"dvd_subtitle", true},
		{"dvb_subtitle", true},
		{"xsub", true},
		{"subrip", false},
		{"ass", false},
		{"mov_text", false},
		{"webvtt", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.codec, func(t *testing.T) {
			if got := (SubtitleStream{Codec: tt.codec}).Bitmap(); got != tt.want {
				t.Errorf("Bitmap() = %t, want %t", got, tt.want)
			}
		})
	}
}
