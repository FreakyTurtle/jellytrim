package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// ---------- Condition fields ----------

// Value kinds decide which input a condition shows and how it is parsed.
const (
	policyKindBool       = "bool"
	policyKindDays       = "days"
	policyKindResolution = "resolution"
	policyKindCodec      = "codec"
	policyKindMbps       = "mbps"
	policyKindGB         = "gb"
	policyKindHDR        = "hdr"
	policyKindWords      = "words"
)

// policyFieldSpec describes one condition field for the editor. The
// operators mirror what policy.Condition accepts.
type policyFieldSpec struct {
	Field string
	Label string
	Kind  string
	Ops   []views.Option
}

var (
	policyOpsDays  = []views.Option{{Value: policy.OpMoreThanDays, Label: "more than"}, {Value: policy.OpLessThanDays, Label: "less than"}}
	policyOpsIs    = []views.Option{{Value: policy.OpIs, Label: "is"}, {Value: policy.OpIsNot, Label: "is not"}}
	policyOpsAbove = []views.Option{{Value: policy.OpAbove, Label: "above"}, {Value: policy.OpBelow, Label: "below"}}
)

var policyFields = []policyFieldSpec{
	{policy.FieldWatched, "watched", policyKindBool, []views.Option{{Value: policy.OpIs, Label: "is"}}},
	{policy.FieldFavourite, "favourite", policyKindBool, []views.Option{{Value: policy.OpIs, Label: "is"}}},
	{policy.FieldLastWatched, "last watched", policyKindDays, policyOpsDays},
	{policy.FieldAdded, "added", policyKindDays, policyOpsDays},
	{policy.FieldResolution, "resolution", policyKindResolution, []views.Option{
		{Value: policy.OpAbove, Label: "above"}, {Value: policy.OpAtMost, Label: "at most"}, {Value: policy.OpIs, Label: "is"}}},
	{policy.FieldCodec, "codec", policyKindCodec, policyOpsIs},
	{policy.FieldBitrate, "bitrate", policyKindMbps, policyOpsAbove},
	{policy.FieldSize, "file size", policyKindGB, policyOpsAbove},
	{policy.FieldHDR, "dynamic range", policyKindHDR, policyOpsIs},
	{policy.FieldTag, "tag", policyKindWords, []views.Option{{Value: policy.OpHas, Label: "has"}, {Value: policy.OpHasNot, Label: "does not have"}}},
	{policy.FieldGenre, "genre", policyKindWords, policyOpsIs},
}

var policyBoolChoices = []views.Option{{Value: "yes", Label: "yes"}, {Value: "no", Label: "no"}}

var policyResolutionChoices = []views.Option{
	{Value: "480p", Label: "480p"}, {Value: "720p", Label: "720p"}, {Value: "1080p", Label: "1080p"},
	{Value: "1440p", Label: "1440p"}, {Value: "2160p", Label: "2160p"},
}

var policyCodecChoices = []views.Option{
	{Value: "h264", Label: "H.264"}, {Value: "hevc", Label: "HEVC"}, {Value: "av1", Label: "AV1"},
	{Value: "vp9", Label: "VP9"}, {Value: "mpeg2video", Label: "MPEG-2"}, {Value: "vc1", Label: "VC-1"},
}

// "dv" in the form stands for both Dolby Vision classes the engine knows.
var policyHDRChoices = []views.Option{
	{Value: "sdr", Label: "SDR"}, {Value: "hdr10", Label: "HDR10"}, {Value: "hlg", Label: "HLG"},
	{Value: "hdr10plus", Label: "HDR10+"}, {Value: "dv", Label: "Dolby Vision"},
}

var policyDolbyVision = []string{"dv-hdr10", "dv"}

func policyFieldByName(field string) (policyFieldSpec, bool) {
	for _, f := range policyFields {
		if f.Field == field {
			return f, true
		}
	}
	return policyFieldSpec{}, false
}

func policyOptionValues(opts []views.Option) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Value
	}
	return out
}

// ---------- Form model ----------

// policyForm is the editor's input as typed, so a re-render after an error
// never loses what the user entered.
type policyForm struct {
	Name        string
	Enabled     bool
	Libraries   []string
	Type        string // "", "Movie" or "Episode"
	Series      []string
	Seasons     string
	Collections []string
	Conds       []policyCondInput
	Kind        string
	MaxRes      string
	Codec       string
	Quality     string
	Encoder     string
	AllowHDR    bool
}

// policyCondInput is one condition row as typed.
type policyCondInput struct {
	Field  string
	Op     string
	Bool   string // "yes" or "no"
	Number string
	Text   string // a resolution
	List   []string
	Words  string // comma-separated tags or genres
}

