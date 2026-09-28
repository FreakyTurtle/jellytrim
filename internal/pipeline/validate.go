package pipeline

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// validate probes the partial file, compares it with the source and the
// plan, and decodes it. On failure the partial file is removed.
func (p *Pipeline) validate(ctx context.Context, j Job, partial string, diag *Diagnostics) (*media.File, Result, bool) {
	diag.Step = "validating"
	fail := func(outcome Outcome, summary string) (*media.File, Result, bool) {
		p.removePartial(context.WithoutCancel(ctx), j.ID, partial, diag)
		return nil, Result{Outcome: outcome, Summary: summary, Diagnostics: *diag}, false
	}
	info, err := p.FS.Stat(partial)
	if err != nil {
		diag.Error = err.Error()
		return fail(Failed, "The encoder did not produce a file. The original is unchanged.")
	}
	probeJSON, frameJSON, err := p.Runner.Probe(ctx, partial)
	if err != nil {
		diag.Error = err.Error()
		return fail(Failed, "The new file could not be read back, so it was discarded. The original is unchanged.")
	}
	out, err := media.Parse(probeJSON, frameJSON, info.Size)
	if err != nil {
		diag.Error = err.Error()
		return fail(Failed, "The new file could not be read back, so it was discarded. The original is unchanged.")
	}
	// The partial file's extension hides the container from media.Parse; the
	// muxer was chosen explicitly, so read the family from the format name.
	out.Container = containerOf(out.FormatName)
	checks := Validate(j.Source, out, j.Plan, j.MinSavingPercent)
	diag.Checks = checks
	for _, c := range checks {
		if c.Pass {
			continue
		}
		if c.Name == "saving" {
			return fail(Skipped, c.Detail+" The new file was discarded and the original is unchanged.")
		}
		return fail(Failed, "The new file failed a check ("+c.Detail+"), so it was discarded. The original is unchanged.")
	}
	if err := p.Runner.Decode(ctx, partial, j.SampledDecode, out.Duration); err != nil {
		diag.Checks = append(diag.Checks, Check{Name: "decode", Pass: false, Detail: err.Error()})
		if ctx.Err() != nil {
			return fail(Interrupted, "Stopped before finishing. The original is unchanged.")
		}
		return fail(Failed, "The new file did not decode cleanly, so it was discarded. The original is unchanged.")
	}
	diag.Checks = append(diag.Checks, Check{Name: "decode", Pass: true, Detail: "decoded without errors"})
	return out, Result{}, true
}

// Validate compares an encoded file with its source and plan. Every check is
// returned, passed or not, for the technical details.
func Validate(src, out *media.File, pl plan.Plan, minSavingPercent int) []Check {
	var cs []Check
	add := func(name string, pass bool, detail string) { cs = append(cs, Check{name, pass, detail}) }

	add("container", out.Container == pl.Container, fmt.Sprintf("container %s (expected %s)", out.Container, pl.Container))
	cs = append(cs, videoChecks(out, pl)...)
	cs = append(cs, colourChecks(out, pl)...)
	cs = append(cs, streamChecks(src, out)...)
	if c, ok := videoDurationCheck(src, out); ok {
		cs = append(cs, c)
	}
	add("chapters", out.Chapters == src.Chapters, fmt.Sprintf("%d chapters (source has %d)", out.Chapters, src.Chapters))

	tolerance := max(500*time.Millisecond, time.Duration(float64(src.Duration)*0.005))
	diff := out.Duration - src.Duration
	if diff < 0 {
		diff = -diff
	}
	add("duration", src.Duration > 0 && diff <= tolerance,
		fmt.Sprintf("duration %s differs from the source by %s (allowed %s)", out.Duration.Round(time.Millisecond), diff.Round(time.Millisecond), tolerance.Round(time.Millisecond)))
	if out.Container == media.Matroska {
		// MP4 cannot carry the tag; the optimised table recognises those.
		add("marker", out.JellyTrimTag() != "", "JELLYTRIM tag present")
	}

	limit := float64(src.Size) * (1 - float64(minSavingPercent)/100)
	saving := 0.0
	if src.Size > 0 {
		saving = 1 - float64(out.Size)/float64(src.Size)
	}
	add("saving", src.Size > 0 && out.Size > 0 && float64(out.Size) <= limit,
		fmt.Sprintf("The new file is %s against %s, a saving of %d%%, below the %d%% minimum.",
			units.Bytes(out.Size), units.Bytes(src.Size), int(math.Round(saving*100)), minSavingPercent))
	if cs[len(cs)-1].Pass {
		cs[len(cs)-1].Detail = fmt.Sprintf("%s to %s, saving %d%%", units.Bytes(src.Size), units.Bytes(out.Size), int(math.Round(saving*100)))
	}
	return cs
}

