package encoder

import (
	"fmt"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
)

// Colour values JellyTrim carries over, keyed by ffprobe's name, with the
// name ffmpeg's setparams filter and -color_* options accept. Anything else
// (linear or log transfers, RGB matrices, values from a newer ffprobe) is
// refused rather than guessed: the plan should have skipped such a file.
var (
	primariesNames = identity("bt709", "bt470m", "bt470bg", "smpte170m", "smpte240m", "film",
		"bt2020", "smpte428", "smpte431", "smpte432", "jedec-p22")
	transferNames = merge(identity("bt709", "smpte170m", "smpte240m", "iec61966-2-1",
		"iec61966-2-4", "bt1361e", "bt2020-10", "bt2020-12", "smpte2084", "arib-std-b67"),
		// ffprobe 8 prints BT.470 transfers as gamma22 and gamma28;
		// setparams only knows the bt470 names.
		map[string]string{"gamma22": "bt470m", "gamma28": "bt470bg", "bt470m": "bt470m", "bt470bg": "bt470bg"})
	matrixNames = identity("bt709", "fcc", "bt470bg", "smpte170m", "smpte240m", "bt2020nc", "bt2020c")
)

func identity(names ...string) map[string]string {
	m := make(map[string]string, len(names))
	for _, n := range names {
		m[n] = n
	}
	return m
}

func merge(a, b map[string]string) map[string]string {
	for k, v := range b {
		a[k] = v
	}
	return a
}

// colour is the output colour description in ffmpeg's names; "" is unset.
type colour struct {
	primaries, transfer, matrix string
}

// colourOf translates the plan's colour tags. Untagged values stay unset,
// so an untagged source gives an untagged output.
func colourOf(p plan.Plan) (colour, error) {
	var c colour
	var err error
	if c.primaries, err = lookup("primaries", p.Primaries, primariesNames); err != nil {
		return c, err
	}
	if c.transfer, err = lookup("transfer", p.Transfer, transferNames); err != nil {
		return c, err
	}
	if c.matrix, err = lookup("matrix", p.Matrix, matrixNames); err != nil {
		return c, err
	}
	return c, c.checkClass(p.HDR)
}

func lookup(what, v string, names map[string]string) (string, error) {
	if v == "" {
		return "", nil
	}
	if n, ok := names[v]; ok {
		return n, nil
	}
	return "", fmt.Errorf("colour %s %q cannot be carried over", what, v)
}

// checkClass makes sure HDR output is fully described, so the encoder never
// writes HDR pixels with SDR or missing tags.
func (c colour) checkClass(class media.HDRClass) error {
	want := map[media.HDRClass]string{media.HDR10: "smpte2084", media.HLG: "arib-std-b67"}[class]
	if want == "" {
		if c.transfer == "smpte2084" || c.transfer == "arib-std-b67" {
			return fmt.Errorf("transfer %s needs an HDR plan, not %s", c.transfer, class)
		}
		return nil
	}
	if c.transfer != want || c.primaries == "" || c.matrix == "" {
		return fmt.Errorf("%s output needs transfer %s with primaries and matrix, got %q/%q/%q",
			class.Label(), want, c.primaries, c.transfer, c.matrix)
	}
	return nil
}

// setparams is the filter that tags frames. ffmpeg 8 takes the stream's
// colour tags from frame properties, so without it the -color_* output
// options alone are lost after a format filter.
func (c colour) setparams() string {
	var parts []string
	if c.primaries != "" {
		parts = append(parts, "color_primaries="+c.primaries)
	}
	if c.transfer != "" {
		parts = append(parts, "color_trc="+c.transfer)
	}
	if c.matrix != "" {
		parts = append(parts, "colorspace="+c.matrix)
	}
	if len(parts) == 0 {
		return ""
	}
	return "setparams=" + strings.Join(parts, ":")
}

// flags are the matching stream options for output stream v:0.
func (c colour) flags() []string {
	var args []string
	if c.primaries != "" {
		args = append(args, "-color_primaries:v:0", c.primaries)
	}
	if c.transfer != "" {
		args = append(args, "-color_trc:v:0", c.transfer)
	}
	if c.matrix != "" {
		args = append(args, "-colorspace:v:0", c.matrix)
	}
	return args
}

// sarFilter keeps the source's sample aspect ratio after scaling. scale
// with explicit dimensions otherwise adjusts the SAR to preserve the exact
// display aspect, turning square pixels into odd ratios like 1596:1600.
func sarFilter(sar string) string {
	num, den, ok := strings.Cut(sar, ":")
	if !ok || !positiveInt(num) || !positiveInt(den) {
		return "setsar=1/1"
	}
	return "setsar=" + num + "/" + den
}

func positiveInt(s string) bool {
	if s == "" || len(s) > 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return strings.TrimLeft(s, "0") != ""
}

// joinFilters joins non-empty filters into one chain.
func joinFilters(fs ...string) string {
	out := fs[:0:0]
	for _, f := range fs {
		if f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, ",")
}