// policyFormErrors maps a form key ("name", "seasons", "c0", "action",
// "form") to a plain-English message.
type policyFormErrors map[string]string

// policyMaxConditions bounds the condition rows read from one request.
const policyMaxConditions = 50

func policyCondKey(i int) string { return "c" + strconv.Itoa(i) }

// policyFromForm parses the editor's form into a policy. The policy is only
// usable when the errors are empty; it is returned either way so the preview
// can describe a half-finished policy.
func policyFromForm(v url.Values) (policy.Policy, policyFormErrors) {
	return readPolicyForm(v).parse()
}

// readPolicyForm reads the raw form. Condition rows are named c<N>.field,
// c<N>.op and so on; they are read in index order and renumbered from 0.
func readPolicyForm(v url.Values) policyForm {
	f := policyForm{
		Name:        strings.TrimSpace(v.Get("name")),
		Enabled:     v.Get("enabled") == "on",
		Libraries:   policyCleanList(v["library"]),
		Type:        strings.TrimSpace(v.Get("type")),
		Series:      policyCleanList(v["series"]),
		Seasons:     v.Get("seasons"),
		Collections: policyCleanList(v["collection"]),
		Kind:        v.Get("kind"),
		MaxRes:      v.Get("max_resolution"),
		Codec:       v.Get("codec"),
		Quality:     v.Get("quality"),
		Encoder:     v.Get("encoder"),
		AllowHDR:    v.Get("allow_hdr_reduction") == "on",
	}
	if f.Type == "both" {
		f.Type = ""
	}
	f.Conds = readPolicyConds(v)
	return f
}

func readPolicyConds(v url.Values) []policyCondInput {
	var idx []int
	for key := range v {
		rest, ok := strings.CutPrefix(key, "c")
		if !ok {
			continue
		}
		num, ok := strings.CutSuffix(rest, ".field")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(num); err == nil && n >= 0 {
			idx = append(idx, n)
		}
	}
	sort.Ints(idx)
	if len(idx) > policyMaxConditions {
		idx = idx[:policyMaxConditions]
	}
	out := make([]policyCondInput, 0, len(idx))
	for _, n := range idx {
		p := "c" + strconv.Itoa(n) + "."
		c := policyCondInput{
			Field: v.Get(p + "field"), Op: v.Get(p + "op"), Bool: v.Get(p + "bool"),
			Number: v.Get(p + "number"), Text: v.Get(p + "text"), Words: v.Get(p + "words"),
			List: policyCleanList(v[p+"list"]),
		}
		// The field changed since the row was drawn: its old values mean
		// something else now, so start the row afresh.
		if prev := v.Get(p + "prev"); prev != "" && prev != c.Field {
			c = policyCondDefaults(c.Field)
		}
		if spec, ok := policyFieldByName(c.Field); ok && !slices.Contains(policyOptionValues(spec.Ops), c.Op) {
			c.Op = spec.Ops[0].Value
		}
		out = append(out, c)
	}
	return out
}

// policyCondDefaults is a fresh row for a field.
func policyCondDefaults(field string) policyCondInput {
	c := policyCondInput{Field: field}
	spec, ok := policyFieldByName(field)
	if !ok {
		return c
	}
	c.Op = spec.Ops[0].Value
	switch spec.Kind {
	case policyKindBool:
		c.Bool = "yes"
	case policyKindResolution:
		c.Text = "1080p"
	}
	return c
}

