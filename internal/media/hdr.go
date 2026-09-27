package media

import (
	"fmt"
	"strings"
)

// Colour values as ffprobe names them.
const (
	transferPQ     = "smpte2084"
	transferHLG    = "arib-std-b67"
	primariesBT709 = "bt709"
	primaries2020  = "bt2020"
)

// sdrTransfers are transfer characteristics with an SDR curve. ffprobe
// names BT.470 M and BG "gamma22" and "gamma28"; both spellings are
// accepted. bt2020-10 and bt2020-12 are the BT.709 curve under another name.
var sdrTransfers = map[string]string{
	"bt709":        "BT.709",
	"smpte170m":    "BT.601",
	"bt601":        "BT.601",
	"bt470bg":      "BT.470 BG",
	"gamma28":      "BT.470 BG",
	"bt470m":       "BT.470 M",
	"gamma22":      "BT.470 M",
	"smpte240m":    "SMPTE 240M",
	"iec61966-2-1": "sRGB",
	"bt2020-10":    "BT.2020 SDR",
	"bt2020-12":    "BT.2020 SDR",
}

// sdrPrimaries are colour primaries JellyTrim accepts on SDR video.
var sdrPrimaries = map[string]string{
	"bt709":     "BT.709",
	"smpte170m": "BT.601 NTSC",
	"bt470bg":   "BT.601 PAL",
	"bt470m":    "BT.470 M",
}

// dolbyVisionTags are codec tags that only Dolby Vision streams use.
var dolbyVisionTags = map[string]bool{"dvh1": true, "dvhe": true, "dav1": true, "dva1": true}

// HDR classifies the main video stream's dynamic range, following
// docs/TRANSCODING.md section 2. Every path that is not clearly SDR, HDR10,
// HLG, HDR10+ or a recognised Dolby Vision profile ends in Unclear, which is
// always skipped.
func (f *File) HDR() HDRInfo {
	v, ok := f.MainVideo()
	if !ok {
		return HDRInfo{Class: Unclear, Facts: []string{"no video stream"}}
	}
	return classifyHDR(v)
}

func classifyHDR(v VideoStream) HDRInfo {
	if v.DolbyVision != nil {
		return classifyDolbyVision(v)
	}
	if tag := strings.ToLower(v.CodecTag); dolbyVisionTags[tag] {
		return unclear(fmt.Sprintf("codec tag %s signals Dolby Vision but there is no Dolby Vision configuration record", tag))
	}
	if v.Frame != nil && v.Frame.DolbyVisionRPU {
		return unclear("the first frame carries Dolby Vision metadata but there is no Dolby Vision configuration record")
	}
	if c := colourConflict(v); c != "" {
		return unclear(c)
	}
	if v.HDR10Plus && v.Transfer != transferPQ {
		return unclear("HDR10+ dynamic metadata without a PQ transfer", transferFact(v.Transfer))
	}
	switch {
	case v.Transfer == transferPQ:
		return classifyPQ(v)
	case v.Transfer == transferHLG:
		return HDRInfo{Class: HLG, Facts: []string{transferFact(v.Transfer), primariesFact(v.Primaries)}}
	case v.Mastering != nil || v.ContentLight != nil:
		return unclear("HDR static metadata without a PQ or HLG transfer", transferFact(v.Transfer))
	case v.Transfer == "":
		return classifyUnsetTransfer(v)
	case sdrTransfers[v.Transfer] != "":
		return classifySDRTransfer(v)
	}
	return unclear(fmt.Sprintf("transfer %s is not one JellyTrim recognises", v.Transfer))
}

func classifyPQ(v VideoStream) HDRInfo {
	facts := []string{transferFact(v.Transfer), primariesFact(v.Primaries)}
	if v.Primaries != primaries2020 {
		return unclear(append(facts, "PQ video should have BT.2020 primaries")...)
	}
	if v.BitDepth < 10 {
		return unclear(append(facts, fmt.Sprintf("PQ transfer on %s video", depthText(v.BitDepth)))...)
	}
	if v.HDR10Plus {
		return HDRInfo{Class: HDR10Plus, Facts: append(facts, "HDR10+ dynamic metadata (SMPTE 2094-40) is present")}
	}
	if v.Frame == nil {
		return unclear(append(facts, "no first-frame data, so HDR10+ cannot be ruled out")...)
	}
	facts = append(facts, staticMetadataFact(v))
	return HDRInfo{Class: HDR10, Facts: facts}
}

func classifySDRTransfer(v VideoStream) HDRInfo {
	facts := []string{transferFact(v.Transfer)}
	if v.Primaries == primaries2020 {
		return unclear(append(facts, "BT.2020 primaries with an SDR transfer")...)
	}
	if v.Primaries != "" {
		facts = append(facts, primariesFact(v.Primaries))
	}
	return HDRInfo{Class: SDR, Facts: facts}
}

