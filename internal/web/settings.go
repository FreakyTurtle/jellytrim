package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/timetable"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// settingsAnchors maps each section's form name to its anchor on the page.
var settingsAnchors = map[string]string{
	"jellyfin":       "jellyfin",
	"libraries":      "libraries",
	"users":          "watched",
	"paths":          "paths",
	"dryrun":         "dry-run",
	"sync":           "sync",
	"schedule-hours": "schedule",
	"processing":     "processing",
	"safety":         "safety",
	"advanced":       "advanced",
}

// settingsSavedURLs is where each section returns after saving. The URLs
// are built here rather than from the request path, so a redirect can only
// go to one of them.
var settingsSavedURLs = func() map[string]string {
	out := make(map[string]string, len(settingsAnchors))
	for section, anchor := range settingsAnchors {
		out[section] = "/settings?saved=" + section + "#" + anchor
	}
	return out
}()

// settingsQualityTiers are the quality tiers, in order, with their labels.
var settingsQualityTiers = []struct{ Key, Label string }{
	{"maximum", "Maximum"}, {"high", "High"}, {"balanced", "Balanced"}, {"space_saver", "Space Saver"},
}

// settingsQualityBackends mirrors the quality maps in internal/encoder
// (settings_test.go keeps them in step), so the web layer shows the
// defaults without depending on encoder types.
var settingsQualityBackends = []struct {
	Backend, Label, Scale string
	Defaults              map[string]int
}{
	{"x265", "Software HEVC", "CRF", map[string]int{"maximum": 18, "high": 21, "balanced": 24, "space_saver": 27}},
	{"qsv-hevc", "Intel Quick Sync HEVC", "ICQ", map[string]int{"maximum": 19, "high": 22, "balanced": 25, "space_saver": 28}},
}

// Limits for numbers typed into Settings.
const (
	settingsMaxInterval = 168
	settingsMaxPercent  = 90
	settingsMaxDays     = 90
	settingsMaxJobs     = 4
	settingsMaxQuality  = 51
)

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.settingsWith(w, r, http.StatusOK, func(v *views.SettingsView) {
		if saved := r.URL.Query().Get("saved"); settingsAnchors[saved] != "" {
			v.Saved = saved
			v.SavedAt = s.Now().Local().Format("15:04")
			v.AdvancedOpen = saved == "advanced"
		}
	})
}