func policyCleanList(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func (f policyForm) parse() (policy.Policy, policyFormErrors) {
	errs := policyFormErrors{}
	p := policy.Policy{Name: f.Name, Enabled: f.Enabled}
	switch {
	case f.Name == "":
		errs["name"] = "Give the policy a name."
	case utf8.RuneCountInString(f.Name) > 100:
		errs["name"] = "Keep the name to 100 characters or fewer."
	}
	p.Scope = f.parseScope(errs)
	p.Conditions = policy.Conditions{Version: 1, All: make([]policy.Condition, 0, len(f.Conds))}
	for i, in := range f.Conds {
		c, msg := in.parse()
		if msg != "" {
			errs[policyCondKey(i)] = msg
		}
		p.Conditions.All = append(p.Conditions.All, c)
	}
	p.Action = f.parseAction(errs)
	if len(errs) == 0 {
		// The engine has the last word, so nothing unevaluable is stored.
		if err := p.Validate(); err != nil {
			errs["form"] = policyErrorText(err)
		}
	}
	return p, errs
}

func (f policyForm) parseScope(errs policyFormErrors) policy.Scope {
	s := policy.Scope{Version: 1, Libraries: f.Libraries, Collections: f.Collections}
	switch f.Type {
	case "":
	case "Movie", "Episode":
		s.Types = []string{f.Type}
	default:
		errs["type"] = "Choose films, episodes or both."
	}
	if f.Type != "Movie" {
		s.Series = f.Series
	}
	if len(s.Series) == 1 {
		seasons, err := parseSeasons(f.Seasons)
		if err != nil {
			errs["seasons"] = "Enter season numbers separated by commas, such as 1, 2 or 1-3."
		}
		s.Seasons = seasons
	}
	return s
}

// parseSeasons reads "1, 2", "1 2" or "1-3". Empty means every season.
func parseSeasons(s string) ([]int, error) {
	var out []int
	add := func(n int) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := policySeason(lo)
		if err != nil {
			return nil, err
		}
		b := a
		if isRange {
			if b, err = policySeason(hi); err != nil {
				return nil, err
			}
		}
		if b < a || b-a > 100 {
			return nil, fmt.Errorf("season range %q", part)
		}
		for n := a; n <= b; n++ {
			add(n)
		}
	}
	sort.Ints(out)
	return out, nil
}

func policySeason(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > 999 {
		return 0, fmt.Errorf("season %q", s)
	}
	return n, nil
}

// parse turns one row into a condition, or says what is wrong with it.
func (in policyCondInput) parse() (policy.Condition, string) {
	c := policy.Condition{Field: in.Field, Op: in.Op}
	spec, ok := policyFieldByName(in.Field)
	if !ok {
		return c, "Choose a condition from the list."
	}
	if !slices.Contains(policyOptionValues(spec.Ops), in.Op) {
		return c, "Choose how to compare from the list."
	}
	var msg string
	switch spec.Kind {
	case policyKindBool:
		switch in.Bool {
		case "yes":
			c.Bool = policy.B(true)
		case "no":
			c.Bool = policy.B(false)
		default:
			msg = "Choose yes or no."
		}
	case policyKindDays:
		c.Number, msg = policyNumber(in.Number, "days", true, 36500)
	case policyKindMbps:
		c.Number, msg = policyNumber(in.Number, "Mbps", false, 100000)
	case policyKindGB:
		c.Number, msg = policyNumber(in.Number, "GB", false, 100000)
	case policyKindResolution:
		c.Text = in.Text
		if !slices.Contains(policyOptionValues(policyResolutionChoices), in.Text) {
			msg = "Choose a resolution from the list."
		}
	case policyKindCodec:
		c.List, msg = policyPickList(in.List, policyCodecChoices, "codec")
	case policyKindHDR:
		c.List, msg = policyPickList(in.List, policyHDRChoices, "dynamic range")
	case policyKindWords:
		c.List = policyWords(in.Words)
		if len(c.List) == 0 {
			msg = "Enter at least one " + spec.Label + ". Separate several with commas."
		}
	}
	return c, msg
}

func policyNumber(s, unit string, whole bool, maxValue float64) (float64, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "Enter a number of " + unit + "."
	}
	n, err := strconv.ParseFloat(s, 64)
	switch {
	case err != nil || math.IsNaN(n) || math.IsInf(n, 0):
		return 0, "Enter a number, such as 90."
	case n <= 0:
		return 0, "Enter a number above zero."
	case whole && n != math.Trunc(n):
		return 0, "Enter a whole number of " + unit + "."
	case n > maxValue:
		return 0, "Enter a number no larger than " + strconv.FormatFloat(maxValue, 'f', -1, 64) + "."
	}
	return n, ""
}

func policyPickList(picked []string, choices []views.Option, what string) ([]string, string) {
	allowed := policyOptionValues(choices)
	var out []string
	for _, v := range picked {
		if !slices.Contains(allowed, v) {
			return nil, "Choose a " + what + " from the list."
		}
		if v == "dv" {
			out = append(out, policyDolbyVision...)
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, "Choose at least one " + what + "."
	}
	return out, ""
}

// policyWords splits "keep, archive" into its words, keeping the first
// spelling of each and ignoring case when checking for repeats.
func policyWords(s string) []string {
	var out []string
	for _, w := range strings.Split(s, ",") {
		w = strings.TrimSpace(w)
		if w == "" || slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, w) }) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Action choices the editor offers. AV1 is shown but planned.
var (
	policyMaxResValues  = []string{"keep", "2160p", "1080p", "720p", "480p"}
	policyCodecValues   = []string{"keep", "hevc", "h264"}
	policyQualityValues = []string{policy.QualityMaximum, policy.QualityHigh, policy.QualityBalanced, policy.QualitySpaceSaver}
)