func videoChecks(out *media.File, pl plan.Plan) []Check {
	v, ok := out.MainVideo()
	if !ok || out.RealVideoCount() != 1 {
		return []Check{{"video", false, fmt.Sprintf("expected one video stream, found %d", out.RealVideoCount())}}
	}
	cs := []Check{
		{"codec", v.Codec == pl.Codec, fmt.Sprintf("video codec %s (expected %s)", v.Codec.Label(), pl.Codec.Label())},
		{"size", abs(v.Width-pl.Width) <= 2 && abs(v.Height-pl.Height) <= 2,
			fmt.Sprintf("video %dx%d (expected %dx%d)", v.Width, v.Height, pl.Width, pl.Height)},
	}
	if pl.BitDepth > 0 {
		cs = append(cs, Check{"bit_depth", v.BitDepth == pl.BitDepth, fmt.Sprintf("%d-bit (expected %d-bit)", v.BitDepth, pl.BitDepth)})
	}
	return cs
}

// colourChecks makes sure HDR survives and SDR is not mislabelled.
func colourChecks(out *media.File, pl plan.Plan) []Check {
	v, ok := out.MainVideo()
	if !ok {
		return nil
	}
	h := out.HDR()
	cs := []Check{{"hdr", h.Class == pl.HDR || (!pl.HDR.IsHDR() && !h.Class.IsHDR()),
		fmt.Sprintf("dynamic range %s (expected %s)", h.Class.Label(), pl.HDR.Label())}}
	if pl.HDR.IsHDR() || pl.Transfer != "" {
		cs = append(cs,
			Check{"transfer", v.Transfer == pl.Transfer, fmt.Sprintf("transfer %q (expected %q)", v.Transfer, pl.Transfer)},
			Check{"primaries", v.Primaries == pl.Primaries, fmt.Sprintf("primaries %q (expected %q)", v.Primaries, pl.Primaries)})
	}
	if pl.Matrix != "" {
		cs = append(cs, Check{"matrix", v.Matrix == pl.Matrix, fmt.Sprintf("matrix %q (expected %q)", v.Matrix, pl.Matrix)})
	}
	if pl.HDR == media.HDR10 {
		if pl.Mastering != nil {
			cs = append(cs, Check{"mastering_display", v.Mastering != nil && *v.Mastering == *pl.Mastering, "mastering display metadata kept"})
		}
		if pl.ContentLight != nil {
			cs = append(cs, Check{"content_light", v.ContentLight != nil && *v.ContentLight == *pl.ContentLight, "content light level metadata kept"})
		}
	}
	return cs
}

