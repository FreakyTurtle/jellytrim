package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/freakyturtle/jellytrim/internal/plan"
)

// The fake prober answers from the ffprobe fixtures in
// internal/media/testdata/probe, patched so that duration, bitrate and size
// match the file on disk, with the audio and subtitle tracks a real release
// of that kind has. It never runs ffprobe.

// profile describes one kind of file.
type profile struct {
	fixture string // fixture name, without .json
	audio   []track
	subs    []track
}

// track is an audio or subtitle stream to add.
type track struct {
	codec    string
	lang     string
	title    string
	channels int
	layout   string
	bitRate  int64
	sdh      bool
}

var (
	remuxAudio = []track{
		{codec: "truehd", lang: "eng", title: "Dolby TrueHD 7.1", channels: 8, layout: "7.1", bitRate: 4_300_000},
		{codec: "ac3", lang: "eng", title: "AC-3 5.1", channels: 6, layout: "5.1(side)", bitRate: 640_000},
		{codec: "ac3", lang: "eng", title: "Commentary", channels: 2, layout: "stereo", bitRate: 192_000},
	}
	remuxSubs = []track{
		{codec: "hdmv_pgs_subtitle", lang: "eng", title: "English"},
		{codec: "hdmv_pgs_subtitle", lang: "eng", title: "English SDH", sdh: true},
		{codec: "hdmv_pgs_subtitle", lang: "fre", title: "Français"},
		{codec: "hdmv_pgs_subtitle", lang: "ger", title: "Deutsch"},
		{codec: "hdmv_pgs_subtitle", lang: "spa", title: "Español"},
	}
	blurayAudio = []track{
		{codec: "dts", lang: "eng", title: "DTS 5.1", channels: 6, layout: "5.1(side)", bitRate: 1_509_000},
		{codec: "ac3", lang: "eng", title: "Stereo", channels: 2, layout: "stereo", bitRate: 192_000},
	}
	textSubs = []track{
		{codec: "subrip", lang: "eng", title: "English"},
		{codec: "subrip", lang: "eng", title: "English SDH", sdh: true},
		{codec: "subrip", lang: "fre", title: "Français"},
	}
	webAudio = []track{{codec: "eac3", lang: "eng", title: "E-AC-3 5.1", channels: 6, layout: "5.1(side)", bitRate: 640_000}}
)

var profiles = map[source]profile{
	uhdH264:    {fixture: "h264-2160p", audio: remuxAudio, subs: remuxSubs},
	uhdHDR10:   {fixture: "hevc-2160p-hdr10", audio: remuxAudio, subs: remuxSubs},
	uhdDV5:     {fixture: "dv-profile5"},
	fhdH264:    {fixture: "h264-1080p", audio: blurayAudio, subs: textSubs},
	fhdScope:   {fixture: "scope-1080p", audio: blurayAudio, subs: textSubs},
	fhdHEVC:    {fixture: "hevc-1080p", audio: webAudio, subs: textSubs[:2]},
	fhdAV1:     {fixture: "av1-1080p", subs: textSubs[:1]},
	webH264:    {fixture: "tv-episode", audio: webAudio, subs: textSubs[:2]},
	webHEVC:    {fixture: "hevc-1080p", audio: []track{{codec: "eac3", lang: "eng", title: "Stereo", channels: 2, layout: "stereo", bitRate: 224_000}}, subs: textSubs[:1]},
	interlaced: {fixture: "interlaced", audio: []track{{codec: "ac3", lang: "eng", title: "Broadcast stereo", channels: 2, layout: "stereo", bitRate: 256_000}}},
}

// fixtures reads the probe fixtures.
type fixtures struct {
	root *os.Root
}

// openFixtures finds internal/media/testdata/probe from this source file,
// which works for `go run` and `go test` in a checkout.
func openFixtures() (*fixtures, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return nil, errors.New("cannot locate the demo's source directory")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "internal", "media", "testdata", "probe")
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening the probe fixtures (run the demo from a checkout): %w", err)
	}
	return &fixtures{root: root}, nil
}

func (f *fixtures) close() { _ = f.root.Close() }