func (f policyForm) parseAction(errs policyFormErrors) policy.Action {
	kind := f.Kind
	if kind == "" {
		kind = policy.KindOptimise
	}
	switch kind {
	case policy.KindProtect:
		return policy.Action{Version: 1, Kind: policy.KindProtect}
	case policy.KindOptimise:
	default:
		errs["kind"] = "Choose Optimise or Protect."
	}
	a := policy.Action{
		Version: 1, Kind: policy.KindOptimise,
		MaxResolution: policyOr(f.MaxRes, "keep"), Codec: policyOr(f.Codec, "hevc"),
		Quality: policyOr(f.Quality, policy.QualityHigh), Encoder: policyOr(f.Encoder, "auto"),
		Audio: "preserve", Subtitles: "preserve", AllowHDRReduction: f.AllowHDR,
	}
	if !slices.Contains(policyMaxResValues, a.MaxResolution) {
		errs["max_resolution"] = "Choose a resolution from the list."
	}
	switch {
	case a.Codec == "av1":
		errs["codec"] = "AV1 is planned and cannot be chosen yet."
	case !slices.Contains(policyCodecValues, a.Codec):
		errs["codec"] = "Choose a codec from the list."
	}
	if !slices.Contains(policyQualityValues, a.Quality) {
		errs["quality"] = "Choose a quality from the list."
	}
	if a.Encoder != "auto" {
		errs["encoder"] = "Only Auto is available for now."
	}
	if a.MaxResolution == "keep" && a.Codec == "keep" {
		errs["action"] = "Choose a lower resolution or a new codec. Keeping both would change nothing."
	}
	return a
}

func policyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// policyErrorText turns a policy.Validate error into a sentence.
func policyErrorText(err error) string {
	msg := strings.TrimPrefix(err.Error(), policy.ErrInvalidPolicy.Error()+": ")
	if msg == "" {
		return "The policy is not valid."
	}
	return "The policy cannot be saved: " + msg + "."
}

// policyFormFrom fills the editor from a stored policy.
func policyFormFrom(p policy.Policy) policyForm {
	f := policyForm{
		Name: p.Name, Enabled: p.Enabled, Libraries: p.Scope.Libraries, Series: p.Scope.Series,
		Collections: p.Scope.Collections, Kind: p.Action.Kind, MaxRes: p.Action.MaxResolution,
		Codec: p.Action.Codec, Quality: p.Action.Quality, Encoder: p.Action.Encoder, AllowHDR: p.Action.AllowHDRReduction,
	}
	if len(p.Scope.Types) == 1 {
		f.Type = p.Scope.Types[0]
	}
	seasons := make([]string, len(p.Scope.Seasons))
	for i, n := range p.Scope.Seasons {
		seasons[i] = strconv.Itoa(n)
	}
	f.Seasons = strings.Join(seasons, ", ")
	if f.Kind == policy.KindProtect || f.Kind == "" {
		f.MaxRes, f.Codec, f.Quality = "keep", "hevc", policy.QualityHigh
	}
	f.MaxRes, f.Encoder = policyOr(f.MaxRes, "keep"), policyOr(f.Encoder, "auto")
	f.Codec, f.Quality = policyOr(f.Codec, "keep"), policyOr(f.Quality, policy.QualityHigh)
	for _, c := range p.Conditions.All {
		f.Conds = append(f.Conds, policyCondFrom(c))
	}
	return f
}

func policyCondFrom(c policy.Condition) policyCondInput {
	in := policyCondInput{Field: c.Field, Op: c.Op, Text: c.Text}
	if c.Bool != nil {
		in.Bool = "no"
		if *c.Bool {
			in.Bool = "yes"
		}
	}
	if c.Number != 0 {
		in.Number = strconv.FormatFloat(c.Number, 'f', -1, 64)
	}
	spec, _ := policyFieldByName(c.Field)
	switch spec.Kind {
	case policyKindWords:
		in.Words = strings.Join(c.List, ", ")
	case policyKindHDR:
		for _, v := range c.List {
			if slices.Contains(policyDolbyVision, v) {
				v = "dv"
			}
			if !slices.Contains(in.List, v) {
				in.List = append(in.List, v)
			}
		}
	default:
		in.List = c.List
	}
	return in
}

// ---------- Names ----------

// policyNames resolves Jellyfin IDs for descriptions and lists the choices
// the editor offers.
type policyNames struct {
	libs        []store.Library
	series      []views.Option // by name
	collections []store.Collection
	libByID     map[string]string
	seriesByID  map[string]string
	colByID     map[string]string
}