// settingsWith renders the Settings page from what is saved, after edit
// has replaced the parts that show a form as the user sent it.
func (s *Server) settingsWith(w http.ResponseWriter, r *http.Request, status int, edit func(*views.SettingsView)) {
	v, err := s.settingsView(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	edit(&v)
	s.setupRender(w, r, status, views.SettingsPage(v))
}

func (s *Server) settingsView(r *http.Request) (views.SettingsView, error) {
	ctx := r.Context()
	sh, err := s.shell(r, "Settings", "settings")
	if err != nil {
		return views.SettingsView{}, err
	}
	st, err := s.Store.Settings(ctx)
	if err != nil {
		return views.SettingsView{}, err
	}
	v := views.SettingsView{
		Shell:      sh,
		Errors:     map[string]string{},
		Problems:   map[string]views.SetupConnResult{},
		Jellyfin:   views.SetupJellyfinView{URL: setupDisplayURL(st.JellyfinURL), HasKey: st.HasAPIKey},
		ServerName: st.JellyfinServer,
		Hardware:   s.setupHardwareView("/settings/hardware/test"),
		LastSync:   s.settingsLastSync(ctx),
	}
	if err := s.settingsLibraryParts(ctx, &v); err != nil {
		return v, err
	}
	settingsFromStore(&v, st)
	if v.BackupUse, err = s.backupUse(ctx); err != nil {
		return v, err
	}
	sc, err := s.scheduleNow(ctx)
	if err != nil {
		return v, err
	}
	v.Schedule = settingsScheduleView(sc)
	return v, nil
}

// settingsScheduleView builds the Processing schedule section.
func settingsScheduleView(sc scheduleState) views.SettingsScheduleView {
	v := views.SettingsScheduleView{
		Grid: views.ScheduleGridProps{
			Name:    "h",
			Legend:  "Active hours",
			Hint:    "Tick the hours when encoding may run.",
			Days:    scheduleDays(sc.week),
			NowDay:  timetable.Day(sc.now),
			NowHour: sc.now.Hour(),
		},
		Summary: scheduleSummary(sc.week),
		State:   sc.stateText(),
		Lamp:    sc.lamp(),
		Zone:    scheduleZone(sc.now),
		Empty:   sc.week.Empty(),
	}
	for _, p := range timetable.Presets() {
		v.Presets = append(v.Presets, views.SettingsSchedulePreset{ID: p.ID, Label: p.Label, Current: p.Week == sc.week})
	}
	return v
}

func (s *Server) settingsLibraryParts(ctx context.Context, v *views.SettingsView) error {
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return err
	}
	locs, err := s.setupLibraryLocations(ctx)
	if err != nil {
		return err
	}
	rows, err := s.setupSavedPathRows(ctx, locs)
	if err != nil {
		return err
	}
	v.Libraries = setupLibraryViews(libs, func(l store.Library) bool { return l.Managed })
	v.Paths = views.SetupPathsView{Rows: rows}
	if v.Watch, err = s.watchFormSaved(ctx, false); err != nil {
		return err
	}
	v.WatchSummary, v.WatchPeople, err = s.watchPeople(ctx)
	return err
}

// settingsFromStore fills the plain settings.
func settingsFromStore(v *views.SettingsView, st store.Settings) {
	v.DryRun = st.DryRun
	v.KeepDays = st.BackupDays
	v.SyncInterval = strconv.Itoa(st.SyncIntervalHours)
	v.SyncDailyAt = st.SyncDailyAt
	v.AutoProcess = st.AutoProcess
	v.Validation = st.Validation
	v.Concurrency = strconv.Itoa(st.Concurrency)
	v.Encoder = st.EncoderPreference
	v.MinSaving = strconv.Itoa(st.MinSavingPercent)
	v.BackupDays = strconv.Itoa(st.BackupDays)
	v.Preset = st.X265Preset
	v.BitDepth = "10"
	if st.MatchBitDepth {
		v.BitDepth = "source"
	}
	v.Quality = settingsQualityView(settingsParseOverrides(st.QualityOverrides), nil)
}

func (s *Server) settingsLastSync(ctx context.Context) string {
	last, err := s.Store.LastSync(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "never"
	case err != nil:
		return "unknown"
	case last.FinishedAt == nil:
		return "running now"
	case last.Status == "failed":
		return "failed " + units.Ago(*last.FinishedAt, s.Now())
	}
	return units.Ago(*last.FinishedAt, s.Now()) + ", at " + last.FinishedAt.Local().Format("2 Jan 2006 15:04")
}

// settingsParseOverrides reads {"x265":{"high":21}}. Unreadable JSON counts
// as no overrides.
func settingsParseOverrides(raw string) map[string]map[string]int {
	out := map[string]map[string]int{}
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]map[string]int{}
	}
	return out
}

func settingsQualityName(backend, tier string) string {
	return "q-" + backend + "-" + tier
}

// settingsQualityView builds the Advanced quality table from saved
// overrides, or from the form as typed when form is not nil.
func settingsQualityView(saved map[string]map[string]int, form url.Values) []views.SettingsQualityBackend {
	var out []views.SettingsQualityBackend
	for _, b := range settingsQualityBackends {
		row := views.SettingsQualityBackend{Label: b.Label, Backend: b.Backend, Scale: b.Scale}
		for _, t := range settingsQualityTiers {
			name := settingsQualityName(b.Backend, t.Key)
			tier := views.SettingsQualityTier{Name: name, Label: t.Label, Default: strconv.Itoa(b.Defaults[t.Key])}
			if form != nil {
				tier.Value = strings.TrimSpace(form.Get(name))
			} else if n, ok := saved[b.Backend][t.Key]; ok {
				tier.Value = strconv.Itoa(n)
			}
			row.Tiers = append(row.Tiers, tier)
		}
		out = append(out, row)
	}
	return out
}