// load returns a fixture's probe JSON as a map, and its frames JSON.
func (f *fixtures) load(name string) (map[string]any, []byte, error) {
	raw, err := f.root.ReadFile(name + ".json")
	if err != nil {
		return nil, nil, err
	}
	frames, err := f.root.ReadFile(name + ".frames.json")
	if err != nil {
		return nil, nil, err
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, nil, fmt.Errorf("reading fixture %s: %w", name, err)
	}
	return d, frames, nil
}

// sourceProbe builds the ffprobe output for an entry's file as it was
// added to the library.
func (f *fixtures) sourceProbe(e entry, local string) ([]byte, []byte, error) {
	p, ok := profiles[e.Source]
	if !ok {
		return nil, nil, fmt.Errorf("no probe profile for %s", e.Source)
	}
	d, frames, err := f.load(p.fixture)
	if err != nil {
		return nil, nil, err
	}
	streams := asList(d["streams"])
	video := asMap(streams[0])
	if video["codec_name"] == "h264" {
		// The fixtures are tiny test encodes; real releases use High.
		video["profile"], video["level"] = "High", 41
		if e.Source == uhdH264 {
			video["level"] = 51
		}
	}
	var out []any
	out = append(out, video)
	if p.audio == nil {
		for _, s := range streams[1:] {
			if asMap(s)["codec_type"] == "audio" {
				out = append(out, s)
			}
		}
	}
	for i, t := range p.audio {
		out = append(out, audioStream(t, len(out), i == 0))
	}
	for _, t := range p.subs {
		out = append(out, subtitleStream(t, len(out)))
	}
	d["streams"] = out
	d["chapters"] = chapters(e.Duration)
	format := asMap(d["format"])
	format["filename"] = "file:" + local
	format["nb_streams"] = len(out)
	fitSize(d, e.Size, e.Duration)
	b, err := json.Marshal(d)
	return b, frames, err
}

// outputProbe builds the ffprobe output of the file a past job wrote: the
// source's audio, subtitles and chapters, with the plan's video.
func (f *fixtures) outputProbe(srcJSON []byte, p plan.Plan, size int64, dur time.Duration) ([]byte, []byte, error) {
	var d map[string]any
	if err := json.Unmarshal(srcJSON, &d); err != nil {
		return nil, nil, err
	}
	name := "hevc-1080p"
	if p.HDR.IsHDR() {
		name = "hevc-2160p-hdr10"
	}
	tmpl, frames, err := f.load(name)
	if err != nil {
		return nil, nil, err
	}
	v := asMap(asList(tmpl["streams"])[0])
	v["index"] = 0
	v["width"], v["height"], v["coded_width"], v["coded_height"] = p.Width, p.Height, p.Width, p.Height
	if p.BitDepth == 10 {
		v["pix_fmt"], v["profile"] = "yuv420p10le", "Main 10"
	}
	v["tags"] = map[string]any{"ENCODER": "Lavc62.28.102 libx265", "DURATION": clock(dur)}
	delete(v, "bit_rate")
	streams := asList(d["streams"])
	streams[0] = v
	format := asMap(d["format"])
	format["tags"] = map[string]any{"ENCODER": "Lavf62.12.102", "JELLYTRIM": "v1;enc=" + p.Encoder + ";q=" + p.Quality}
	fitSize(d, size, dur)
	b, err := json.Marshal(d)
	return b, frames, err
}

