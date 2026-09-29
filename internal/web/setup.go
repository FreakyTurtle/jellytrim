package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pathmap"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// setupSteps are the wizard's steps in order, as they appear in the URL.
var setupSteps = []string{"welcome", "jellyfin", "libraries", "paths", "hardware", "defaults", "policies", "scan"}

// setupTVPolicy is the starter policy that asks which show libraries it covers.
const setupTVPolicy = "Space-saving television"

var errSetupNoLibrary = errors.New("setup: the library service is not configured")

func setupStepNumber(name string) int {
	return slices.Index(setupSteps, name) + 1
}

// setupRender writes a page with a status other than 200, such as 422 when a
// form comes back with errors.
func (s *Server) setupRender(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		s.Log.Error("http: render", "path", r.URL.Path, "err", err)
	}
}

func setupIsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// setupStart sends the user to the first step that still needs doing, so a
// refresh or a return visit resumes where they left off.
func (s *Server) setupStart(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if st.SetupComplete {
		redirect(w, r, "/")
		return
	}
	step, err := s.setupResume(r.Context(), st)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/setup/"+step)
}

func (s *Server) setupResume(ctx context.Context, st store.Settings) (string, error) {
	if st.JellyfinURL == "" && !st.HasAPIKey {
		return "welcome", nil
	}
	if st.JellyfinURL == "" || !st.HasAPIKey {
		return "jellyfin", nil
	}
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(libs, func(l store.Library) bool { return l.Managed }) {
		return "libraries", nil
	}
	ms, err := s.Store.PathMappings(ctx)
	if err != nil {
		return "", err
	}
	if len(ms) == 0 {
		return "paths", nil
	}
	ps, err := s.Store.Policies(ctx)
	if err != nil {
		return "", err
	}
	if len(ps) == 0 {
		return "hardware", nil
	}
	return "scan", nil
}

// setupGate loads the settings for a step request. It answers the request
// itself (and returns false) for an unknown step, or once setup is complete
// for every step but the scan, which stays reachable to watch it finish.
func (s *Server) setupGate(w http.ResponseWriter, r *http.Request) (string, store.Settings, bool) {
	step := r.PathValue("step")
	if setupStepNumber(step) == 0 {
		http.NotFound(w, r)
		return "", store.Settings{}, false
	}
	if s.Library == nil {
		s.serverError(w, r, errSetupNoLibrary)
		return "", store.Settings{}, false
	}
	st, err := s.Store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return "", store.Settings{}, false
	}
	if st.SetupComplete && step != "scan" {
		redirect(w, r, "/")
		return "", store.Settings{}, false
	}
	return step, st, true
}

func (s *Server) setupStep(w http.ResponseWriter, r *http.Request) {
	step, st, ok := s.setupGate(w, r)
	if !ok {
		return
	}
	c, err := s.setupStepPage(r, step, st)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, c)
}

// setupStepPage builds a step's page from what is saved.
func (s *Server) setupStepPage(r *http.Request, step string, st store.Settings) (templ.Component, error) {
	ctx := r.Context()
	av := assetVersion()
	switch step {
	case "jellyfin":
		return views.SetupJellyfin(views.SetupJellyfinView{URL: setupDisplayURL(st.JellyfinURL), HasKey: st.HasAPIKey}, av), nil
	case "libraries":
		v, err := s.setupLibrariesView(ctx)
		return views.SetupLibraries(v, av), err
	case "paths":
		v, err := s.setupPathsView(ctx)
		return views.SetupPaths(v, av), err
	case "hardware":
		return views.SetupHardware(s.setupHardwareView("/setup/hardware"), av), nil
	case "defaults":
		return views.SetupDefaults(views.SetupDefaultsView{Quality: st.DefaultQuality, Codec: st.DefaultCodec, Encoder: st.EncoderPreference}, av), nil
	case "policies":
		v, err := s.setupPoliciesView(ctx, nil, nil)
		return views.SetupPolicies(v, av), err
	case "scan":
		v, err := s.setupScanView(ctx, st)
		if setupIsHTMX(r) {
			return views.SetupScanStatus(v), err
		}
		return views.SetupScan(v, av), err
	}
	return views.SetupWelcome(av), nil
}