func (n policyNames) Library(id string) string    { return n.libByID[id] }
func (n policyNames) Series(id string) string     { return n.seriesByID[id] }
func (n policyNames) Collection(id string) string { return n.colByID[id] }

func (s *Server) loadPolicyNames(ctx context.Context) (policyNames, error) {
	n := policyNames{libByID: map[string]string{}, seriesByID: map[string]string{}, colByID: map[string]string{}}
	var err error
	if n.libs, err = s.Store.Libraries(ctx); err != nil {
		return n, err
	}
	for _, l := range n.libs {
		n.libByID[l.ID] = l.Name
	}
	if n.seriesByID, err = s.Store.SeriesNames(ctx); err != nil {
		return n, err
	}
	for id, name := range n.seriesByID {
		n.series = append(n.series, views.Option{Value: id, Label: name})
	}
	sort.Slice(n.series, func(i, j int) bool {
		a, b := strings.ToLower(n.series[i].Label), strings.ToLower(n.series[j].Label)
		if a != b {
			return a < b
		}
		return n.series[i].Value < n.series[j].Value
	})
	if n.collections, err = s.Store.Collections(ctx); err != nil {
		return n, err
	}
	for _, c := range n.collections {
		n.colByID[c.ID] = c.Name
	}
	return n, nil
}

// ---------- Handlers ----------

// policyService returns the library service, or one bound to the store when
// the server was built without it (tests of other pages).
func (s *Server) policyService() *library.Service {
	if s.Library != nil {
		return s.Library
	}
	return library.New(library.Options{Store: s.Store, Log: s.Log, Now: s.Now})
}

// policyReevaluate re-checks the library in the background after a change.
func (s *Server) policyReevaluate() {
	if s.Library != nil && !s.Library.EvaluateAsync() {
		s.Log.Info("policies: evaluation already running; the change applies from the next run")
	}
}

func policyPathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func policyIsHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func (s *Server) policyRender(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		s.Log.Error("http: render", "path", r.URL.Path, "err", err)
	}
}

func (s *Server) policyNotFound(w http.ResponseWriter, r *http.Request) {
	if policyIsHTMX(r) || r.Method != http.MethodGet {
		http.Error(w, "That policy no longer exists.", http.StatusNotFound)
		return
	}
	sh, err := s.shell(r, "Policy not found", "policies")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.policyRender(w, r, http.StatusNotFound, views.PolicyMissingPage(sh))
}

// policies shows the ordered list.
func (s *Server) policies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.policyListView(ctx, 0, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	q := r.URL.Query()
	if id, err := strconv.ParseInt(q.Get("saved"), 10, 64); err == nil {
		for _, c := range list.Cards {
			if c.ID == id {
				list.Notice = "Saved " + c.Name
				list.NoticeText = "JellyTrim is checking the library against your policies again."
			}
		}
	}
	if name := q.Get("deleted"); name != "" {
		list.Notice = "Deleted " + name
		list.NoticeText = "JellyTrim is checking the library against the remaining policies."
	}
	sh, err := s.shell(r, "Policies", "policies")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.PoliciesPage(sh, list))
}

// policyListView builds the list. focusID, when set, is the control that
// takes focus after an HTMX swap removed the one that was pressed.
func (s *Server) policyListView(ctx context.Context, focusPolicy int64, focusID string) (views.PoliciesListView, error) {
	var v views.PoliciesListView
	ps, err := s.policyService().Policies(ctx)
	if err != nil {
		return v, err
	}
	names, err := s.loadPolicyNames(ctx)
	if err != nil {
		return v, err
	}
	for i, p := range ps {
		c := views.PolicyCardView{
			ID: p.ID, Position: i + 1, Name: p.Name, Sentence: policy.Describe(p, names),
			Enabled: p.Enabled, Protect: p.Action.Kind == policy.KindProtect,
			First: i == 0, Last: i == len(ps)-1,
		}
		if p.ID == focusPolicy {
			c.Focus = focusID
		}
		v.Cards = append(v.Cards, c)
	}
	return v, nil
}

// newPolicy opens the editor on a fresh policy.
func (s *Server) newPolicy(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f := policyForm{Enabled: true, Kind: policy.KindOptimise, MaxRes: "keep", Codec: "hevc", Quality: policy.QualityHigh, Encoder: "auto"}
	if slices.Contains(policyQualityValues, st.DefaultQuality) {
		f.Quality = st.DefaultQuality
	}
	if slices.Contains(policyCodecValues[1:], st.DefaultCodec) {
		f.Codec = st.DefaultCodec
	}
	s.renderPolicyEditor(w, r, policyEditorState{form: f})
}

