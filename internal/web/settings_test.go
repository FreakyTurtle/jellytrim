package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// newSettingsEnv is a server whose setup is complete and connected to the
// fake Jellyfin.
func newSettingsEnv(t *testing.T) *setupEnv {
	t.Helper()
	e := newSetupEnv(t)
	e.connect()
	if err := e.st.SetSetting(context.Background(), store.KeySetupComplete, "true"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *setupEnv) settings() store.Settings {
	e.t.Helper()
	st, err := e.st.Settings(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func TestSettingsPageRendersWithoutServices(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.SetSetting(context.Background(), store.KeySetupComplete, "true"); err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), httptest.NewRequest("GET", "/settings", nil))
	body, _ := io.ReadAll(res.Body)
	expectStatus(t, res, string(body), http.StatusOK)
	expectContains(t, string(body), `id="dry-run"`, "The hardware test is not available yet", "Encoder settings", "Default 21", `href="#advanced"`)
}

func TestSettingsSaveAndShowSaved(t *testing.T) {
	e := newSettingsEnv(t)
	res, body := e.post("/settings/schedule", url.Values{
		"interval": {"12"}, "daily_at": {"03:30"}, "window": {"on"}, "window_start": {"01:00"}, "window_end": {"06:30"},
	}, false)
	e.expectRedirect(res, body, "/settings?saved=schedule#schedule")
	st := e.settings()
	if st.SyncIntervalHours != 12 || st.SyncDailyAt != "03:30" || st.WindowStart != "01:00" || st.WindowEnd != "06:30" {
		t.Fatalf("schedule not saved: %+v", st)
	}
	res, body = e.get("/settings?saved=schedule", false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Saved at ", `value="12"`)

	res, body = e.post("/settings/processing", url.Values{"validation": {"sampled"}, "concurrency": {"2"}, "encoder": {"software"}}, false)
	e.expectRedirect(res, body, "/settings?saved=processing#processing")
	if st := e.settings(); st.AutoProcess || st.Validation != "sampled" || st.Concurrency != 2 || st.EncoderPreference != "software" {
		t.Fatalf("processing not saved: %+v", st)
	}

	res, body = e.post("/settings/safety", url.Values{"min_saving": {"15"}, "backup_days": {"0"}}, false)
	e.expectRedirect(res, body, "/settings?saved=safety#safety")
	if st := e.settings(); st.MinSavingPercent != 15 || st.BackupDays != 0 {
		t.Fatalf("safety not saved: %+v", st)
	}
	e.checkNoKey()
}

func TestSettingsValidationErrors(t *testing.T) {
	e := newSettingsEnv(t)
	before := e.settings()
	cases := []struct {
		section string
		form    url.Values
		want    string
	}{
		{"schedule", url.Values{"interval": {"0"}}, "Enter a whole number of hours from 1 to 168."},
		{"schedule", url.Values{"interval": {"6"}, "daily_at": {"25:00"}}, "Enter a time such as 03:30"},
		{"schedule", url.Values{"interval": {"6"}, "window": {"on"}, "window_start": {"02:00"}, "window_end": {"02:00"}}, "must end at a different time"},
		{"processing", url.Values{"validation": {"full"}, "concurrency": {"5"}, "encoder": {"hardware"}}, "Choose from 1 to 4 jobs"},
		{"safety", url.Values{"min_saving": {"95"}, "backup_days": {"7"}}, "Enter a whole number from 0 to 90."},
		{"safety", url.Values{"min_saving": {"10"}, "backup_days": {"-1"}}, "Enter a whole number of days from 0 to 90."},
		{"advanced", url.Values{"preset": {"slow"}, "bit_depth": {"10"}, "q-x265-high": {"60"}}, "Enter a whole number from 1 to 51"},
		{"advanced", url.Values{"preset": {"placebo"}, "bit_depth": {"10"}}, "Choose a speed preset"},
		{"libraries", url.Values{}, "Choose at least one library."},
		{"users", url.Values{"watch_mode": {"any"}}, "Choose at least one person"},
		{"jellyfin", url.Values{"url": {"ftp://jellyfin"}}, "Enter an address starting with http://"},
	}
	for _, c := range cases {
		res, body := e.post("/settings/"+c.section, c.form, false)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s %v: status %d, want 422", c.section, c.form, res.StatusCode)
			continue
		}
		expectContains(t, body, c.want)
		if !strings.Contains(body, `class="wordmark"`) {
			t.Errorf("%s: the whole page should come back", c.section)
		}
	}
	after := e.settings()
	if after != before {
		t.Fatalf("an invalid form changed the settings:\n%+v\n%+v", before, after)
	}
	e.checkNoKey()
}

func TestSettingsDryRunOffNeedsConfirmation(t *testing.T) {
	e := newSettingsEnv(t)
	// An unticked toggle sends nothing: that means off, which must be confirmed.
	res, body := e.post("/settings/dryrun", url.Values{}, false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Turn off Dry Run?", `name="confirm" value="yes"`, "Originals are kept as backups for 7 days.")
	if !e.settings().DryRun {
		t.Fatal("Dry Run turned off without confirmation")
	}
	res, body = e.post("/settings/dryrun", url.Values{"dry_run": {"off"}, "confirm": {"yes"}}, false)
	e.expectRedirect(res, body, "/settings?saved=dryrun#dry-run")
	if e.settings().DryRun {
		t.Fatal("Dry Run still on after confirming")
	}
	e.waitIdle()
	// Turning it back on needs no confirmation.
	res, body = e.post("/settings/dryrun", url.Values{"dry_run": {"on"}}, false)
	e.expectRedirect(res, body, "/settings?saved=dryrun#dry-run")
	if !e.settings().DryRun {
		t.Fatal("Dry Run not back on")
	}
}

func TestSettingsDryRunDialogWhenOn(t *testing.T) {
	e := newSettingsEnv(t)
	_, body := e.get("/settings", false)
	expectContains(t, body, `data-dialog-open="settings-dryrun-dialog"`, `<dialog class="dialog" id="settings-dryrun-dialog"`, "Turn off Dry Run")
}

func TestSettingsAdvancedOverrides(t *testing.T) {
	e := newSettingsEnv(t)
	res, body := e.post("/settings/advanced", url.Values{"preset": {"slower"}, "bit_depth": {"source"}, "q-x265-high": {"20"}, "q-qsv-hevc-space_saver": {"30"}, "q-x265-maximum": {""}}, false)
	e.expectRedirect(res, body, "/settings?saved=advanced#advanced")
	st := e.settings()
	if st.X265Preset != "slower" || !st.MatchBitDepth || st.QualityOverrides != `{"qsv-hevc":{"space_saver":30},"x265":{"high":20}}` {
		t.Fatalf("advanced not saved: %+v", st)
	}
	_, body = e.get("/settings?saved=advanced", false)
	expectContains(t, body, `name="q-x265-high"`, `value="20"`, "<details class=\"settings-advanced\" open")

	res, body = e.post("/settings/advanced", url.Values{"reset": {"1"}}, false)
	e.expectRedirect(res, body, "/settings?saved=advanced#advanced")
	if st := e.settings(); st.X265Preset != "slow" || st.MatchBitDepth || st.QualityOverrides != "{}" {
		t.Fatalf("not reset: %+v", st)
	}
}

func TestSettingsQualityDefaultsMatchEncoders(t *testing.T) {
	backends := map[string]interface {
		QualityValue(string, encoder.Overrides) int
	}{encoder.NameX265: encoder.X265{}, encoder.NameQSV: encoder.QSV{}}
	for _, b := range settingsQualityBackends {
		enc, ok := backends[b.Backend]
		if !ok {
			t.Fatalf("unknown backend %s", b.Backend)
		}
		for _, tier := range settingsQualityTiers {
			if got, want := b.Defaults[tier.Key], enc.QualityValue(tier.Key, encoder.Overrides{}); got != want {
				t.Errorf("%s %s: settings shows %d, encoder uses %d", b.Backend, tier.Key, got, want)
			}
		}
	}
}

func TestSettingsJellyfinKeepsKey(t *testing.T) {
	e := newSettingsEnv(t)
	_, body := e.get("/settings", false)
	expectContains(t, body, "An API key is saved", "Connected to <span class=\"mono\">jellytrim-dev</span>")
	res, body := e.post("/settings/jellyfin", url.Values{"url": {e.jf.URL + "/"}}, false)
	e.expectRedirect(res, body, "/settings?saved=jellyfin#jellyfin")
	key, _ := e.st.JellyfinAPIKey(context.Background())
	if key != e.jf.APIKey() {
		t.Fatal("saving without a key replaced the saved key")
	}
	// The no-JavaScript test shows the Settings page with the result.
	res, body = e.post("/setup/jellyfin/test", url.Values{"url": {e.jf.URL}, "from": {"settings"}}, false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Connected to Jellyfin 12.1.0", `id="dry-run"`)
	e.checkNoKey()
}

func TestSettingsLibrariesUsersAndPaths(t *testing.T) {
	e := newSettingsEnv(t)
	ctx := context.Background()
	libs, _ := e.st.Libraries(ctx)
	users, _ := e.st.Users(ctx)
	res, body := e.post("/settings/libraries", url.Values{"library": {libs[0].ID}}, false)
	e.expectRedirect(res, body, "/settings?saved=libraries#libraries")
	e.waitIdle()
	res, body = e.post("/settings/users", url.Values{"user": {users[0].ID}, "watch_mode": {"all"}}, false)
	e.expectRedirect(res, body, "/settings?saved=users#watched")
	e.waitIdle()
	if sel, _ := e.st.SelectedUsers(ctx); len(sel) != 1 || e.settings().WatchMode != "all" {
		t.Fatalf("users not saved: %v", sel)
	}
	form := e.mappingForm()
	form.Set("add", "1")
	res, body = e.post("/settings/paths", form, false)
	expectStatus(t, res, body, http.StatusOK)
	if n := strings.Count(body, `name="local"`); n != 3 {
		t.Fatalf("%d rows after adding, want 3", n)
	}
	res, body = e.post("/settings/paths", e.mappingForm(), false)
	e.expectRedirect(res, body, "/settings?saved=paths#paths")
	e.waitIdle()
	if ms, _ := e.st.PathMappings(ctx); len(ms) != 2 {
		t.Fatalf("mappings %+v", ms)
	}
}

func TestSettingsUnknownSection(t *testing.T) {
	e := newSettingsEnv(t)
	res, body := e.post("/settings/nope", url.Values{}, false)
	expectStatus(t, res, body, http.StatusNotFound)
}

type settingsFakeHardware struct{ retests atomic.Int32 }

func (h *settingsFakeHardware) Capabilities() []Capability {
	return []Capability{
		{Backend: "x265", Label: "Software", Codec: "hevc", Available: true, HDR: true, FFmpegVersion: "7.1", TestedAt: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)},
		{Backend: "qsv-hevc", Label: "Intel Quick Sync", Codec: "hevc", Device: "/dev/dri/renderD128", Error: "No /dev/dri device in the container"},
	}
}

func (h *settingsFakeHardware) Retest(context.Context) error {
	h.retests.Add(1)
	return nil
}

func TestSettingsHardwareTestAgain(t *testing.T) {
	e := newSettingsEnv(t)
	hw := &settingsFakeHardware{}
	e.s.Hardware = hw
	_, body := e.get("/settings", false)
	expectContains(t, body, "Works", "Failed", "No /dev/dri device in the container", "HDR metadata kept", "7.1")
	res, body := e.post("/settings/hardware/test", nil, true)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, `id="hardware-body"`, "Works")
	if strings.Contains(body, "<html") || hw.retests.Load() != 1 {
		t.Fatalf("want a fragment after one re-test, got %d re-tests", hw.retests.Load())
	}
}
