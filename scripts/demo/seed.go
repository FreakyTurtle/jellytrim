package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/timetable"
	"github.com/freakyturtle/jellytrim/internal/web"
)

// ffmpegVersion is what the fake hardware test reports.
const ffmpegVersion = "ffmpeg version 8.1.3-Jellyfin Copyright (c) 2000-2025 the FFmpeg developers"

// fakeCapabilities are the results of a hardware test that never ran:
// software x265 passed, and there is no Intel GPU.
func fakeCapabilities(reg *encoder.Registry, testedAt time.Time) []encoder.Capability {
	var caps []encoder.Capability
	for _, c := range reg.Capabilities() {
		c.FFmpegVersion, c.TestedAt, c.Detail, c.Error = ffmpegVersion, testedAt, "", ""
		switch c.Backend {
		case encoder.NameX265:
			c.Available, c.HDRPassthrough, c.DolbyVisionOption = true, true, true
			c.Detail = "Test encode passed in 2.4 s. HDR10 metadata kept."
		case encoder.NameQSV:
			c.Device = encoder.DefaultQSVDevice
			c.Error = fmt.Sprintf("No Intel GPU device (%s) found.", encoder.DefaultQSVDevice)
		}
		caps = append(caps, c)
	}
	return caps
}

// hardware stands in for the app's hardware tester in the web UI. Test
// again only refreshes the time: the demo never runs ffmpeg.
type hardware struct {
	reg   *encoder.Registry
	store *store.Store
	now   func() time.Time
}

// apply records the fake results in the registry and the database.
func (h *hardware) apply(ctx context.Context) error {
	caps := fakeCapabilities(h.reg, h.now())
	h.reg.SetCapabilities(caps)
	rows := make([]store.CapabilityRow, 0, len(caps))
	for _, c := range caps {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		rows = append(rows, store.CapabilityRow{Backend: c.Backend, Available: c.Available, Detail: string(b), TestedAt: c.TestedAt})
	}
	return h.store.SaveCapabilities(ctx, rows)
}

// Retest implements web.Hardware.
func (h *hardware) Retest(ctx context.Context) error { return h.apply(ctx) }

// SetEncoderPreference does nothing: the demo never encodes.
func (h *hardware) SetEncoderPreference(string) {}

// Capabilities implements web.Hardware, as the app does.
func (h *hardware) Capabilities() []web.Capability {
	var out []web.Capability
	for _, c := range h.reg.Capabilities() {
		out = append(out, web.Capability{
			Backend: c.Backend, Label: c.Label, Codec: c.Codec.Label(), Hardware: c.Hardware, Available: c.Available,
			HDR: c.HDRPassthrough, Device: c.Device, Detail: c.Detail, Error: c.Error, FFmpegVersion: c.FFmpegVersion,
			TestedAt: c.TestedAt, HardwareDecode: c.HWDecode,
		})
	}
	return out
}

// wizardPolicies are the starter policies switched on, as a user might
// choose in the setup wizard. Efficient encoding stays off.
var wizardPolicies = map[string]bool{
	"Protect favourites":      true,
	"Archive watched 4K":      true,
	"Space-saving television": true,
}

// seedConfig does what the setup wizard (and scripts/devseed) do: save the
// connection, manage every library, map Jellyfin's paths to the demo's
// media folder, tick every user and add the starter policies.
func (d *demo) seedConfig(ctx context.Context) error {
	if _, err := d.lib.SaveConnection(ctx, d.jf.URL, d.jf.APIKey()); err != nil {
		return fmt.Errorf("connecting to the fake Jellyfin: %w", err)
	}
	if err := d.lib.RefreshServerInfo(ctx); err != nil {
		return err
	}
	libs, err := d.st.Libraries(ctx)
	if err != nil {
		return err
	}
	var ids, tv []string
	var maps []store.PathMapping
	for _, l := range libs {
		ids = append(ids, l.ID)
		if l.CollectionType == "tvshows" {
			tv = append(tv, l.ID)
		}
	}
	maps = append(maps,
		store.PathMapping{JellyfinPrefix: filmsRoot, LocalPrefix: filepath.ToSlash(filepath.Join(d.dir.media(), "films"))},
		store.PathMapping{JellyfinPrefix: tvRoot, LocalPrefix: filepath.ToSlash(filepath.Join(d.dir.media(), "tv"))})
	if err := d.st.SetManagedLibraries(ctx, ids); err != nil {
		return err
	}
	if err := d.st.ReplacePathMappings(ctx, maps); err != nil {
		return err
	}
	if err := d.selectUsers(ctx); err != nil {
		return err
	}
	if err := d.lib.CreateStarterPolicies(ctx, tv); err != nil {
		return err
	}
	rows, err := d.st.Policies(ctx)
	if err != nil {
		return err
	}
	for _, p := range rows {
		if err := d.st.SetPolicyEnabled(ctx, p.ID, wizardPolicies[p.Name]); err != nil {
			return err
		}
	}
	return d.st.SetSetting(ctx, store.KeySetupComplete, "true")
}

func (d *demo) selectUsers(ctx context.Context) error {
	users, err := d.st.Users(ctx)
	if err != nil {
		return err
	}
	var sel []string
	for _, u := range users {
		if !u.Disabled {
			sel = append(sel, u.ID)
		}
	}
	return d.st.SetSelectedUsers(ctx, sel)
}

// applyMode sets Dry Run and the processing schedule. With -running, Dry
// Run is off and the schedule leaves weekday working hours free, as a
// household sharing the GPU with a home office might; the evenings are
// active, so a demo started then agrees with the job shown encoding.
// Nothing can run either way: the demo never starts the queue's workers
// and has no ffmpeg.
func (d *demo) applyMode(ctx context.Context, running bool) error {
	week := nightsAndWeekends()
	if running {
		week = outsideWorkingHours()
	}
	return d.st.SetSettings(ctx, map[string]string{
		store.KeyDryRun:             fmt.Sprint(!running),
		store.KeyProcessingSchedule: week.String(),
	})
}

// outsideWorkingHours is every hour except Monday to Friday, 08:00 to
// 17:00.
func outsideWorkingHours() timetable.Week {
	week := timetable.Always()
	for day := range 5 { // Monday is day 0
		for hour := 8; hour < 17; hour++ {
			week[day][hour] = false
		}
	}
	return week
}

func nightsAndWeekends() timetable.Week {
	for _, p := range timetable.Presets() {
		if p.ID == "nights-weekends" {
			return p.Week
		}
	}
	return timetable.Always()
}

// Make sure the wizard's policy names still exist, so a renamed starter
// policy fails loudly instead of leaving every policy off.
func checkWizardPolicies() error {
	names := map[string]bool{}
	for _, p := range policy.Starter() {
		names[p.Name] = true
	}
	for n := range wizardPolicies {
		if !names[n] {
			return fmt.Errorf("the starter policies no longer include %q; update wizardPolicies", n)
		}
	}
	return nil
}
