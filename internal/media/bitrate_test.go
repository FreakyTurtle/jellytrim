package media

import (
	"testing"
	"time"
)

func TestVideoBitrate(t *testing.T) {
	video := func(br int64) VideoStream { return VideoStream{Index: 0, BitRate: br} }
	tests := []struct {
		name     string
		file     File
		wantBPS  int64
		wantSrc  BitrateSource
		wantKnow bool
	}{
		{
			name:    "stream bitrate wins",
			file:    File{BitRate: 9_000_000, Video: []VideoStream{video(8_000_000)}, Audio: []AudioStream{{BitRate: 640_000}}},
			wantBPS: 8_000_000, wantSrc: BitrateFromStream, wantKnow: true,
		},
		{
			name:    "container minus known audio",
			file:    File{BitRate: 10_000_000, Video: []VideoStream{video(0)}, Audio: []AudioStream{{BitRate: 640_000}, {BitRate: 192_000}}},
			wantBPS: 9_168_000, wantSrc: BitrateFromContainer, wantKnow: true,
		},
		{
			name:    "container with no audio",
			file:    File{BitRate: 5_000_000, Video: []VideoStream{video(0)}},
			wantBPS: 5_000_000, wantSrc: BitrateFromContainer, wantKnow: true,
		},
		{
			name:    "container with an unknown audio bitrate is unknown",
			file:    File{BitRate: 10_000_000, Video: []VideoStream{video(0)}, Audio: []AudioStream{{BitRate: 640_000}, {BitRate: 0}}},
			wantSrc: BitrateUnknown,
		},
		{
			name:    "container not above audio is unknown",
			file:    File{BitRate: 500_000, Video: []VideoStream{video(0)}, Audio: []AudioStream{{BitRate: 640_000}}},
			wantSrc: BitrateUnknown,
		},
		{
			name: "size over duration minus audio",
			file: File{Size: 125_000_000, Duration: 100 * time.Second, Video: []VideoStream{video(0)},
				Audio: []AudioStream{{BitRate: 1_000_000}}},
			wantBPS: 9_000_000, wantSrc: BitrateFromSize, wantKnow: true,
		},
		{
			name:    "size with no duration is unknown",
			file:    File{Size: 125_000_000, Video: []VideoStream{video(0)}},
			wantSrc: BitrateUnknown,
		},
		{
			name:    "duration with no size is unknown",
			file:    File{Duration: time.Minute, Video: []VideoStream{video(0)}},
			wantSrc: BitrateUnknown,
		},
		{
			name: "size with an unknown audio bitrate is unknown",
			file: File{Size: 125_000_000, Duration: 100 * time.Second, Video: []VideoStream{video(0)},
				Audio: []AudioStream{{BitRate: 0}}},
			wantSrc: BitrateUnknown,
		},
		{
			name:    "no video stream is unknown",
			file:    File{BitRate: 1_000_000, Audio: []AudioStream{{BitRate: 128_000}}},
			wantSrc: BitrateUnknown,
		},
		{
			name:    "cover art only is unknown",
			file:    File{BitRate: 1_000_000, Video: []VideoStream{{BitRate: 0, Disposition: Disposition{AttachedPic: true}}}},
			wantSrc: BitrateUnknown,
		},
		{
			name: "cover art is not subtracted and not taken as main",
			file: File{BitRate: 6_000_000, Video: []VideoStream{
				{Index: 0, Disposition: Disposition{AttachedPic: true}},
				{Index: 1},
			}, Audio: []AudioStream{{BitRate: 1_000_000}}},
			wantBPS: 5_000_000, wantSrc: BitrateFromContainer, wantKnow: true,
		},
		{
			name: "second real video with known bitrate is subtracted",
			file: File{BitRate: 10_000_000, Video: []VideoStream{{Index: 0}, {Index: 1, BitRate: 2_000_000}},
				Audio: []AudioStream{{BitRate: 1_000_000}}},
			wantBPS: 7_000_000, wantSrc: BitrateFromContainer, wantKnow: true,
		},
		{
			name:    "second real video with unknown bitrate is unknown",
			file:    File{BitRate: 10_000_000, Video: []VideoStream{{Index: 0}, {Index: 1}}},
			wantSrc: BitrateUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bps, known := tt.file.VideoBitrate()
			if bps != tt.wantBPS || known != tt.wantKnow {
				t.Errorf("VideoBitrate() = %d, %t; want %d, %t", bps, known, tt.wantBPS, tt.wantKnow)
			}
			if src := tt.file.VideoBitrateSource(); src != tt.wantSrc {
				t.Errorf("VideoBitrateSource() = %q, want %q", src, tt.wantSrc)
			}
		})
	}
}

func TestBitrateSourceLabel(t *testing.T) {
	for src, want := range map[BitrateSource]string{
		BitrateFromStream:    "from the stream",
		BitrateFromContainer: "estimated from the container bitrate",
		BitrateFromSize:      "estimated from file size",
		BitrateUnknown:       "unknown",
	} {
		if got := src.Label(); got != want {
			t.Errorf("%q.Label() = %q, want %q", src, got, want)
		}
	}
}

// TestVideoBitrateFromBPSTag checks the MKV statistics path end to end: the
// BPS tag reaches VideoStream.BitRate through Parse.
func TestVideoBitrateFromBPSTag(t *testing.T) {
	probe := []byte(`{"format":{"filename":"file:/media/movies/A/A.mkv","format_name":"matroska,webm","bit_rate":"30000000"},
		"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"pix_fmt":"yuv420p",
		"tags":{"BPS-eng":"12000000"}}]}`)
	f, err := Parse(probe, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	bps, known := f.VideoBitrate()
	if bps != 12_000_000 || !known || f.VideoBitrateSource() != BitrateFromStream {
		t.Errorf("VideoBitrate() = %d, %t (%q); want 12000000 from the stream", bps, known, f.VideoBitrateSource())
	}
}

func TestLossyAudioWithoutBitrateIsEstimatedHigh(t *testing.T) {
	f := &File{
		BitRate: 8_000_000,
		Video:   []VideoStream{{Index: 0, Codec: CodecH264}},
		Audio:   []AudioStream{{Index: 1, Codec: "aac", Channels: 2}},
	}
	bps, ok := f.VideoBitrate()
	if !ok || bps != 8_000_000-192_000 || f.VideoBitrateSource() != BitrateFromContainer {
		t.Fatalf("got %d %v %q", bps, ok, f.VideoBitrateSource())
	}
	f.Audio = []AudioStream{{Index: 1, Codec: "truehd", Channels: 8}}
	if _, ok := f.VideoBitrate(); ok {
		t.Fatal("lossless audio without a bitrate must leave the video bitrate unknown")
	}
	f.Audio = []AudioStream{{Index: 1, Codec: "eac3", Channels: 8}}
	if bps, _ := f.VideoBitrate(); bps != 8_000_000-768_000 {
		t.Fatalf("nominal audio must be capped: %d", bps)
	}
}