// streamChecks makes sure every audio, subtitle, attachment and cover art
// stream survived with its language, title and flags, in order.
func streamChecks(src, out *media.File) []Check {
	var cs []Check
	add := func(name string, pass bool, detail string) { cs = append(cs, Check{name, pass, detail}) }
	add("audio_count", len(out.Audio) == len(src.Audio), fmt.Sprintf("%d audio streams (source has %d)", len(out.Audio), len(src.Audio)))
	add("subtitle_count", len(out.Subtitles) == len(src.Subtitles), fmt.Sprintf("%d subtitle streams (source has %d)", len(out.Subtitles), len(src.Subtitles)))
	add("attachment_count", len(out.Attachments) == len(src.Attachments), fmt.Sprintf("%d attachments (source has %d)", len(out.Attachments), len(src.Attachments)))
	add("cover_art", len(out.CoverArt()) == len(src.CoverArt()), fmt.Sprintf("%d cover images (source has %d)", len(out.CoverArt()), len(src.CoverArt())))
	if len(out.Audio) == len(src.Audio) {
		for i, a := range src.Audio {
			b := out.Audio[i]
			same := a.Codec == b.Codec && a.Channels == b.Channels && a.Language == b.Language && a.Title == b.Title &&
				a.Disposition.Default == b.Disposition.Default && a.Disposition.Forced == b.Disposition.Forced &&
				a.Disposition.Comment == b.Disposition.Comment
			add(fmt.Sprintf("audio_%d", i+1), same, streamDetail(b.Codec, b.Language, b.Title))
		}
	}
	if len(out.Subtitles) == len(src.Subtitles) {
		for i, s := range src.Subtitles {
			b := out.Subtitles[i]
			same := s.Codec == b.Codec && s.Language == b.Language && s.Title == b.Title &&
				s.Disposition.Default == b.Disposition.Default && s.Disposition.Forced == b.Disposition.Forced &&
				s.Disposition.HearingImpaired == b.Disposition.HearingImpaired
			add(fmt.Sprintf("subtitle_%d", i+1), same, streamDetail(b.Codec, b.Language, b.Title))
		}
	}
	if len(out.Attachments) == len(src.Attachments) {
		for i, a := range src.Attachments {
			b := out.Attachments[i]
			add(fmt.Sprintf("attachment_%d", i+1), strings.EqualFold(a.FileName, b.FileName) && strings.EqualFold(a.MimeType, b.MimeType),
				fmt.Sprintf("%s (%s)", b.FileName, b.MimeType))
		}
	}
	return cs
}

// streamDetail describes a kept stream: codec, language and title if any.
func streamDetail(codec, language, title string) string {
	d := codec + ", " + orUnknown(language)
	if title != "" {
		d += `, "` + title + `"`
	}
	return d
}

func orUnknown(s string) string {
	if s == "" {
		return "und"
	}
	return s
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func containerOf(formatName string) media.Container {
	switch {
	case strings.Contains(formatName, "matroska"):
		return media.Matroska
	case strings.Contains(formatName, "mp4"):
		return media.MP4
	}
	return media.OtherContainer
}

// videoDurationCheck compares the main video stream's own duration (the
// DURATION tag Matroska muxers write) with the source's, so dropped frames
// cannot hide behind a longer audio track. It is skipped when either side
// does not record it.
func videoDurationCheck(src, out *media.File) (Check, bool) {
	sv, ok1 := src.MainVideo()
	ov, ok2 := out.MainVideo()
	if !ok1 || !ok2 {
		return Check{}, false
	}
	sd, okS := tagDuration(sv.Tags)
	od, okO := tagDuration(ov.Tags)
	if !okS || !okO {
		return Check{}, false
	}
	tolerance := max(500*time.Millisecond, time.Duration(float64(sd)*0.005))
	diff := od - sd
	if diff < 0 {
		diff = -diff
	}
	return Check{"video_duration", diff <= tolerance, fmt.Sprintf("video stream lasts %s (source %s)", od.Round(time.Millisecond), sd.Round(time.Millisecond))}, true
}

// tagDuration reads a Matroska DURATION tag such as "01:52:03.041000000".
func tagDuration(tags map[string]string) (time.Duration, bool) {
	for k, v := range tags {
		if !strings.EqualFold(k, "DURATION") && !strings.HasPrefix(strings.ToUpper(k), "DURATION-") {
			continue
		}
		var h, m int
		var s float64
		if n, _ := fmt.Sscanf(v, "%d:%d:%f", &h, &m, &s); n == 3 {
			return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s*float64(time.Second)), true
		}
	}
	return 0, false
}