// editPolicy opens the editor on a stored policy.
func (s *Server) editPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := policyPathID(r)
	if !ok {
		s.policyNotFound(w, r)
		return
	}
	p, err := s.policyService().Policy(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.policyNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPolicyEditor(w, r, policyEditorState{id: id, form: policyFormFrom(p)})
}

// policyEditorState is what one render of the editor needs.
type policyEditorState struct {
	id       int64
	form     policyForm
	errs     policyFormErrors
	focus    string // element id that takes focus
	status   int
	fragment bool // only the form, for an HTMX swap
}

func (s *Server) renderPolicyEditor(w http.ResponseWriter, r *http.Request, st policyEditorState) {
	ctx := r.Context()
	names, err := s.loadPolicyNames(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if st.status == 0 {
		st.status = http.StatusOK
	}
	v := policyEditorView(st, names)
	if st.fragment {
		s.policyRender(w, r, st.status, views.PolicyEditorForm(v))
		return
	}
	pv := s.policyPreviewView(ctx, st.id, st.form, names)
	title := "New policy"
	if st.id != 0 {
		title = "Edit policy"
	}
	sh, err := s.shell(r, title, "policies")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.policyRender(w, r, st.status, views.PolicyEditPage(sh, v, pv))
}

// savePolicy saves the editor, or handles its add, remove and refresh
// buttons by drawing the form again.
func (s *Server) savePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var id int64
	if r.PathValue("id") != "" {
		var ok bool
		if id, ok = policyPathID(r); !ok {
			s.policyNotFound(w, r)
			return
		}
		if _, err := s.Store.Policy(ctx, id); errors.Is(err, store.ErrNotFound) {
			s.policyNotFound(w, r)
			return
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	st := policyEditorState{id: id, form: readPolicyForm(r.PostForm), fragment: policyIsHTMX(r)}
	if policyApplyOp(r.PostForm.Get("op"), &st) {
		s.renderPolicyEditor(w, r, st)
		return
	}
	s.storePolicy(w, r, st)
}

// policyApplyOp applies an add, remove or refresh button to the form. It
// reports false for a save.
func policyApplyOp(op string, st *policyEditorState) bool {
	f := &st.form
	switch {
	case op == "add":
		if len(f.Conds) < policyMaxConditions {
			f.Conds = append(f.Conds, policyCondDefaults(policy.FieldWatched))
		}
		st.focus = views.PolicyCondFieldID(len(f.Conds) - 1)
	case strings.HasPrefix(op, "remove-"):
		if i, err := strconv.Atoi(strings.TrimPrefix(op, "remove-")); err == nil && i >= 0 && i < len(f.Conds) {
			f.Conds = slices.Delete(f.Conds, i, i+1)
		}
		st.focus = views.PolicyAddConditionID
	case op == "refresh":
	default:
		return false
	}
	return true
}

func (s *Server) storePolicy(w http.ResponseWriter, r *http.Request, st policyEditorState) {
	p, errs := st.form.parse()
	if len(errs) > 0 {
		st.errs, st.status, st.fragment = errs, http.StatusUnprocessableEntity, false
		s.renderPolicyEditor(w, r, st)
		return
	}
	p.ID = st.id
	id, err := s.policyService().SavePolicy(r.Context(), p)
	switch {
	case errors.Is(err, policy.ErrInvalidPolicy):
		st.errs, st.status, st.fragment = policyFormErrors{"form": policyErrorText(err)}, http.StatusUnprocessableEntity, false
		s.renderPolicyEditor(w, r, st)
		return
	case errors.Is(err, store.ErrNotFound):
		s.policyNotFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.policyReevaluate()
	redirect(w, r, "/policies?saved="+strconv.FormatInt(id, 10))
}

// previewPolicy answers the editor's live preview.
func (s *Server) previewPolicy(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	names, err := s.loadPolicyNames(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	id, _ := strconv.ParseInt(r.PostForm.Get("id"), 10, 64)
	pv := s.policyPreviewView(r.Context(), max(id, 0), readPolicyForm(r.PostForm), names)
	s.render(w, r, views.PolicyPreviewUpdate(pv))
}

// policyPreviewView simulates the policy at its place in the list.
func (s *Server) policyPreviewView(ctx context.Context, id int64, f policyForm, names policyNames) views.PolicyPreviewView {
	p, errs := f.parse()
	if p.Name == "" {
		p.Name = "this policy" // sample summaries name the deciding policy
	}
	v := views.PolicyPreviewView{Sentence: policy.Describe(p, names), Enabled: f.Enabled, Protect: p.Action.Kind == policy.KindProtect}
	delete(errs, "name") // a name does not change what the policy does
	delete(errs, "form")
	if len(errs) > 0 {
		v.Problem = "Finish the parts of the policy marked as incomplete to see what it would do."
		return v
	}
	svc := s.policyService()
	p.ID = id
	if err := s.policyPlace(ctx, svc, &p); err != nil {
		s.Log.Error("policies: preview", "err", err)
		v.Failed = true
		return v
	}
	res, err := svc.Preview(ctx, p)
	if err != nil {
		s.Log.Error("policies: preview", "err", err)
		v.Failed = true
		return v
	}
	v.Ready = true
	v.Matches, v.Wins, v.Higher = res.Matches, res.Wins, res.Matches-res.Wins
	v.Optimise, v.Optimal, v.Skipped = res.Optimise, res.Optimal, res.Skipped
	if res.Optimise > 0 {
		v.Current = units.Bytes(res.Current)
		v.After = policyRange(res.AfterMin, res.AfterMax)
		v.Saving = policyRange(max(res.Current-res.AfterMax, 0), max(res.Current-res.AfterMin, 0))
	}
	for _, sm := range res.Samples {
		v.Samples = append(v.Samples, views.PolicySampleView{
			Href: "/library/" + url.PathEscape(sm.ItemID), Name: sm.Name,
			Outcome: sm.Outcome.Label(), Lamp: policyOutcomeLamp(sm.Outcome), Summary: sm.Summary,
		})
	}
	return v
}

// policyPlace gives the previewed policy its real position: its stored
// priority, or the end of the list for a new one.
func (s *Server) policyPlace(ctx context.Context, svc *library.Service, p *policy.Policy) error {
	if p.ID != 0 {
		stored, err := svc.Policy(ctx, p.ID)
		if err == nil {
			p.Priority = stored.Priority
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		p.ID = 0
	}
	ps, err := svc.Policies(ctx)
	if err != nil {
		return err
	}
	for _, o := range ps {
		p.Priority = max(p.Priority, o.Priority)
	}
	p.Priority += 10
	return nil
}

func policyRange(lo, hi int64) string {
	a, b := units.Bytes(lo), units.Bytes(hi)
	if a == b {
		return a
	}
	return a + " to " + b
}

func policyOutcomeLamp(o plan.Outcome) string {
	switch o {
	case plan.Optimise:
		return "info"
	case plan.AlreadyOptimal:
		return "ok"
	case plan.Skipped:
		return "warn"
	}
	return "idle"
}

// togglePolicy turns a policy on or off.
func (s *Server) togglePolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.policyForAction(w, r)
	if !ok {
		return
	}
	enabled := r.PostForm.Get("enabled") == "on"
	if err := s.Store.SetPolicyEnabled(r.Context(), p.ID, enabled); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.policyReevaluate()
	if !policyIsHTMX(r) {
		redirect(w, r, "/policies#"+views.PolicyCardID(p.ID))
		return
	}
	list, err := s.policyListView(r.Context(), 0, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	state := "off"
	if enabled {
		state = "on"
	}
	for _, c := range list.Cards {
		if c.ID == p.ID {
			s.render(w, r, views.PolicyCardUpdate(c, p.Name+" turned "+state+"."))
			return
		}
	}
	s.policyNotFound(w, r)
}

// movePolicy moves a policy one place up or down.
func (s *Server) movePolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.policyForAction(w, r)
	if !ok {
		return
	}
	dir := r.PostForm.Get("dir")
	if dir != "up" && dir != "down" {
		http.Error(w, "Choose up or down.", http.StatusBadRequest)
		return
	}
	if err := s.Store.MovePolicy(r.Context(), p.ID, dir == "up"); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.policyReevaluate()
	if !policyIsHTMX(r) {
		redirect(w, r, "/policies#"+views.PolicyCardID(p.ID))
		return
	}
	list, err := s.policyListView(r.Context(), 0, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	pos := 0
	for i, c := range list.Cards {
		if c.ID != p.ID {
			continue
		}
		pos = c.Position
		// The pressed button is gone at either end: focus its partner.
		if dir == "up" && c.First && !c.Last {
			list.Cards[i].Focus = views.PolicyMoveID(p.ID, "down")
		} else if dir == "down" && c.Last && !c.First {
			list.Cards[i].Focus = views.PolicyMoveID(p.ID, "up")
		}
	}
	s.render(w, r, views.PolicyListUpdate(list, fmt.Sprintf("%s moved to position %d.", p.Name, pos)))
}

// deletePolicy removes a policy after the confirmation dialog.
func (s *Server) deletePolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.policyForAction(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeletePolicy(r.Context(), p.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.policyReevaluate()
	redirect(w, r, "/policies?deleted="+url.QueryEscape(p.Name))
}

// policyForAction loads the policy named in the path and parses the form.
func (s *Server) policyForAction(w http.ResponseWriter, r *http.Request) (policy.Policy, bool) {
	id, ok := policyPathID(r)
	if !ok {
		s.policyNotFound(w, r)
		return policy.Policy{}, false
	}
	p, err := s.policyService().Policy(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.policyNotFound(w, r)
		return p, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return p, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return p, false
	}
	return p, true
}

// ---------- View models ----------

// policyEditorView turns the form state into what the editor template draws.
func policyEditorView(st policyEditorState, names policyNames) views.PolicyEditorView {
	f := st.form
	v := views.PolicyEditorView{
		ID: st.id, Action: "/policies", Name: f.Name, Enabled: f.Enabled,
		Type: policyOr(f.Type, "both"), Seasons: f.Seasons,
		LibrariesSelected: f.Libraries, SeriesSelected: f.Series, CollectionsSelected: f.Collections,
		Kind: policyOr(f.Kind, policy.KindOptimise), MaxRes: policyOr(f.MaxRes, "keep"), Codec: policyOr(f.Codec, "hevc"),
		Quality: policyOr(f.Quality, policy.QualityHigh), Encoder: policyOr(f.Encoder, "auto"), AllowHDR: f.AllowHDR,
		Errors: st.errs, Focus: st.focus, Fragment: st.fragment,
	}
	if st.id != 0 {
		v.Action = "/policies/" + strconv.FormatInt(st.id, 10)
	}
	for _, l := range names.libs {
		if l.Managed || slices.Contains(f.Libraries, l.ID) {
			label := l.Name
			if !l.Managed {
				label += " (not managed)"
			}
			v.Libraries = append(v.Libraries, views.Option{Value: l.ID, Label: label})
		}
	}
	v.Libraries = policyKeepUnknown(v.Libraries, f.Libraries, "Removed library")
	v.ShowSeries = f.Type != "Movie"
	v.Series = policyKeepUnknown(slices.Clone(names.series), f.Series, "Removed series")
	if v.ShowSeries && len(f.Series) == 1 {
		v.ShowSeasons = true
		v.SeasonsOf = names.seriesByID[f.Series[0]]
	}
	for _, c := range names.collections {
		v.Collections = append(v.Collections, views.Option{Value: c.ID, Label: c.Name})
	}
	v.Collections = policyKeepUnknown(v.Collections, f.Collections, "Removed collection")
	for _, spec := range policyFields {
		v.Fields = append(v.Fields, views.Option{Value: spec.Field, Label: spec.Label})
	}
	for i, c := range f.Conds {
		v.Conds = append(v.Conds, policyCondView(i, c, st.errs[policyCondKey(i)]))
	}
	return v
}

// policyKeepUnknown adds selected IDs that are no longer offered, so saving
// the form does not silently drop them.
func policyKeepUnknown(opts []views.Option, selected []string, label string) []views.Option {
	for _, id := range selected {
		if !slices.ContainsFunc(opts, func(o views.Option) bool { return o.Value == id }) {
			opts = append(opts, views.Option{Value: id, Label: label})
		}
	}
	return opts
}

func policyCondView(i int, c policyCondInput, errText string) views.PolicyCondView {
	spec, ok := policyFieldByName(c.Field)
	if !ok {
		spec = policyFields[0]
		if errText == "" {
			errText = "Choose a condition from the list."
		}
	}
	v := views.PolicyCondView{
		Index: i, Field: c.Field, FieldLabel: spec.Label, Kind: spec.Kind, Ops: spec.Ops, Op: c.Op,
		Bool: c.Bool, Number: c.Number, Text: c.Text, List: c.List, Words: c.Words, Error: errText,
	}
	switch spec.Kind {
	case policyKindBool:
		v.Choices = policyBoolChoices
	case policyKindDays:
		v.Unit = "days ago"
	case policyKindMbps:
		v.Unit = "Mbps"
	case policyKindGB:
		v.Unit = "GB"
	case policyKindResolution:
		v.Choices = policyResolutionChoices
	case policyKindCodec:
		v.Choices = policyCodecChoices
	case policyKindHDR:
		v.Choices = policyHDRChoices
	case policyKindWords:
		v.Unit = "separate several with commas"
	}
	return v
}