// fitSize makes the container's size, duration and bitrate agree with the
// file, and the video stream's own bitrate where the fixture has one.
func fitSize(d map[string]any, size int64, dur time.Duration) {
	secs := dur.Seconds()
	total := int64(float64(size) * 8 / secs)
	format := asMap(d["format"])
	format["size"] = strconv.FormatInt(size, 10)
	format["duration"] = strconv.FormatFloat(secs, 'f', 6, 64)
	format["bit_rate"] = strconv.FormatInt(total, 10)
	var audio int64
	for _, s := range asList(d["streams"]) {
		m := asMap(s)
		if m["codec_type"] == "audio" {
			n, _ := strconv.ParseInt(fmt.Sprint(m["bit_rate"]), 10, 64)
			audio += n
		}
	}
	for _, s := range asList(d["streams"]) {
		m := asMap(s)
		tags, _ := m["tags"].(map[string]any)
		if tags != nil {
			tags["DURATION"] = clock(dur)
		}
		if _, ok := m["duration"]; ok {
			m["duration"] = format["duration"]
		}
		if m["codec_type"] == "video" {
			if _, ok := m["bit_rate"]; ok {
				m["bit_rate"] = strconv.FormatInt(total-audio, 10)
			}
			if _, ok := m["nb_frames"]; ok {
				m["nb_frames"] = strconv.Itoa(int(secs * 24))
			}
			delete(m, "duration_ts")
		}
	}
}

func audioStream(t track, index int, first bool) map[string]any {
	return map[string]any{
		"index": index, "codec_name": t.codec, "codec_type": "audio", "sample_fmt": "fltp", "sample_rate": "48000",
		"channels": t.channels, "channel_layout": t.layout, "bit_rate": strconv.FormatInt(t.bitRate, 10),
		"disposition": map[string]any{"default": b2i(first), "forced": 0, "hearing_impaired": 0, "comment": b2i(t.title == "Commentary")},
		"tags":        map[string]any{"language": t.lang, "title": t.title},
	}
}

func subtitleStream(t track, index int) map[string]any {
	m := map[string]any{
		"index": index, "codec_name": t.codec, "codec_type": "subtitle",
		"disposition": map[string]any{"default": 0, "forced": 0, "hearing_impaired": b2i(t.sdh)},
		"tags":        map[string]any{"language": t.lang, "title": t.title},
	}
	if t.codec == "hdmv_pgs_subtitle" {
		m["width"], m["height"] = 1920, 1080
	}
	return m
}

// chapters are evenly spaced, about every ten minutes.
func chapters(dur time.Duration) []any {
	n := max(int(dur/(10*time.Minute)), 2)
	step := dur / time.Duration(n)
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		start, end := step*time.Duration(i), step*time.Duration(i+1)
		out = append(out, map[string]any{
			"id": i, "time_base": "1/1000000000", "start": start.Nanoseconds(), "end": end.Nanoseconds(),
			"start_time": strconv.FormatFloat(start.Seconds(), 'f', 6, 64), "end_time": strconv.FormatFloat(end.Seconds(), 'f', 6, 64),
			"tags": map[string]any{"title": fmt.Sprintf("Chapter %02d", i+1)},
		})
	}
	return out
}

// clock is a Matroska DURATION tag: "01:58:03.000000000".
func clock(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d.000000000", s/3600, s/60%60, s%60)
}

func asList(v any) []any         { l, _ := v.([]any); return l }
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// prober is the fake ffprobe. Each file has the probe of its original and,
// once a past job replaced it, the probe of the new file; which one it
// answers with depends on the file's size now, so restoring an original
// from its backup shows the original again.
type prober struct {
	mu    sync.Mutex
	files map[string]*probed
}

type probed struct {
	probe, frames       []byte
	outSize             int64
	outProbe, outFrames []byte
}

func newProber() *prober { return &prober{files: map[string]*probed{}} }

func (p *prober) add(path string, probe, frames []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[path] = &probed{probe: probe, frames: frames}
}

// addOutput registers the file a past job wrote at path.
func (p *prober) addOutput(path string, size int64, probe, frames []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f := p.files[path]; f != nil {
		f.outSize, f.outProbe, f.outFrames = size, probe, frames
	}
}

// source returns the original's probe and frames for path.
func (p *prober) source(path string) (probe, frames []byte, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.files[path]
	if f == nil {
		return nil, nil, false
	}
	return f.probe, f.frames, true
}

// Probe implements library.Prober.
func (p *prober) Probe(_ context.Context, path string) ([]byte, []byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.files[path]
	if f == nil {
		return nil, nil, fmt.Errorf("the demo has no probe for %s", path)
	}
	if f.outProbe != nil && fi.Size() == f.outSize {
		return f.outProbe, f.outFrames, nil
	}
	return f.probe, f.frames, nil
}