func (s *Server) setupSubmit(w http.ResponseWriter, r *http.Request) {
	step, st, ok := s.setupGate(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	switch step {
	case "welcome":
		redirect(w, r, "/setup/jellyfin")
	case "jellyfin":
		s.setupSaveJellyfin(w, r, st)
	case "libraries":
		s.setupSaveLibraries(w, r)
	case "paths":
		s.setupSavePaths(w, r)
	case "hardware":
		if r.PostForm.Get("action") == "retest" {
			s.setupHardwareRetest(w, r, "setup")
			return
		}
		redirect(w, r, "/setup/defaults")
	case "defaults":
		s.setupSaveDefaults(w, r)
	case "policies":
		s.setupSavePolicies(w, r)
	case "scan":
		s.setupStartScan(w, r)
	}
}

// ---------- Jellyfin ----------

func (s *Server) setupSaveJellyfin(w http.ResponseWriter, r *http.Request, st store.Settings) {
	v, ok := s.setupConnect(r.Context(), r.PostForm.Get("url"), r.PostForm.Get("key"), st.HasAPIKey)
	if !ok {
		s.setupRender(w, r, http.StatusUnprocessableEntity, views.SetupJellyfin(v, assetVersion()))
		return
	}
	redirect(w, r, "/setup/libraries")
}

// testConnection tests the address and key without saving them. An empty
// key tests the saved one. Without JavaScript it shows the whole page again.
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	st, err := s.Store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := s.setupTestConn(r.Context(), r.PostForm.Get("url"), r.PostForm.Get("key"), st.HasAPIKey)
	switch {
	case setupIsHTMX(r):
		res := *v.Result
		s.render(w, r, views.SetupConnResultView(res))
	case r.PostForm.Get("from") == "settings":
		s.settingsWith(w, r, http.StatusOK, func(sv *views.SettingsView) { sv.Jellyfin = v })
	default:
		s.render(w, r, views.SetupJellyfin(v, assetVersion()))
	}
}

// ---------- Libraries and watched state ----------

func (s *Server) setupLibrariesView(ctx context.Context) (views.SetupLibrariesView, error) {
	var v views.SetupLibrariesView
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return v, err
	}
	if len(libs) == 0 {
		// A connection set through the environment has not read the
		// libraries yet.
		if err := s.Library.RefreshServerInfo(ctx); err != nil {
			res := setupConnError(err, "", "")
			v.LoadError = &res
		} else if libs, err = s.Store.Libraries(ctx); err != nil {
			return v, err
		}
	}
	// Until the user chooses, every film and show library is ticked.
	anyManaged := slices.ContainsFunc(libs, func(l store.Library) bool { return l.Managed })
	v.Libraries = setupLibraryViews(libs, func(l store.Library) bool { return l.Managed || !anyManaged })
	v.Watch, err = s.watchFormSaved(ctx, true)
	return v, err
}

func (s *Server) setupSaveLibraries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	libChoice, err := s.setupParseLibraries(ctx, r.PostForm)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	watch, err := s.watchParse(ctx, r.PostForm)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if libChoice.Error != "" || watch.invalid() {
		v := views.SetupLibrariesView{Libraries: libChoice.Views, LibraryError: libChoice.Error, Watch: watch.Form}
		s.setupRender(w, r, http.StatusUnprocessableEntity, views.SetupLibraries(v, assetVersion()))
		return
	}
	if err := s.setupSaveLibraryChoice(ctx, libChoice, watch); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/setup/paths")
}

func (s *Server) setupSaveLibraryChoice(ctx context.Context, libs setupLibraryChoice, watch watchChoice) error {
	if err := s.Store.SetManagedLibraries(ctx, libs.IDs); err != nil {
		return err
	}
	return s.watchSave(ctx, watch)
}

// ---------- Path mappings ----------

func (s *Server) setupPathsView(ctx context.Context) (views.SetupPathsView, error) {
	locs, err := s.setupLibraryLocations(ctx)
	if err != nil {
		return views.SetupPathsView{}, err
	}
	rows, err := s.setupSavedPathRows(ctx, locs)
	return views.SetupPathsView{Rows: rows}, err
}

