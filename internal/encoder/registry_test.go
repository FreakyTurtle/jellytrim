package encoder

import (
	"strings"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/media"
)

func caps(x265, x265HDR, qsv, qsvHDR bool) []Capability {
	return []Capability{
		{Backend: NameX265, Available: x265, HDRPassthrough: x265HDR},
		{Backend: NameQSV, Available: qsv, HDRPassthrough: qsvHDR},
	}
}

func TestSelect(t *testing.T) {
	cases := []struct {
		name      string
		caps      []Capability
		pref      string
		codec     media.Codec
		hdr       bool
		requested string
		want      string
		why       string
	}{
		{"auto prefers hardware", caps(true, true, true, true), PreferHardware, media.CodecHEVC, false, Auto, NameQSV, ""},
		{"empty request is auto", caps(true, true, true, true), PreferHardware, media.CodecHEVC, false, "", NameQSV, ""},
		{"auto can prefer software", caps(true, true, true, true), SoftwareOnly, media.CodecHEVC, false, Auto, NameX265, ""},
		{"auto skips failed hardware", caps(true, true, false, false), PreferHardware, media.CodecHEVC, false, Auto, NameX265, ""},
		{"HDR goes to x265 when QSV drops metadata", caps(true, true, true, false), PreferHardware, media.CodecHEVC, true, Auto, NameX265, ""},
		{"no HDR-safe encoder", caps(true, false, true, false), PreferHardware, media.CodecHEVC, true, Auto, "", "keeps HDR metadata"},
		{"nothing passed", caps(false, false, false, false), PreferHardware, media.CodecHEVC, false, Auto, "", "No HEVC encoder passed"},
		{"no encoder for the codec", caps(true, true, true, true), PreferHardware, media.CodecAV1, false, Auto, "", "no encoder for AV1"},
		{"explicit x265", caps(true, true, true, true), PreferHardware, media.CodecHEVC, false, NameX265, NameX265, ""},
		{"explicit QSV that failed", caps(true, true, false, false), PreferHardware, media.CodecHEVC, false, NameQSV, "", "Intel Quick Sync was requested but did not pass the hardware test."},
		{"explicit x265 that failed", caps(false, false, true, true), PreferHardware, media.CodecHEVC, false, NameX265, "", "Software x265 was requested but did not pass its test."},
		{"explicit QSV without HDR", caps(true, true, true, false), PreferHardware, media.CodecHEVC, true, NameQSV, "", "did not prove that it keeps HDR metadata"},
		{"explicit backend for another codec", caps(true, true, true, true), PreferHardware, media.CodecAV1, false, NameX265, "", "cannot produce AV1"},
		{"software only never falls back to hardware", caps(false, false, true, true), SoftwareOnly, media.CodecHEVC, false, Auto, "", "Software only is on in Settings, and no software HEVC encoder passed its test"},
		{"hardware only uses QSV", caps(true, true, true, true), HardwareOnly, media.CodecHEVC, false, Auto, NameQSV, ""},
		{"hardware only never falls back to x265", caps(true, true, false, false), HardwareOnly, media.CodecHEVC, false, Auto, "", "Hardware only is on in Settings, and no hardware HEVC encoder passed its test"},
		{"hardware only skips HDR QSV cannot keep", caps(true, true, true, false), HardwareOnly, media.CodecHEVC, true, Auto, "", "no hardware HEVC encoder on this machine proved that it keeps HDR metadata"},
		{"hardware only keeps HDR on QSV that proved it", caps(true, true, true, true), HardwareOnly, media.CodecHEVC, true, Auto, NameQSV, ""},
		{"hardware only refuses a request for x265", caps(true, true, true, true), HardwareOnly, media.CodecHEVC, true, NameX265, "", "This needs Software x265, but Hardware only is on in Settings."},
		{"hardware only has no H.264 hardware encoder", caps(true, true, true, true), HardwareOnly, media.CodecH264, false, Auto, "", "no hardware encoder for H.264"},
		{"unknown backend", caps(true, true, true, true), PreferHardware, media.CodecHEVC, false, "nvenc", "", "does not know it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(DefaultBackends(""))
			r.SetCapabilities(tc.caps)
			r.SetPreference(tc.pref)
			name, ok, why := r.Select(tc.codec, tc.hdr, tc.requested)
			if name != tc.want || ok != (tc.want != "") {
				t.Errorf("Select = %q %v %q, want %q", name, ok, why, tc.want)
			}
			if tc.why != "" && !strings.Contains(why, tc.why) {
				t.Errorf("why = %q, want it to contain %q", why, tc.why)
			}
		})
	}
}

func TestRegistryBeforeDetection(t *testing.T) {
	r := NewRegistry(DefaultBackends(""))
	if _, ok, why := r.Select(media.CodecHEVC, false, Auto); ok || why == "" {
		t.Errorf("an untested registry selected an encoder (%q)", why)
	}
	cs := r.Capabilities()
	if len(cs) != 2 || cs[0].Backend != NameQSV || cs[1].Backend != NameX265 || cs[0].Error == "" {
		t.Errorf("capabilities = %+v", cs)
	}
}

func TestRegistryBackendCarriesCapability(t *testing.T) {
	r := NewRegistry(DefaultBackends("/dev/dri/renderD129"))
	r.SetCapabilities([]Capability{{Backend: NameQSV, Available: true, HWDecode: []string{"hevc/10"}}, {Backend: "gone"}})
	b, ok := r.Backend(NameQSV)
	if !ok || !b.Capability().Available || b.(QSV).Device != "/dev/dri/renderD129" {
		t.Errorf("backend = %+v", b)
	}
	if _, ok := r.Backend("gone"); ok {
		t.Error("unknown backend found")
	}
}