// saveSettings saves one section. A valid form redirects back to the
// section with "Saved at"; an invalid one shows the page again with the
// errors next to the fields.
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	target, ok := settingsSavedURLs[section]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	v, err := s.settingsView(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var watchBefore watchSaved
	if section == "users" {
		if watchBefore, err = s.watchSavedNow(r.Context()); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	status, err := s.settingsSave(r.Context(), section, r.PostForm, &v)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if status != 0 {
		s.setupRender(w, r, status, views.SettingsPage(v))
		return
	}
	if section == "users" {
		s.watchFollowUp(r.Context(), watchBefore)
	} else {
		s.settingsReevaluate(section)
	}
	redirect(w, r, target)
}

// settingsSave validates and stores one section. It returns 0 when saved,
// or the status to show the page again with (v then holds the errors).
func (s *Server) settingsSave(ctx context.Context, section string, form url.Values, v *views.SettingsView) (int, error) {
	switch section {
	case "jellyfin":
		return s.settingsSaveJellyfin(ctx, form, v)
	case "libraries":
		return s.settingsSaveLibraries(ctx, form, v)
	case "users":
		return s.settingsSaveUsers(ctx, form, v)
	case "paths":
		return s.settingsSavePaths(ctx, form, v)
	case "dryrun":
		return s.settingsSaveDryRun(ctx, form, v)
	case "sync":
		return s.settingsSaveSync(ctx, form, v)
	case "schedule-hours":
		return s.settingsSaveHours(ctx, form, v)
	case "processing":
		return s.settingsSaveProcessing(ctx, form, v)
	case "safety":
		return s.settingsSaveSafety(ctx, form, v)
	}
	return s.settingsSaveAdvanced(ctx, form, v)
}

// settingsReevaluate brings the library up to date after a change that
// affects decisions. Which libraries and folders are read needs a full
// sync; the rest only needs the policies evaluated again. The watched state
// decides for itself (watchFollowUp).
func (s *Server) settingsReevaluate(section string) {
	if s.Library == nil {
		return
	}
	switch section {
	case "libraries", "paths":
		s.Library.RunAsync()
	case "dryrun", "safety", "advanced":
		s.Library.EvaluateAsync()
	}
}

func (s *Server) settingsSaveJellyfin(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	if s.Library == nil {
		v.Problems["jellyfin"] = views.SetupConnResult{Variant: "bad", Title: "The connection cannot be changed right now."}
		return http.StatusServiceUnavailable, nil
	}
	jv, ok := s.setupConnect(ctx, form.Get("url"), form.Get("key"), v.Jellyfin.HasKey)
	if !ok {
		v.Jellyfin = jv
		return http.StatusUnprocessableEntity, nil
	}
	return 0, nil
}

func (s *Server) settingsSaveLibraries(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	c, err := s.setupParseLibraries(ctx, form)
	if err != nil {
		return 0, err
	}
	if c.Error != "" {
		v.Libraries, v.Errors["library"] = c.Views, c.Error
		return http.StatusUnprocessableEntity, nil
	}
	return 0, s.Store.SetManagedLibraries(ctx, c.IDs)
}

func (s *Server) settingsSaveUsers(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	c, err := s.watchParse(ctx, form)
	if err != nil {
		return 0, err
	}
	if c.invalid() {
		v.Watch = c.Form
		return http.StatusUnprocessableEntity, nil
	}
	return 0, s.watchSave(ctx, c)
}

func (s *Server) settingsSavePaths(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	locs, err := s.setupLibraryLocations(ctx)
	if err != nil {
		return 0, err
	}
	rows := setupParsePathRows(form, locs)
	if edited, ok := setupEditRows(rows, form); ok {
		v.Paths = views.SetupPathsView{Rows: edited}
		return http.StatusOK, nil
	}
	ms, pv := setupValidatePaths(rows)
	if pv.Error != "" {
		v.Paths = pv
		return http.StatusUnprocessableEntity, nil
	}
	return 0, s.Store.ReplacePathMappings(ctx, ms)
}

// settingsSaveDryRun turns Dry Run on at once. Turning it off needs
// confirm=yes, which the dialog (or, without JavaScript, the confirmation
// shown in its place) sends.
func (s *Server) settingsSaveDryRun(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	on := form.Get("dry_run") == "on"
	if !on && v.DryRun && form.Get("confirm") != "yes" {
		v.ConfirmDryRunOff = true
		return http.StatusOK, nil
	}
	return 0, s.Store.SetSetting(ctx, store.KeyDryRun, store.FormatBool(on))
}

func (s *Server) settingsSaveSync(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	v.SyncInterval = strings.TrimSpace(form.Get("interval"))
	v.SyncDailyAt = strings.TrimSpace(form.Get("daily_at"))
	settingsCheckInt(v, "interval", v.SyncInterval, 1, settingsMaxInterval, "Enter a whole number of hours from 1 to 168.")
	if v.SyncDailyAt != "" && !settingsIsClock(v.SyncDailyAt) {
		v.Errors["daily_at"] = "Enter a time such as 03:30, or leave it empty."
	}
	if len(v.Errors) > 0 {
		return http.StatusUnprocessableEntity, nil
	}
	return 0, s.Store.SetSettings(ctx, map[string]string{
		store.KeySyncIntervalHours: v.SyncInterval,
		store.KeySyncDailyAt:       v.SyncDailyAt,
	})
}

// settingsSaveHours saves the processing schedule: a preset button, or the
// ticked grid cells. It clears the old daily window, which the schedule
// replaces, and wakes the queue so a change applies at once: a running
// encode stops if its hour is now switched off.
func (s *Server) settingsSaveHours(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	var week timetable.Week
	if id := form.Get("preset"); id != "" {
		p, ok := schedulePreset(id)
		if !ok {
			v.Problems["schedule-hours"] = views.SetupConnResult{Variant: "bad", Title: "Choose one of the presets."}
			return http.StatusUnprocessableEntity, nil
		}
		week = p.Week
	} else {
		w, ok := scheduleParse(form["h"])
		if !ok {
			v.Problems["schedule-hours"] = views.SetupConnResult{Variant: "bad", Title: "Some hours could not be read. Nothing was saved. Choose the hours again and save."}
			return http.StatusUnprocessableEntity, nil
		}
		week = w
	}
	if err := s.Store.SetSettings(ctx, map[string]string{
		store.KeyProcessingSchedule: week.String(),
		store.KeyWindowStart:        "",
		store.KeyWindowEnd:          "",
	}); err != nil {
		return 0, err
	}
	if s.Queue != nil {
		s.Queue.Wake()
	}
	return 0, nil
}

func (s *Server) settingsSaveProcessing(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	v.AutoProcess = form.Get("auto_process") == "on"
	v.Validation = form.Get("validation")
	v.Concurrency = form.Get("concurrency")
	v.Encoder = form.Get("encoder")
	settingsCheckInt(v, "concurrency", v.Concurrency, 1, settingsMaxJobs, "Choose from 1 to 4 jobs at a time.")
	if !setupValid(v.Validation, "full", "sampled") || !setupValid(v.Encoder, "hardware", "software") {
		v.Problems["processing"] = views.SetupConnResult{Variant: "bad", Title: "Choose a check and an encoder."}
	}
	if len(v.Errors) > 0 || len(v.Problems) > 0 {
		return http.StatusUnprocessableEntity, nil
	}
	return 0, s.Store.SetSettings(ctx, map[string]string{
		store.KeyAutoProcess:       store.FormatBool(v.AutoProcess),
		store.KeyValidation:        v.Validation,
		store.KeyConcurrency:       v.Concurrency,
		store.KeyEncoderPreference: v.Encoder,
	})
}

func (s *Server) settingsSaveSafety(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	v.MinSaving = strings.TrimSpace(form.Get("min_saving"))
	v.BackupDays = strings.TrimSpace(form.Get("backup_days"))
	settingsCheckInt(v, "min_saving", v.MinSaving, 0, settingsMaxPercent, "Enter a whole number from 0 to 90.")
	settingsCheckInt(v, "backup_days", v.BackupDays, 0, settingsMaxDays, "Enter a whole number of days from 0 to 90.")
	if len(v.Errors) > 0 {
		return http.StatusUnprocessableEntity, nil
	}
	return 0, s.Store.SetSettings(ctx, map[string]string{
		store.KeyMinSavingPercent: v.MinSaving,
		store.KeyBackupDays:       v.BackupDays,
	})
}

// settingsSaveAdvanced stores the encoder numbers as
// {"x265":{"high":21}}; an empty box uses the default.
func (s *Server) settingsSaveAdvanced(ctx context.Context, form url.Values, v *views.SettingsView) (int, error) {
	if form.Get("reset") != "" {
		return 0, s.Store.SetSettings(ctx, map[string]string{
			store.KeyX265Preset:       "slow",
			store.KeyMatchBitDepth:    "false",
			store.KeyQualityOverrides: "{}",
		})
	}
	v.AdvancedOpen = true
	v.Preset, v.BitDepth = form.Get("preset"), form.Get("bit_depth")
	v.Quality = settingsQualityView(nil, form)
	overrides := map[string]map[string]int{}
	bad := false
	for i, b := range v.Quality {
		for j, t := range b.Tiers {
			if t.Value == "" {
				continue
			}
			n, err := strconv.Atoi(t.Value)
			if err != nil || n < 1 || n > settingsMaxQuality {
				v.Quality[i].Tiers[j].Error = "Enter a whole number from 1 to 51, or leave it empty for the default."
				bad = true
				continue
			}
			if overrides[b.Backend] == nil {
				overrides[b.Backend] = map[string]int{}
			}
			overrides[b.Backend][settingsQualityTiers[j].Key] = n
		}
	}
	if !setupValid(v.Preset, "medium", "slow", "slower") || !setupValid(v.BitDepth, "10", "source") {
		v.Problems["advanced"] = views.SetupConnResult{Variant: "bad", Title: "Choose a speed preset and a bit depth."}
		bad = true
	}
	if bad {
		return http.StatusUnprocessableEntity, nil
	}
	raw, err := json.Marshal(overrides)
	if err != nil {
		return 0, err
	}
	return 0, s.Store.SetSettings(ctx, map[string]string{
		store.KeyX265Preset:       v.Preset,
		store.KeyMatchBitDepth:    store.FormatBool(v.BitDepth == "source"),
		store.KeyQualityOverrides: string(raw),
	})
}

func settingsCheckInt(v *views.SettingsView, name, value string, lo, hi int, msg string) {
	n, err := strconv.Atoi(value)
	if err != nil || n < lo || n > hi {
		v.Errors[name] = msg
	}
}

// settingsIsClock reports whether t is a 24-hour time such as 03:30.
func settingsIsClock(t string) bool {
	_, err := time.Parse("15:04", t)
	return err == nil && len(t) == 5
}

// retestHardware runs the hardware test again from Settings.
func (s *Server) retestHardware(w http.ResponseWriter, r *http.Request) {
	s.setupHardwareRetest(w, r, "settings")
}