func (s *Server) setupSavePaths(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locs, err := s.setupLibraryLocations(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	rows := setupParsePathRows(r.PostForm, locs)
	if edited, ok := setupEditRows(rows, r.PostForm); ok {
		s.render(w, r, views.SetupPaths(views.SetupPathsView{Rows: edited}, assetVersion()))
		return
	}
	ms, v := setupValidatePaths(rows)
	if v.Error != "" {
		s.setupRender(w, r, http.StatusUnprocessableEntity, views.SetupPaths(v, assetVersion()))
		return
	}
	if err := s.Store.ReplacePathMappings(ctx, ms); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/setup/hardware")
}

// checkPaths checks the mappings as entered, without saving them.
func (s *Server) checkPaths(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	locs, err := s.setupLibraryLocations(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := s.setupRunPathCheck(r.Context(), setupParsePathRows(r.PostForm, locs))
	switch {
	case setupIsHTMX(r):
		s.render(w, r, views.SetupPathResults(v))
	case r.PostForm.Get("from") == "settings":
		s.settingsWith(w, r, http.StatusOK, func(sv *views.SettingsView) { sv.Paths = v })
	default:
		s.render(w, r, views.SetupPaths(v, assetVersion()))
	}
}

// ---------- Defaults ----------

func (s *Server) setupSaveDefaults(w http.ResponseWriter, r *http.Request) {
	v := views.SetupDefaultsView{Quality: r.PostForm.Get("quality"), Codec: r.PostForm.Get("codec"), Encoder: r.PostForm.Get("encoder")}
	if !setupValid(v.Quality, "maximum", "high", "balanced", "space_saver") ||
		!setupValid(v.Codec, "hevc", "h264") || !setupValid(v.Encoder, "hardware", "software") {
		v.Error = "Choose a quality, a codec and an encoder."
		s.setupRender(w, r, http.StatusUnprocessableEntity, views.SetupDefaults(v, assetVersion()))
		return
	}
	err := s.Store.SetSettings(r.Context(), map[string]string{
		store.KeyDefaultQuality:    v.Quality,
		store.KeyDefaultCodec:      v.Codec,
		store.KeyEncoderPreference: v.Encoder,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/setup/policies")
}

func setupValid(v string, allowed ...string) bool {
	return slices.Contains(allowed, v)
}

// ---------- Starter policies ----------

// setupNames resolves library IDs to names for policy sentences.
type setupNames map[string]string

func (n setupNames) Library(id string) string {
	if name, ok := n[id]; ok {
		return name
	}
	return "a library that no longer exists"
}

func (setupNames) Series(id string) string     { return id }
func (setupNames) Collection(id string) string { return id }

// setupPoliciesView shows the starter policies. enabled and tv are the
// user's choices when re-rendering; nil means use what is saved, or the
// defaults.
func (s *Server) setupPoliciesView(ctx context.Context, enabled map[string]bool, tv []string) (views.SetupPoliciesView, error) {
	var v views.SetupPoliciesView
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return v, err
	}
	names := setupNames{}
	var tvLibs []store.Library
	for _, l := range libs {
		names[l.ID] = l.Name
		if l.Managed && l.CollectionType == "tvshows" {
			tvLibs = append(tvLibs, l)
		}
	}
	existing, err := s.Library.Policies(ctx)
	if err != nil {
		return v, err
	}
	v.Existing = len(existing) > 0
	if enabled == nil {
		enabled, tv = setupSavedChoices(existing, tvLibs)
	}
	for _, p := range policy.Starter() {
		if p.Name == setupTVPolicy {
			p.Scope.Libraries = tv
		}
		v.Cards = append(v.Cards, views.SetupPolicyCard{
			Name: p.Name, Sentence: policy.Describe(p, names), Enabled: enabled[p.Name], TV: p.Name == setupTVPolicy,
		})
	}
	for _, l := range tvLibs {
		v.TVLibraries = append(v.TVLibraries, views.SetupLibrary{ID: l.ID, Name: l.Name, Checked: slices.Contains(tv, l.ID)})
	}
	return v, nil
}

// setupSavedChoices reads which starter policies are on and which show
// libraries the television policy covers. With no policies yet, only
// Protect favourites is on and every managed show library is chosen.
func setupSavedChoices(existing []policy.Policy, tvLibs []store.Library) (map[string]bool, []string) {
	enabled := map[string]bool{}
	var tv []string
	if len(existing) == 0 {
		for _, p := range policy.Starter() {
			enabled[p.Name] = p.Enabled
		}
		for _, l := range tvLibs {
			tv = append(tv, l.ID)
		}
		return enabled, tv
	}
	for _, p := range existing {
		enabled[p.Name] = p.Enabled
		if p.Name == setupTVPolicy {
			tv = p.Scope.Libraries
		}
	}
	return enabled, tv
}

func (s *Server) setupSavePolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Only managed show libraries can be chosen; other IDs are ignored.
	var tv []string
	for _, l := range libs {
		if l.Managed && l.CollectionType == "tvshows" && slices.Contains(r.PostForm["tv"], l.ID) {
			tv = append(tv, l.ID)
		}
	}
	enabled := map[string]bool{}
	for _, name := range r.PostForm["enabled"] {
		enabled[name] = true
	}
	if err := s.setupApplyStarters(ctx, enabled, tv); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/setup/scan")
}

// applyStarterPolicies creates the starter policies if there are none, then
// switches each on or off as chosen. Policies are matched by name.
func (s *Server) setupApplyStarters(ctx context.Context, enabled map[string]bool, tv []string) error {
	before, err := s.Store.Policies(ctx)
	if err != nil {
		return err
	}
	if err := s.Library.CreateStarterPolicies(ctx, tv); err != nil {
		return err
	}
	ps, err := s.Library.Policies(ctx)
	if err != nil {
		return err
	}
	starters := map[string]bool{}
	for _, p := range policy.Starter() {
		starters[p.Name] = true
	}
	for _, p := range ps {
		if !starters[p.Name] {
			continue
		}
		on := enabled[p.Name]
		if p.Name == setupTVPolicy && len(before) > 0 {
			// Created on an earlier visit: the library choice may have changed.
			p.Scope.Libraries, p.Enabled = tv, on
			if _, err := s.Library.SavePolicy(ctx, p); err != nil {
				return err
			}
			continue
		}
		if p.Enabled != on {
			if err := s.Store.SetPolicyEnabled(ctx, p.ID, on); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------- Dry Run scan ----------

// setupStartScan marks setup complete and starts the first sync in the
// background. The scan page then polls for progress.
func (s *Server) setupStartScan(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.SetSetting(r.Context(), store.KeySetupComplete, "true"); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.Library.RunAsync()
	redirect(w, r, "/setup/scan")
}

// setupScanView describes the scan: not started, running, failed or done.
func (s *Server) setupScanView(ctx context.Context, st store.Settings) (views.SetupScanView, error) {
	status := s.Library.Status()
	if status.Running {
		return setupRunningView(status.Phase, status.Done, status.Total), nil
	}
	if !st.SetupComplete {
		return views.SetupScanView{State: "ready"}, nil
	}
	if status.LastError != "" {
		return views.SetupScanView{State: "failed", Error: status.LastError}, nil
	}
	last, err := s.Store.LastSync(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return views.SetupScanView{State: "ready"}, nil
	case err != nil:
		return views.SetupScanView{}, err
	case last.Status == "failed":
		return views.SetupScanView{State: "failed", Error: last.Error}, nil
	}
	sum, err := s.Library.DryRun(ctx)
	if err != nil {
		return views.SetupScanView{}, err
	}
	summary := setupSummary(sum)
	return views.SetupScanView{State: "done", Summary: &summary}, nil
}

func setupRunningView(phase string, done, total int) views.SetupScanView {
	v := views.SetupScanView{State: "running", Phase: phase, Percent: -1}
	if v.Phase == "" {
		v.Phase = "Connecting to Jellyfin"
	}
	switch {
	case total > 0:
		v.Percent = done * 100 / total
		v.Readout = setupCount(done) + " of " + setupCount(total)
	case done > 0:
		v.Readout = setupCount(done) + " so far"
	}
	return v
}

// setupDisplayURL is the saved address without anything that could carry
// a secret: no user name, password, query or fragment.
func setupDisplayURL(raw string) string {
	u, err := setupParseURL(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.ForceQuery, u.Fragment = nil, "", false, ""
	return strings.TrimSpace(u.String())
}

func setupParseURL(raw string) (*url.URL, error) {
	return url.Parse(strings.TrimSpace(raw))
}

// ---------- Shared with Settings: connection ----------

// setupCheckURL returns a plain message if the address cannot be a Jellyfin
// server address.
func setupCheckURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "Enter the address of your Jellyfin server."
	}
	u, err := setupParseURL(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return "Enter an address starting with http:// or https://, for example http://jellyfin:8096."
	case u.User != nil:
		return "Leave the user name and password out of the address. JellyTrim uses the API key."
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "Enter just the server address, without a ? or # part."
	}
	return ""
}

func setupCheckKey(key string, hasKey bool) string {
	switch {
	case key == "" && !hasKey:
		return "Enter an API key."
	case strings.ContainsFunc(key, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return "The key contains spaces. Paste it again without them."
	}
	return ""
}

// setupConnect validates, tests and saves the connection, then reads the
// libraries and users. On failure the view carries the errors to show.
func (s *Server) setupConnect(ctx context.Context, rawURL, key string, hasKey bool) (views.SetupJellyfinView, bool) {
	addr := strings.TrimSpace(rawURL)
	key = strings.TrimSpace(key)
	v := views.SetupJellyfinView{URL: setupDisplayURL(addr), HasKey: hasKey, URLError: setupCheckURL(addr), KeyError: setupCheckKey(key, hasKey)}
	if v.URLError != "" || v.KeyError != "" {
		return v, false
	}
	if _, err := s.Library.SaveConnection(ctx, addr, key); err != nil {
		res := setupConnError(err, addr, key)
		v.Result = &res
		return v, false
	}
	v.HasKey = true
	if err := s.Library.RefreshServerInfo(ctx); err != nil {
		res := setupConnError(err, addr, key)
		res.Title = "Connected, but JellyTrim could not read the libraries and users"
		v.Result = &res
		return v, false
	}
	return v, true
}

// setupTestConn tests without saving. An empty key tests the saved key.
// The result is always set.
func (s *Server) setupTestConn(ctx context.Context, rawURL, key string, hasKey bool) views.SetupJellyfinView {
	addr := strings.TrimSpace(rawURL)
	key = strings.TrimSpace(key)
	v := views.SetupJellyfinView{URL: setupDisplayURL(addr), HasKey: hasKey, URLError: setupCheckURL(addr), KeyError: setupCheckKey(key, hasKey)}
	if v.URLError != "" || v.KeyError != "" {
		msg := v.URLError
		if msg == "" {
			msg = v.KeyError
		}
		v.Result = &views.SetupConnResult{Variant: "bad", Title: msg}
		return v
	}
	if s.Library == nil {
		v.Result = &views.SetupConnResult{Variant: "bad", Title: "The connection test is not available."}
		return v
	}
	if key == "" {
		saved, err := s.Store.JellyfinAPIKey(ctx)
		if err != nil {
			v.Result = &views.SetupConnResult{Variant: "bad", Title: "JellyTrim could not read the saved API key."}
			return v
		}
		key = saved
	}
	info, err := s.Library.TestConnection(ctx, addr, key)
	if err != nil {
		res := setupConnError(err, addr, key)
		v.Result = &res
		return v
	}
	v.Result = &views.SetupConnResult{Variant: "ok", Title: "Connected to Jellyfin " + info.Version + " on " + info.ServerName + "."}
	return v
}

// setupConnError turns a Jellyfin error into what happened and what to do.
// The key is scrubbed from the technical details in case anything echoed it.
func setupConnError(err error, addr, key string) views.SetupConnResult {
	detail := err.Error()
	if key != "" {
		detail = strings.ReplaceAll(detail, key, "[API key]")
	}
	res := views.SetupConnResult{Variant: "bad", Detail: detail}
	var se *jellyfin.StatusError
	var ue *jellyfin.UnavailableError
	if addr == "" {
		addr = "Jellyfin"
	}
	switch {
	case errors.Is(err, library.ErrNotConfigured):
		res.Title, res.Text, res.Detail = "Jellyfin is not connected yet", "Go back and connect Jellyfin first.", ""
	case errors.Is(err, jellyfin.ErrUnauthorized):
		res.Title = "Jellyfin rejected this API key"
		res.Text = "Create a new key in Jellyfin (Dashboard, then API Keys) and paste it here."
	case errors.As(err, &se) && se.StatusCode == http.StatusServiceUnavailable:
		res.Variant, res.Title, res.Text = "warn", "Jellyfin is starting up", "Try again in a few seconds."
	case errors.As(err, &ue) || errors.Is(err, jellyfin.ErrUnavailable):
		res.Title = "JellyTrim could not reach " + addr
		res.Text = "Check the address, and that both containers are on the same Docker network."
	default:
		res.Title = "JellyTrim could not connect to Jellyfin"
		res.Text = "Check the address and the API key."
	}
	return res
}

// ---------- Shared with Settings: libraries and users ----------

// setupLibraryType names a Jellyfin library type and says whether
// JellyTrim can manage it.
func setupLibraryType(collectionType string) (label string, supported bool) {
	switch collectionType {
	case "movies":
		return "Movies", true
	case "tvshows":
		return "Shows", true
	case "":
		return "Mixed", false
	case "musicvideos":
		return "Music videos", false
	case "homevideos":
		return "Home videos", false
	case "boxsets":
		return "Collections", false
	case "livetv":
		return "Live TV", false
	}
	return strings.ToUpper(collectionType[:1]) + collectionType[1:], false
}

func setupLibraryViews(libs []store.Library, checked func(store.Library) bool) []views.SetupLibrary {
	out := make([]views.SetupLibrary, 0, len(libs))
	for _, l := range libs {
		label, ok := setupLibraryType(l.CollectionType)
		v := views.SetupLibrary{ID: l.ID, Name: l.Name, Type: label, Locations: l.Locations, Supported: ok, Checked: ok && checked(l)}
		if !ok {
			v.Reason = label + " libraries are not supported. JellyTrim manages Movies and Shows libraries."
		}
		out = append(out, v)
	}
	return out
}

// setupLibraryChoice is the libraries ticked in a form, checked against
// what Jellyfin reported.
type setupLibraryChoice struct {
	IDs   []string
	Views []views.SetupLibrary
	Error string
}

func (s *Server) setupParseLibraries(ctx context.Context, form url.Values) (setupLibraryChoice, error) {
	var c setupLibraryChoice
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return c, err
	}
	picked := func(l store.Library) bool { return slices.Contains(form["library"], l.ID) }
	c.Views = setupLibraryViews(libs, picked)
	for _, v := range c.Views {
		if v.Checked {
			c.IDs = append(c.IDs, v.ID)
		}
	}
	if len(c.IDs) == 0 {
		c.Error = "Choose at least one library."
	}
	return c, nil
}

// ---------- Shared with Settings: path mappings ----------

// setupLibraryLocations is every library folder, which shows read-only in
// the mapping rows.
func (s *Server) setupLibraryLocations(ctx context.Context) (map[string]bool, error) {
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return nil, err
	}
	locs := map[string]bool{}
	for _, l := range libs {
		for _, loc := range l.Locations {
			locs[loc] = true
		}
	}
	return locs, nil
}

// setupSavedPathRows is the saved mappings, or suggestions for each managed
// library folder when none are saved yet.
func (s *Server) setupSavedPathRows(ctx context.Context, locs map[string]bool) ([]views.SetupPathRow, error) {
	ms, err := s.Store.PathMappings(ctx)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		libs, err := s.Store.Libraries(ctx)
		if err != nil {
			return nil, err
		}
		managed := slices.DeleteFunc(libs, func(l store.Library) bool { return !l.Managed })
		for _, m := range library.SuggestMappings(managed, library.DirExists) {
			if m.LocalPrefix == "" {
				m.LocalPrefix = m.JellyfinPrefix
			}
			ms = append(ms, m)
		}
	}
	rows := make([]views.SetupPathRow, 0, len(ms)+1)
	for _, m := range ms {
		rows = append(rows, views.SetupPathRow{Jellyfin: m.JellyfinPrefix, Local: m.LocalPrefix, FromLibrary: locs[m.JellyfinPrefix]})
	}
	if len(rows) == 0 {
		rows = append(rows, views.SetupPathRow{})
	}
	return rows, nil
}

func setupParsePathRows(form url.Values, locs map[string]bool) []views.SetupPathRow {
	jf, local := form["jellyfin"], form["local"]
	n := max(len(jf), len(local))
	rows := make([]views.SetupPathRow, 0, n)
	for i := range n {
		var row views.SetupPathRow
		if i < len(jf) {
			row.Jellyfin = strings.TrimSpace(jf[i])
		}
		if i < len(local) {
			row.Local = strings.TrimSpace(local[i])
		}
		row.FromLibrary = locs[row.Jellyfin]
		rows = append(rows, row)
	}
	return rows
}

// setupEditRows applies "Add mapping" or a row's "Remove". It reports false
// when the form was not an edit.
func setupEditRows(rows []views.SetupPathRow, form url.Values) ([]views.SetupPathRow, bool) {
	if form.Get("add") != "" {
		return append(rows, views.SetupPathRow{}), true
	}
	if v := form.Get("remove"); v != "" {
		i, err := strconv.Atoi(v)
		if err == nil && i >= 0 && i < len(rows) {
			rows = slices.Delete(rows, i, i+1)
		}
		if len(rows) == 0 {
			rows = append(rows, views.SetupPathRow{})
		}
		return rows, true
	}
	return rows, false
}

// setupValidatePaths checks each row with the path mapper, so the rules are
// the ones the sync uses. Blank rows are dropped. On failure the view has
// the error and the rows with their messages.
func setupValidatePaths(rows []views.SetupPathRow) ([]store.PathMapping, views.SetupPathsView) {
	var ms []store.PathMapping
	var valid []pathmap.Mapping
	v := views.SetupPathsView{}
	bad := false
	for _, row := range rows {
		if row.Jellyfin == "" && row.Local == "" {
			continue
		}
		setupCheckRow(&row, valid)
		if row.JellyfinError != "" || row.LocalError != "" {
			bad = true
		} else {
			valid = append(valid, pathmap.Mapping{Jellyfin: row.Jellyfin, Local: row.Local})
			ms = append(ms, store.PathMapping{JellyfinPrefix: row.Jellyfin, LocalPrefix: row.Local})
		}
		v.Rows = append(v.Rows, row)
	}
	switch {
	case bad:
		v.Error = "Some paths need fixing. The problems are marked below."
	case len(ms) == 0:
		v.Error = "Add at least one mapping."
		v.Rows = append(v.Rows, views.SetupPathRow{})
	}
	return ms, v
}

func setupCheckRow(row *views.SetupPathRow, earlier []pathmap.Mapping) {
	if row.Jellyfin == "" {
		row.JellyfinError = "Enter the folder as Jellyfin sees it."
	}
	if row.Local == "" {
		row.LocalError = "Enter the same folder as JellyTrim sees it."
	}
	if row.Jellyfin == "" || row.Local == "" {
		return
	}
	m := pathmap.Mapping{Jellyfin: row.Jellyfin, Local: row.Local}
	if _, err := pathmap.New([]pathmap.Mapping{m}); err != nil {
		if strings.Contains(err.Error(), "local path") {
			row.LocalError = "Enter a full path starting with /, without . or .. parts."
		} else {
			row.JellyfinError = "Enter a full path, such as /media/movies or D:\\Media, without . or .. parts."
		}
		return
	}
	if _, err := pathmap.New(append(slices.Clone(earlier), m)); err != nil {
		row.JellyfinError = "This Jellyfin path is already mapped in another row."
	}
}

// setupRunPathCheck validates the rows and, if they are valid, checks them
// against Jellyfin and the disk.
func (s *Server) setupRunPathCheck(ctx context.Context, rows []views.SetupPathRow) views.SetupPathsView {
	ms, v := setupValidatePaths(rows)
	if v.Error != "" {
		v.CheckError = &views.SetupConnResult{Variant: "bad", Title: v.Error}
		return v
	}
	if s.Library == nil {
		v.CheckError = &views.SetupConnResult{Variant: "bad", Title: "The path check is not available."}
		return v
	}
	checks, err := s.Library.CheckMappings(ctx, ms)
	if err != nil {
		res := setupConnError(err, "", "")
		res.Title = "JellyTrim could not check the paths: " + strings.ToLower(res.Title[:1]) + res.Title[1:]
		v.CheckError = &res
		return v
	}
	for _, c := range checks {
		v.Results = append(v.Results, setupPathResult(c))
	}
	return v
}

// setupPathResult says what the check found, in the words of docs/UI.md.
func setupPathResult(c library.MappingCheck) views.SetupPathResult {
	r := views.SetupPathResult{Jellyfin: c.Mapping.JellyfinPrefix, Local: c.Mapping.LocalPrefix}
	found, sampled := setupCount(c.Found), setupCount(c.Sampled)
	switch {
	case c.Error != "":
		r.Variant, r.Text = "bad", c.Error
	case !c.Writable:
		r.Variant, r.Text = "bad", "Found "+found+" of "+sampled+" files, but cannot write here. "+c.Problem
	case c.Sampled == 0:
		r.Variant, r.Text = "warn", "Jellyfin has no files under this path yet, so none could be looked for. JellyTrim can write here."
	case c.Found == 0:
		r.Variant, r.Text = "bad", "Found 0 of "+sampled+" files. Is the media mounted at this path inside the JellyTrim container?"
	case c.Found < c.Sampled:
		r.Variant, r.Text = "warn", "Found "+found+" of "+sampled+" files. "+setupCount(c.Sampled-c.Found)+" are missing, for example:"
		r.Missing = c.Missing
	case !c.HardLink:
		r.Variant, r.Text = "warn", "Found "+found+" of "+sampled+" files. Can write, but hard links are not supported here. Backups will use rename instead."
	case c.Problem != "":
		r.Variant, r.Text = "warn", "Found "+found+" of "+sampled+" files. "+c.Problem
	default:
		r.Variant, r.Text = "ok", "Found "+found+" of "+sampled+" files. Can write, hard-link and rename."
	}
	return r
}

// ---------- Shared with Settings: hardware ----------

func (s *Server) setupHardwareView(action string) views.SetupHardwareView {
	v := views.SetupHardwareView{Action: action}
	if s.Hardware == nil {
		return v
	}
	v.Available = true
	var latest time.Time
	for _, c := range s.Hardware.Capabilities() {
		v.Caps = append(v.Caps, setupCapability(c))
		if v.FFmpegVersion == "" {
			v.FFmpegVersion = setupFFmpegVersion(c.FFmpegVersion)
		}
		if c.TestedAt.After(latest) {
			latest = c.TestedAt
		}
	}
	if !latest.IsZero() {
		v.TestedAt = units.Ago(latest, s.Now())
	}
	return v
}

func setupCapability(c Capability) views.SetupCapability {
	v := views.SetupCapability{Label: c.Label, Backend: c.Backend, Codec: setupCodecLabel(c.Codec), Device: c.Device, Result: "unavailable"}
	switch {
	case c.Available:
		v.Result = "works"
	case c.Error != "":
		v.Result = "failed"
	}
	var notes []string
	if c.Available && c.HDR && !strings.Contains(c.Detail, "HDR") {
		notes = append(notes, "HDR metadata kept.")
	}
	if len(c.HardwareDecode) > 0 {
		labels := make([]string, 0, len(c.HardwareDecode))
		for _, d := range c.HardwareDecode {
			labels = append(labels, setupCodecLabel(d))
		}
		notes = append(notes, "Decodes "+strings.Join(labels, ", ")+" in hardware.")
	}
	for _, n := range []string{c.Detail, c.Error} {
		if n = strings.TrimSpace(n); n != "" {
			if !strings.HasSuffix(n, ".") {
				n += "."
			}
			notes = append(notes, n)
		}
	}
	v.Notes = strings.Join(notes, " ")
	return v
}

// setupFFmpegVersion shortens "ffmpeg version 8.1.2 Copyright ..." to
// "8.1.2".
func setupFFmpegVersion(v string) string {
	f := strings.Fields(v)
	if len(f) >= 3 && f[0] == "ffmpeg" && f[1] == "version" {
		return f[2]
	}
	return v
}

func setupCodecLabel(codec string) string {
	switch codec {
	case "hevc":
		return "HEVC"
	case "h264":
		return "H.264"
	case "av1":
		return "AV1"
	}
	return strings.ToUpper(codec)
}

// setupHardwareRetest runs the hardware test again. HTMX gets the table;
// a plain form post gets the whole page back.
func (s *Server) setupHardwareRetest(w http.ResponseWriter, r *http.Request, from string) {
	action := "/setup/hardware"
	if from == "settings" {
		action = "/settings/hardware/test"
	}
	var failure string
	if s.Hardware != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		if err := s.Hardware.Retest(ctx); err != nil {
			s.Log.Warn("setup: hardware test failed", "err", err)
			failure = "The test could not run: " + err.Error()
		}
	}
	v := s.setupHardwareView(action)
	v.Error = failure
	switch {
	case setupIsHTMX(r):
		s.render(w, r, views.SetupHardwareBody(v))
	case from == "settings":
		s.settingsWith(w, r, http.StatusOK, func(sv *views.SettingsView) { sv.Hardware = v })
	default:
		s.render(w, r, views.SetupHardware(v, assetVersion()))
	}
}

// ---------- Scan summary ----------

func setupSummary(sum library.Summary) views.SetupScanSummary {
	out := views.SetupScanSummary{Evaluated: setupCount(sum.Evaluated), Optimise: setupCount(sum.Optimise)}
	switch {
	case sum.Evaluated == 0:
		out.SavingNote = "Nothing was checked."
	case sum.Optimise == 0:
		out.SavingValue, out.SavingUnit = "0", "B"
		out.SavingNote = "No enabled policy would convert anything yet."
	default:
		out.Estimate = true
		out.SavingValue, out.SavingUnit = setupSplitBytes(sum.SavingMax())
		out.SavingNote = "Estimate. If every current plan ran."
		if lo, hi := units.Bytes(sum.SavingMin()), units.Bytes(sum.SavingMax()); lo != hi {
			out.SavingNote = "Estimate. Between " + lo + " and " + hi + " if every current plan ran."
		}
	}
	for _, l := range sum.Lines {
		out.Lines = append(out.Lines, views.SetupScanLine{Count: setupCount(l.Count), Text: l.Text})
	}
	return out
}

func setupSplitBytes(n int64) (value, unit string) {
	value, unit, _ = strings.Cut(units.Bytes(n), " ")
	return value, unit
}

// setupCount formats a count with thousands separators: 1,204.
func setupCount(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
