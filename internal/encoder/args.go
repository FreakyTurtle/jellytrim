package encoder

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
)

// ErrRefused wraps every reason BuildArgs refuses a job.
var ErrRefused = errors.New("encoder refused the job")

// statisticsTags are the MKV statistics tags mkvmerge writes per stream.
// After re-encoding they describe the old stream, so they are cleared on the
// encoded one (the muxer writes a fresh DURATION).
var statisticsTags = []string{
	"BPS", "NUMBER_OF_BYTES", "NUMBER_OF_FRAMES", "DURATION",
	"_STATISTICS_TAGS", "_STATISTICS_WRITING_APP", "_STATISTICS_WRITING_DATE_UTC",
}

// commonPrefix starts every ffmpeg run (media-safety rules).
var commonPrefix = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1", "-nostats", "-y"}

// buildMode varies BuildArgs for detection and sampling.
type buildMode struct {
	// input replaces "-i file:<Input>" (detection encodes a lavfi source).
	input []string
	// segment encodes only the main video from start for length.
	segment bool
	start   time.Duration
	length  time.Duration
	// detecting skips the capability checks detection is proving.
	detecting bool
}

// BuildArgs returns the complete ffmpeg argument list for a job
// (docs/TRANSCODING.md section 4). It refuses anything it cannot encode
// safely with an error wrapping ErrRefused.
func BuildArgs(b Backend, j Job) ([]string, error) {
	return build(b, j, buildMode{})
}

func build(b Backend, j Job, m buildMode) ([]string, error) {
	if err := validate(b, j, m); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	enc, err := b.EncoderArgs(j)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	chain, err := b.FilterChain(j)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	col, err := colourOf(j.Plan)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}

	args := append([]string{}, commonPrefix...)
	args = append(args, b.InputArgs(j)...)
	args = append(args, inputArgs(j, m)...)
	args = append(args, mapArgs(j, m)...)
	args = append(args, enc...)
	if chain != "" {
		args = append(args, "-filter:v:0", chain)
	}
	args = append(args, col.flags()...)
	if !m.segment {
		args = append(args, metadataArgs(b, j)...)
	}
	args = append(args, "-max_muxing_queue_size", "4096")
	if m.segment && m.length > 0 {
		args = append(args, "-t", seconds(m.length))
	}
	return append(args, outputArgs(j, m)...), nil
}

func inputArgs(j Job, m buildMode) []string {
	if m.input != nil {
		return m.input
	}
	var args []string
	if m.segment && m.start > 0 {
		args = append(args, "-ss", seconds(m.start))
	}
	return append(args, "-i", "file:"+j.Input)
}

// mapArgs maps the main video first, so it is output stream v:0, then every
// copied stream in source order. A segment maps only the video.
func mapArgs(j Job, m buildMode) []string {
	args := []string{"-map", "0:" + strconv.Itoa(j.Plan.VideoIndex)}
	if m.segment || m.input != nil {
		return append(args, "-map_metadata", "-1", "-map_chapters", "-1", "-an", "-sn", "-dn")
	}
	for _, i := range j.Plan.CopyIndices {
		args = append(args, "-map", "0:"+strconv.Itoa(i))
	}
	return append(args, "-map_metadata", "0", "-map_chapters", "0", "-c", "copy")
}

// metadataArgs keep the main video's disposition, clear stale statistics on
// the encoded stream and mark the file as JellyTrim's.
//
// Setting any disposition by hand also stops ffmpeg inferring one: without
// it, when a type has two or more streams and none is marked default,
// ffmpeg marks the first as default, so players would start showing the
// first subtitle track.
//
// MP4 gets no JELLYTRIM tag: the MP4 muxer drops tags it does not know,
// and its use_metadata_tags option, which would keep it, rewrites all
// metadata as QuickTime keys and drops the cover art. JellyTrim's own files
// are recognised by the optimised table.
func metadataArgs(b Backend, j Job) []string {
	args := []string{"-disposition:v:0", dispositionFlags(mainDisposition(j))}
	if j.Plan.Container != media.Matroska {
		return args
	}
	for _, tag := range statisticsTags {
		args = append(args, "-metadata:s:v:0", tag+"=", "-metadata:s:v:0", tag+"-eng=")
	}
	return append(args, "-metadata", "JELLYTRIM=v1;enc="+b.Name()+";q="+j.Plan.Quality)
}

func mainDisposition(j Job) media.Disposition {
	v, _ := videoByIndex(j.Source, j.Plan.VideoIndex)
	return v.Disposition
}

// dispositionFlags writes a disposition in ffmpeg's -disposition syntax.
func dispositionFlags(d media.Disposition) string {
	var flags []string
	for _, f := range []struct {
		on   bool
		name string
	}{
		{d.Default, "default"}, {d.Forced, "forced"}, {d.HearingImpaired, "hearing_impaired"},
		{d.VisualImpaired, "visual_impaired"}, {d.Comment, "comment"}, {d.Original, "original"},
	} {
		if f.on {
			flags = append(flags, f.name)
		}
	}
	if len(flags) == 0 {
		return "0"
	}
	return strings.Join(flags, "+")
}

// outputArgs name the muxer explicitly.
func outputArgs(j Job, m buildMode) []string {
	if j.Plan.Container == media.MP4 && !m.segment && m.input == nil {
		var args []string
		if j.Plan.Codec == media.CodecHEVC {
			// Apple players and some TVs only play HEVC in MP4 tagged hvc1.
			args = append(args, "-tag:v:0", "hvc1")
		}
		return append(args, "-movflags", "+faststart", "-f", "mp4", "file:"+j.Output)
	}
	return []string{"-f", "matroska", "file:" + j.Output}
}

// seconds formats a duration as ffmpeg seconds with millisecond precision.
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}
