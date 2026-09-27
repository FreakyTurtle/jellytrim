package media

import (
	"math"
	"strings"
)

// Side data type names as ffprobe 8 writes them.
const (
	sideDOVIConfig   = "DOVI configuration record"
	sideMastering    = "Mastering display metadata"
	sideContentLight = "Content light level metadata"
)

// applySideData reads Dolby Vision, static HDR and HDR10+ metadata from a
// stream's side data list.
func applySideData(v *VideoStream, side []sideData) {
	for _, sd := range side {
		switch {
		case sd.Type == sideDOVIConfig:
			if dv := dolbyVisionFrom(sd); dv != nil && v.DolbyVision == nil {
				v.DolbyVision = dv
			}
		case sd.Type == sideMastering:
			if m := masteringFrom(sd); m != nil && v.Mastering == nil {
				v.Mastering = m
			}
		case sd.Type == sideContentLight:
			if cl := contentLightFrom(sd); cl != nil && v.ContentLight == nil {
				v.ContentLight = cl
			}
		case isHDR10PlusType(sd.Type):
			v.HDR10Plus = true
		}
	}
}

// applyFrame records the first frame's colour description and side data.
// Stream values win; frame values fill gaps, because MKV and MP4 often carry
// the colour tags and static metadata only in the bitstream.
func applyFrame(v *VideoStream, fr *probeFrame) {
	v.Frame = &FrameInfo{
		Primaries: colourValue(fr.ColorPrimaries),
		Transfer:  colourValue(fr.ColorTransfer),
		Matrix:    colourValue(fr.ColorSpace),
	}
	if v.Primaries == "" {
		v.Primaries = v.Frame.Primaries
	}
	if v.Transfer == "" {
		v.Transfer = v.Frame.Transfer
	}
	if v.Matrix == "" {
		v.Matrix = v.Frame.Matrix
	}
	for _, sd := range fr.SideData {
		if strings.Contains(sd.Type, "Dolby Vision") {
			v.Frame.DolbyVisionRPU = true
		}
	}
	applySideData(v, fr.SideData)
}

func isHDR10PlusType(t string) bool {
	return strings.Contains(t, "SMPTE2094-40") || strings.Contains(t, "HDR10+")
}

// dolbyVisionFrom reads a DOVI configuration record. A record without a
// profile is ignored.
func dolbyVisionFrom(sd sideData) *DolbyVisionConfig {
	profile, ok := sd.DVProfile.Int64()
	if !ok {
		return nil
	}
	return &DolbyVisionConfig{
		Profile:           int(profile),
		Level:             sd.DVLevel.Int(),
		RPU:               sd.RPUPresent.Int() != 0,
		EL:                sd.ELPresent.Int() != 0,
		BL:                sd.BLPresent.Int() != 0,
		BLCompatibilityID: sd.BLCompatibility.Int(),
	}
}

// masteringFrom converts ffprobe's rationals to x265 master-display units:
// chromaticity in 0.00002 steps (value * 50000) and luminance in
// 0.0001 cd/m² steps (value * 10000). ffprobe omits the primaries or the
// luminance when the source lacks them; a partial record is dropped, since
// x265 needs all ten numbers and a guess would misdescribe the picture.
func masteringFrom(sd sideData) *MasteringDisplay {
	chroma := []flexString{sd.RedX, sd.RedY, sd.GreenX, sd.GreenY, sd.BlueX, sd.BlueY, sd.WhiteX, sd.WhiteY}
	vals := make([]int, 0, len(chroma)+2)
	for _, c := range chroma {
		n, ok := scaled(c, 50000)
		if !ok {
			return nil
		}
		vals = append(vals, n)
	}
	for _, l := range []flexString{sd.MaxLuminance, sd.MinLuminance} {
		n, ok := scaled(l, 10000)
		if !ok {
			return nil
		}
		vals = append(vals, n)
	}
	return &MasteringDisplay{
		RedX: vals[0], RedY: vals[1],
		GreenX: vals[2], GreenY: vals[3],
		BlueX: vals[4], BlueY: vals[5],
		WhiteX: vals[6], WhiteY: vals[7],
		MaxLuminance: vals[8], MinLuminance: vals[9],
	}
}

func scaled(f flexString, unit float64) (int, bool) {
	v, ok := f.Float()
	if !ok || v < 0 {
		return 0, false
	}
	return int(math.Round(v * unit)), true
}

// contentLightFrom reads MaxCLL and MaxFALL; nil when neither is present.
func contentLightFrom(sd sideData) *ContentLight {
	maxCLL, okCLL := sd.MaxContent.Int64()
	maxFALL, okFALL := sd.MaxAverage.Int64()
	if !okCLL && !okFALL {
		return nil
	}
	return &ContentLight{MaxCLL: int(maxCLL), MaxFALL: int(maxFALL)}
}
