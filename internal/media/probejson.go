package media

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The types in this file mirror the parts of ffprobe's JSON output that
// JellyTrim reads. They are unexported: callers see only File.

// probeDoc is `ffprobe -show_format -show_streams -show_chapters -of json`.
type probeDoc struct {
	Format   probeFormat     `json:"format"`
	Streams  []probeStream   `json:"streams"`
	Chapters json.RawMessage `json:"chapters"`
}

type probeFormat struct {
	Filename   string            `json:"filename"`
	FormatName string            `json:"format_name"`
	Duration   flexString        `json:"duration"`
	Size       flexString        `json:"size"`
	BitRate    flexString        `json:"bit_rate"`
	Tags       map[string]string `json:"tags"`
}

type probeStream struct {
	Index             int               `json:"index"`
	CodecType         string            `json:"codec_type"`
	CodecName         string            `json:"codec_name"`
	CodecTagString    string            `json:"codec_tag_string"`
	Profile           flexString        `json:"profile"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
	SampleAspectRatio string            `json:"sample_aspect_ratio"`
	DisplayAspect     string            `json:"display_aspect_ratio"`
	AvgFrameRate      string            `json:"avg_frame_rate"`
	RFrameRate        string            `json:"r_frame_rate"`
	FieldOrder        string            `json:"field_order"`
	BitsPerRawSample  flexString        `json:"bits_per_raw_sample"`
	PixFmt            string            `json:"pix_fmt"`
	ColorPrimaries    string            `json:"color_primaries"`
	ColorTransfer     string            `json:"color_transfer"`
	ColorSpace        string            `json:"color_space"`
	ColorRange        string            `json:"color_range"`
	BitRate           flexString        `json:"bit_rate"`
	Channels          int               `json:"channels"`
	ChannelLayout     string            `json:"channel_layout"`
	SampleRate        flexString        `json:"sample_rate"`
	Disposition       map[string]int    `json:"disposition"`
	Tags              map[string]string `json:"tags"`
	SideData          []sideData        `json:"side_data_list"`
}

// framesDoc is the first-frame probe (docs/TRANSCODING.md section 1): the
// colour fields and side data of the first frame of v:0.
type framesDoc struct {
	Frames []probeFrame `json:"frames"`
}

type probeFrame struct {
	ColorPrimaries string     `json:"color_primaries"`
	ColorTransfer  string     `json:"color_transfer"`
	ColorSpace     string     `json:"color_space"`
	SideData       []sideData `json:"side_data_list"`
}

// sideData covers every side data shape JellyTrim reads. ffprobe writes one
// object per entry with a side_data_type and type-specific fields.
type sideData struct {
	Type string `json:"side_data_type"`

	// Display Matrix.
	Rotation flexString `json:"rotation"`

	// DOVI configuration record.
	DVProfile       flexString `json:"dv_profile"`
	DVLevel         flexString `json:"dv_level"`
	RPUPresent      flexString `json:"rpu_present_flag"`
	ELPresent       flexString `json:"el_present_flag"`
	BLPresent       flexString `json:"bl_present_flag"`
	BLCompatibility flexString `json:"dv_bl_signal_compatibility_id"`

	// Mastering display metadata: rationals such as "13250/50000".
	RedX         flexString `json:"red_x"`
	RedY         flexString `json:"red_y"`
	GreenX       flexString `json:"green_x"`
	GreenY       flexString `json:"green_y"`
	BlueX        flexString `json:"blue_x"`
	BlueY        flexString `json:"blue_y"`
	WhiteX       flexString `json:"white_point_x"`
	WhiteY       flexString `json:"white_point_y"`
	MaxLuminance flexString `json:"max_luminance"`
	MinLuminance flexString `json:"min_luminance"`

	// Content light level metadata.
	MaxContent flexString `json:"max_content"`
	MaxAverage flexString `json:"max_average"`
}

// flexString accepts a JSON string or number. ffprobe writes some numeric
// fields as strings (bit_rate, duration) and others as numbers (dv_profile,
// rotation), and the choice has changed between versions.
type flexString string

// UnmarshalJSON implements json.Unmarshaler.
func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexString(n.String())
	return nil
}

// Int64 parses the value as a whole number, accepting decimals ("-90.00").
// ok is false when the value is missing or not a number.
func (f flexString) Int64() (int64, bool) {
	s := strings.TrimSpace(string(f))
	if s == "" || strings.EqualFold(s, "N/A") {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return int64(math.Round(v)), true
}

// Int is Int64 as an int, 0 when missing.
func (f flexString) Int() int {
	n, _ := f.Int64()
	return int(n)
}

// Float parses a plain number or a rational such as "13250/50000" or
// "24000/1001". ok is false when missing, malformed or divided by zero.
func (f flexString) Float() (float64, bool) {
	return parseRational(string(f))
}

// parseRational parses "a/b" or a plain number.
func parseRational(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	num, den, found := strings.Cut(s, "/")
	n, err := strconv.ParseFloat(num, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	if !found {
		return n, true
	}
	d, err := strconv.ParseFloat(den, 64)
	if err != nil || d == 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return 0, false
	}
	return n / d, true
}