// classifyUnsetTransfer handles streams with no transfer characteristics.
// By the time it runs, classifyHDR has already sent every stream with HDR
// evidence (Dolby Vision, HDR10+, static metadata) elsewhere. What is left
// is SDR unless a BT.2020 matrix or primaries hint at HDR whose transfer
// was lost. Untagged 10-bit video is common in SDR encodes (x265 and AV1),
// so it counts as SDR; the output keeps the same unset tags, so nothing is
// reinterpreted.
func classifyUnsetTransfer(v VideoStream) HDRInfo {
	noTransfer := fmt.Sprintf("%s video with no transfer characteristics", depthText(v.BitDepth))
	switch {
	case v.Primaries == primaries2020:
		return unclear("BT.2020 primaries with no transfer characteristics")
	case isBT2020Matrix(v.Matrix):
		return unclear(fmt.Sprintf("BT.2020 matrix (%s) with no transfer characteristics", v.Matrix))
	case v.BitDepth <= 0:
		return unclear("bit depth unknown and no transfer characteristics")
	case v.Primaries != "" && sdrPrimaries[v.Primaries] == "":
		return unclear(noTransfer, primariesFact(v.Primaries))
	case v.Primaries != "":
		return HDRInfo{Class: SDR, Facts: []string{noTransfer, primariesFact(v.Primaries)}}
	case v.BitDepth >= 10:
		return HDRInfo{Class: SDR, Facts: []string{fmt.Sprintf(
			"%s video without colour tags or HDR metadata; treated as SDR, and the output keeps the same (unset) colour tags",
			depthText(v.BitDepth))}}
	}
	return HDRInfo{Class: SDR, Facts: []string{noTransfer + " is treated as SDR"}}
}

func isBT2020Matrix(m string) bool {
	return m == "bt2020nc" || m == "bt2020c"
}

func classifyDolbyVision(v VideoStream) HDRInfo {
	dv := v.DolbyVision
	fact := fmt.Sprintf("Dolby Vision profile %d, base layer compatibility ID %d", dv.Profile, dv.BLCompatibilityID)
	switch dv.Profile {
	case 7:
		return dolbyVisionHDR10Base(v, fact)
	case 8:
		switch dv.BLCompatibilityID {
		case 1:
			return dolbyVisionHDR10Base(v, fact)
		case 4:
			return dolbyVision(fact, "Dolby Vision profile 8.4 has an HLG base layer, which JellyTrim does not handle yet")
		case 2:
			return dolbyVision(fact, "Dolby Vision profile 8.2 has an SDR base layer, which JellyTrim does not handle yet")
		case 0:
			return dolbyVision(fact, "Dolby Vision profile 8 with compatibility ID 0 has no usable base layer")
		}
		return dolbyVision(fact, fmt.Sprintf("Dolby Vision profile 8 with compatibility ID %d is not recognised", dv.BLCompatibilityID))
	case 5:
		return dolbyVision(fact, "Dolby Vision profile 5 has no HDR10 base layer")
	}
	return dolbyVision(fact, fmt.Sprintf("Dolby Vision profile %d is not supported", dv.Profile))
}

// dolbyVisionHDR10Base checks that a profile 7 or 8.1 stream's base layer
// really looks like HDR10 before calling it one, because reducing it to
// HDR10 copies these colour tags to the output.
func dolbyVisionHDR10Base(v VideoStream, fact string) HDRInfo {
	facts := []string{fact}
	if !v.DolbyVision.BL {
		return unclear(append(facts, "the configuration record says there is no base layer")...)
	}
	if c := colourConflict(v); c != "" {
		return unclear(append(facts, c)...)
	}
	facts = append(facts, transferFact(v.Transfer), primariesFact(v.Primaries))
	if v.Transfer != transferPQ || v.Primaries != primaries2020 {
		return unclear(append(facts, "the base layer should be PQ with BT.2020 primaries")...)
	}
	return HDRInfo{Class: DolbyVisionHDR10, Facts: append(facts, "the base layer is HDR10", staticMetadataFact(v))}
}

// colourConflict describes a disagreement between the stream's colour tags
// and the first frame's, or returns "". Parse fills empty stream tags from
// the frame, so any difference left is a real contradiction.
func colourConflict(v VideoStream) string {
	if v.Frame == nil {
		return ""
	}
	if v.Frame.Transfer != "" && v.Frame.Transfer != v.Transfer {
		return fmt.Sprintf("the stream says transfer %s but the first frame says %s", v.Transfer, v.Frame.Transfer)
	}
	if v.Frame.Primaries != "" && v.Frame.Primaries != v.Primaries {
		return fmt.Sprintf("the stream says primaries %s but the first frame says %s", v.Primaries, v.Frame.Primaries)
	}
	return ""
}

func unclear(facts ...string) HDRInfo {
	return HDRInfo{Class: Unclear, Facts: facts}
}

func dolbyVision(facts ...string) HDRInfo {
	return HDRInfo{Class: DolbyVision, Facts: facts}
}

func transferFact(t string) string {
	switch {
	case t == "":
		return "no transfer characteristics"
	case t == transferPQ:
		return "transfer is PQ (smpte2084)"
	case t == transferHLG:
		return "transfer is HLG (arib-std-b67)"
	case sdrTransfers[t] != "":
		return fmt.Sprintf("transfer is %s (%s)", sdrTransfers[t], t)
	}
	return "transfer is " + t
}

func primariesFact(p string) string {
	switch {
	case p == "":
		return "no colour primaries"
	case p == primaries2020:
		return "primaries are BT.2020 (bt2020)"
	case sdrPrimaries[p] != "":
		return fmt.Sprintf("primaries are %s (%s)", sdrPrimaries[p], p)
	}
	return "primaries are " + p
}

func staticMetadataFact(v VideoStream) string {
	switch {
	case v.Mastering != nil && v.ContentLight != nil:
		return fmt.Sprintf("mastering display metadata present, MaxCLL %d, MaxFALL %d", v.ContentLight.MaxCLL, v.ContentLight.MaxFALL)
	case v.Mastering != nil:
		return "mastering display metadata present, no content light level"
	case v.ContentLight != nil:
		return fmt.Sprintf("no mastering display metadata, MaxCLL %d, MaxFALL %d", v.ContentLight.MaxCLL, v.ContentLight.MaxFALL)
	}
	return "no static HDR metadata"
}

func depthText(bits int) string {
	if bits <= 0 {
		return "unknown bit depth"
	}
	return fmt.Sprintf("%d-bit", bits)
}
