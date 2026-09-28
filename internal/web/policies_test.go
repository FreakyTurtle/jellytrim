package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// ---------- policyFromForm ----------

// policyBaseForm is a valid form: a named optimise policy with no scope and
// no conditions.
func policyBaseForm() url.Values {
	return url.Values{
		"name": {"Test"}, "enabled": {"on"}, "type": {"both"},
		"kind": {"optimise"}, "max_resolution": {"1080p"}, "codec": {"hevc"}, "quality": {"high"}, "encoder": {"auto"},
	}
}

func policyWith(extra map[string][]string) url.Values {
	v := policyBaseForm()
	for k, vals := range extra {
		if vals == nil {
			v.Del(k)
			continue
		}
		v[k] = vals
	}
	return v
}

func policyOptimise(maxRes, codec, quality string) policy.Action {
	return policy.Action{Version: 1, Kind: policy.KindOptimise, MaxResolution: maxRes, Codec: codec, Quality: quality,
		Encoder: "auto", Audio: "preserve", Subtitles: "preserve"}
}

func TestPolicyFromFormValid(t *testing.T) {
	v := url.Values{
		"name": {"  Archive watched movies  "}, "enabled": {"on"},
		"library": {"lm", "lt", "lm", " "}, "type": {"Episode"}, "series": {"s1"}, "seasons": {"2, 1, 3-4"},
		"collection": {"box"},
		"c0.field":   {"watched"}, "c0.op": {"is"}, "c0.bool": {"yes"},
		"c1.field": {"last_watched"}, "c1.op": {"more_than_days"}, "c1.number": {" 90 "},
		"c2.field": {"resolution"}, "c2.op": {"above"}, "c2.text": {"1080p"},
		"kind": {"optimise"}, "max_resolution": {"720p"}, "codec": {"h264"}, "quality": {"space_saver"},
		"encoder": {"auto"}, "allow_hdr_reduction": {"on"},
	}
	got, errs := policyFromForm(v)
	if len(errs) != 0 {
		t.Fatalf("errors %v", errs)
	}
	want := policy.Policy{
		Name: "Archive watched movies", Enabled: true,
		Scope: policy.Scope{Version: 1, Libraries: []string{"lm", "lt"}, Types: []string{"Episode"}, Series: []string{"s1"},
			Seasons: []int{1, 2, 3, 4}, Collections: []string{"box"}},
		Conditions: policy.Conditions{Version: 1, All: []policy.Condition{
			{Field: "watched", Op: "is", Bool: policy.B(true)},
			{Field: "last_watched", Op: "more_than_days", Number: 90},
			{Field: "resolution", Op: "above", Text: "1080p"},
		}},
		Action: policy.Action{Version: 1, Kind: "optimise", MaxResolution: "720p", Codec: "h264", Quality: "space_saver",
			Encoder: "auto", Audio: "preserve", Subtitles: "preserve", AllowHDRReduction: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestPolicyFromFormConditions(t *testing.T) {
	cases := []struct {
		name string
		in   map[string][]string // c0.* values
		want policy.Condition
		err  string // part of the error message; empty for none
	}{
		{"watched yes", map[string][]string{"field": {"watched"}, "op": {"is"}, "bool": {"yes"}}, policy.Condition{Field: "watched", Op: "is", Bool: policy.B(true)}, ""},
		{"watched no", map[string][]string{"field": {"watched"}, "op": {"is"}, "bool": {"no"}}, policy.Condition{Field: "watched", Op: "is", Bool: policy.B(false)}, ""},
		{"watched missing bool", map[string][]string{"field": {"watched"}, "op": {"is"}}, policy.Condition{}, "yes or no"},
		{"favourite no", map[string][]string{"field": {"favourite"}, "op": {"is"}, "bool": {"no"}}, policy.Condition{Field: "favourite", Op: "is", Bool: policy.B(false)}, ""},
		{"favourite bad bool", map[string][]string{"field": {"favourite"}, "op": {"is"}, "bool": {"maybe"}}, policy.Condition{}, "yes or no"},
		{"last watched less", map[string][]string{"field": {"last_watched"}, "op": {"less_than_days"}, "number": {"7"}}, policy.Condition{Field: "last_watched", Op: "less_than_days", Number: 7}, ""},
		{"added more", map[string][]string{"field": {"added"}, "op": {"more_than_days"}, "number": {"30"}}, policy.Condition{Field: "added", Op: "more_than_days", Number: 30}, ""},
		{"days empty", map[string][]string{"field": {"added"}, "op": {"more_than_days"}, "number": {""}}, policy.Condition{}, "Enter a number of days"},
		{"days text", map[string][]string{"field": {"added"}, "op": {"more_than_days"}, "number": {"ninety"}}, policy.Condition{}, "Enter a number, such as 90"},
		{"days zero", map[string][]string{"field": {"added"}, "op": {"more_than_days"}, "number": {"0"}}, policy.Condition{}, "above zero"},
		{"days negative", map[string][]string{"field": {"last_watched"}, "op": {"more_than_days"}, "number": {"-5"}}, policy.Condition{}, "above zero"},
		{"days fraction", map[string][]string{"field": {"last_watched"}, "op": {"more_than_days"}, "number": {"1.5"}}, policy.Condition{}, "whole number"},
		{"days NaN", map[string][]string{"field": {"last_watched"}, "op": {"more_than_days"}, "number": {"NaN"}}, policy.Condition{}, "Enter a number, such as 90"},
		{"days Inf", map[string][]string{"field": {"last_watched"}, "op": {"more_than_days"}, "number": {"+Inf"}}, policy.Condition{}, "Enter a number, such as 90"},
		{"days too many", map[string][]string{"field": {"last_watched"}, "op": {"more_than_days"}, "number": {"99999"}}, policy.Condition{}, "no larger than"},
		{"resolution above", map[string][]string{"field": {"resolution"}, "op": {"above"}, "text": {"1080p"}}, policy.Condition{Field: "resolution", Op: "above", Text: "1080p"}, ""},
		{"resolution at most", map[string][]string{"field": {"resolution"}, "op": {"at_most"}, "text": {"720p"}}, policy.Condition{Field: "resolution", Op: "at_most", Text: "720p"}, ""},
		{"resolution is", map[string][]string{"field": {"resolution"}, "op": {"is"}, "text": {"2160p"}}, policy.Condition{Field: "resolution", Op: "is", Text: "2160p"}, ""},
		{"resolution unknown", map[string][]string{"field": {"resolution"}, "op": {"is"}, "text": {"999p"}}, policy.Condition{}, "resolution from the list"},
		{"codec is", map[string][]string{"field": {"codec"}, "op": {"is"}, "list": {"h264", "vc1", "h264"}}, policy.Condition{Field: "codec", Op: "is", List: []string{"h264", "vc1"}}, ""},
		{"codec is not", map[string][]string{"field": {"codec"}, "op": {"is_not"}, "list": {"hevc"}}, policy.Condition{Field: "codec", Op: "is_not", List: []string{"hevc"}}, ""},
		{"codec none", map[string][]string{"field": {"codec"}, "op": {"is"}}, policy.Condition{}, "at least one codec"},
		{"codec unknown", map[string][]string{"field": {"codec"}, "op": {"is"}, "list": {"prores"}}, policy.Condition{}, "codec from the list"},
		{"bitrate above", map[string][]string{"field": {"bitrate"}, "op": {"above"}, "number": {"12.5"}}, policy.Condition{Field: "bitrate", Op: "above", Number: 12.5}, ""},
		{"bitrate below", map[string][]string{"field": {"bitrate"}, "op": {"below"}, "number": {"4"}}, policy.Condition{Field: "bitrate", Op: "below", Number: 4}, ""},
		{"bitrate text", map[string][]string{"field": {"bitrate"}, "op": {"below"}, "number": {"4 Mbps"}}, policy.Condition{}, "Enter a number"},
		{"size above", map[string][]string{"field": {"size"}, "op": {"above"}, "number": {"10"}}, policy.Condition{Field: "size", Op: "above", Number: 10}, ""},
		{"size below", map[string][]string{"field": {"size"}, "op": {"below"}, "number": {"0.5"}}, policy.Condition{Field: "size", Op: "below", Number: 0.5}, ""},
		{"hdr is", map[string][]string{"field": {"hdr"}, "op": {"is"}, "list": {"hdr10", "hlg"}}, policy.Condition{Field: "hdr", Op: "is", List: []string{"hdr10", "hlg"}}, ""},
		{"hdr dolby vision", map[string][]string{"field": {"hdr"}, "op": {"is_not"}, "list": {"sdr", "dv", "hdr10plus"}}, policy.Condition{Field: "hdr", Op: "is_not", List: []string{"sdr", "dv-hdr10", "dv", "hdr10plus"}}, ""},
		{"hdr none", map[string][]string{"field": {"hdr"}, "op": {"is"}}, policy.Condition{}, "at least one dynamic range"},
		{"tag has", map[string][]string{"field": {"tag"}, "op": {"has"}, "words": {" keep, Archive ,keep, KEEP,, "}}, policy.Condition{Field: "tag", Op: "has", List: []string{"keep", "Archive"}}, ""},
		{"tag has not", map[string][]string{"field": {"tag"}, "op": {"has_not"}, "words": {"kids"}}, policy.Condition{Field: "tag", Op: "has_not", List: []string{"kids"}}, ""},
		{"tag empty", map[string][]string{"field": {"tag"}, "op": {"has"}, "words": {" , "}}, policy.Condition{}, "at least one tag"},
		{"genre is", map[string][]string{"field": {"genre"}, "op": {"is"}, "words": {"Documentary, Drama"}}, policy.Condition{Field: "genre", Op: "is", List: []string{"Documentary", "Drama"}}, ""},
		{"genre is not", map[string][]string{"field": {"genre"}, "op": {"is_not"}, "words": {"Animation"}}, policy.Condition{Field: "genre", Op: "is_not", List: []string{"Animation"}}, ""},
		{"genre empty", map[string][]string{"field": {"genre"}, "op": {"is"}}, policy.Condition{}, "at least one genre"},
		{"unknown field", map[string][]string{"field": {"colour"}, "op": {"is"}}, policy.Condition{}, "condition from the list"},
		{"wrong op is corrected", map[string][]string{"field": {"size"}, "op": {"more_than_days"}, "number": {"3"}}, policy.Condition{Field: "size", Op: "above", Number: 3}, ""},
		{"field changed resets values", map[string][]string{"field": {"bitrate"}, "prev": {"added"}, "op": {"more_than_days"}, "number": {"90"}}, policy.Condition{}, "Enter a number of Mbps"},
		{"field changed to resolution", map[string][]string{"field": {"resolution"}, "prev": {"codec"}, "op": {"is"}, "list": {"h264"}}, policy.Condition{Field: "resolution", Op: "above", Text: "1080p"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string][]string{}
			for k, v := range tc.in {
				extra["c0."+k] = v
			}
			got, errs := policyFromForm(policyWith(extra))
			if tc.err != "" {
				if !strings.Contains(errs["c0"], tc.err) {
					t.Fatalf("error %q, want it to contain %q (all: %v)", errs["c0"], tc.err, errs)
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("errors %v", errs)
			}
			if len(got.Conditions.All) != 1 || !reflect.DeepEqual(got.Conditions.All[0], tc.want) {
				t.Fatalf("got %+v, want %+v", got.Conditions.All, tc.want)
			}
		})
	}
}

func TestPolicyFromFormConditionOrder(t *testing.T) {
	v := policyWith(map[string][]string{
		"c10.field": {"size"}, "c10.op": {"above"}, "c10.number": {"3"},
		"c2.field": {"favourite"}, "c2.op": {"is"}, "c2.bool": {"no"},
		"c5.field": {"added"}, "c5.op": {"less_than_days"}, "c5.number": {"x"},
		"cx.field": {"watched"}, "c-1.field": {"watched"}, "c3.op": {"is"}, // no field: ignored
	})
	p, errs := policyFromForm(v)
	fields := []string{}
	for _, c := range p.Conditions.All {
		fields = append(fields, c.Field)
	}
	if strings.Join(fields, ",") != "favourite,added,size" {
		t.Fatalf("fields %v", fields)
	}
	// Errors refer to the renumbered row.
	if errs["c1"] == "" || len(errs) != 1 {
		t.Fatalf("errors %v", errs)
	}
}

func TestPolicyFromFormScopeAndAction(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string][]string
		check func(t *testing.T, p policy.Policy, errs policyFormErrors)
	}{
		{"no scope means everything", nil, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || !reflect.DeepEqual(p.Scope, policy.Scope{Version: 1}) || p.Conditions.All == nil {
				t.Fatalf("%+v %v", p, errs)
			}
		}},
		{"films only drops series and seasons", map[string][]string{"type": {"Movie"}, "series": {"s1"}, "seasons": {"1"}}, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || p.Scope.Series != nil || p.Scope.Seasons != nil || p.Scope.Types[0] != "Movie" {
				t.Fatalf("%+v %v", p.Scope, errs)
			}
		}},
		{"seasons ignored with two series", map[string][]string{"series": {"s1", "s2"}, "seasons": {"nonsense"}}, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || len(p.Scope.Series) != 2 || p.Scope.Seasons != nil {
				t.Fatalf("%+v %v", p.Scope, errs)
			}
		}},
		{"bad seasons", map[string][]string{"series": {"s1"}, "seasons": {"1, two"}}, func(t *testing.T, _ policy.Policy, errs policyFormErrors) {
			if errs["seasons"] == "" {
				t.Fatalf("errors %v", errs)
			}
		}},
		{"bad type", map[string][]string{"type": {"Audio"}}, func(t *testing.T, _ policy.Policy, errs policyFormErrors) {
			if errs["type"] == "" {
				t.Fatalf("errors %v", errs)
			}
		}},
		{"no name", map[string][]string{"name": {"   "}}, func(t *testing.T, _ policy.Policy, errs policyFormErrors) {
			if errs["name"] != "Give the policy a name." {
				t.Fatalf("errors %v", errs)
			}
		}},
		{"long name", map[string][]string{"name": {strings.Repeat("é", 101)}}, func(t *testing.T, _ policy.Policy, errs policyFormErrors) {
			if errs["name"] == "" {
				t.Fatalf("errors %v", errs)
			}
		}},
		{"disabled", map[string][]string{"enabled": nil}, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || p.Enabled {
				t.Fatalf("%+v %v", p, errs)
			}
		}},
		{"protect ignores the rest", map[string][]string{"kind": {"protect"}, "codec": {"av1"}, "encoder": {"x"}, "allow_hdr_reduction": {"on"}}, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || !reflect.DeepEqual(p.Action, policy.Action{Version: 1, Kind: "protect"}) {
				t.Fatalf("%+v %v", p.Action, errs)
			}
		}},
		{"defaults when missing", map[string][]string{"kind": nil, "max_resolution": nil, "codec": nil, "quality": nil, "encoder": nil}, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || !reflect.DeepEqual(p.Action, policyOptimise("keep", "hevc", "high")) {
				t.Fatalf("%+v %v", p.Action, errs)
			}
		}},
		{"keep and keep changes nothing", map[string][]string{"max_resolution": {"keep"}, "codec": {"keep"}}, func(t *testing.T, _ policy.Policy, errs policyFormErrors) {
			if !strings.Contains(errs["action"], "would change nothing") {
				t.Fatalf("errors %v", errs)
			}
		}},
		{"reduce resolution keeping codec", map[string][]string{"max_resolution": {"720p"}, "codec": {"keep"}}, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || p.Action.Codec != "keep" || p.Action.MaxResolution != "720p" {
				t.Fatalf("%+v %v", p.Action, errs)
			}
		}},
		{"av1 is planned", map[string][]string{"codec": {"av1"}}, func(t *testing.T, _ policy.Policy, errs policyFormErrors) {
			if !strings.Contains(errs["codec"], "planned") {
				t.Fatalf("errors %v", errs)
			}
		}},
		{"unknown values", map[string][]string{"kind": {"delete"}, "max_resolution": {"4320p"}, "quality": {"ultra"}, "encoder": {"nvenc"}, "codec": {"vp9"}}, func(t *testing.T, _ policy.Policy, errs policyFormErrors) {
			for _, k := range []string{"kind", "max_resolution", "quality", "encoder", "codec"} {
				if errs[k] == "" {
					t.Errorf("no error for %s: %v", k, errs)
				}
			}
		}},
		{"every quality", map[string][]string{"quality": {"maximum"}}, func(t *testing.T, p policy.Policy, errs policyFormErrors) {
			if len(errs) != 0 || p.Action.Quality != "maximum" {
				t.Fatalf("%+v %v", p.Action, errs)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, errs := policyFromForm(policyWith(tc.extra))
			tc.check(t, p, errs)
		})
	}
}

func TestParseSeasons(t *testing.T) {
	cases := []struct {
		in   string
		want []int
		ok   bool
	}{
		{"", nil, true},
		{"  ", nil, true},
		{"1", []int{1}, true},
		{"1, 2", []int{1, 2}, true},
		{"2 1", []int{1, 2}, true},
		{"3,1;2", []int{1, 2, 3}, true},
		{"0", []int{0}, true},
		{"2-4, 1", []int{1, 2, 3, 4}, true},
		{"2,2,2", []int{2}, true},
		{"x", nil, false},
		{"1.5", nil, false},
		{"-1", nil, false},
		{"4-2", nil, false},
		{"1-", nil, false},
		{"1000", nil, false},
		{"1-500", nil, false},
	}
	for _, tc := range cases {
		got, err := parseSeasons(tc.in)
		if (err == nil) != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseSeasons(%q) = %v, %v; want %v ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

// policyFormValues writes a form back as the browser would post it.
func policyFormValues(f policyForm) url.Values {
	v := url.Values{"name": {f.Name}, "type": {policyOr(f.Type, "both")}, "seasons": {f.Seasons},
		"library": f.Libraries, "series": f.Series, "collection": f.Collections,
		"kind": {f.Kind}, "max_resolution": {f.MaxRes}, "codec": {f.Codec}, "quality": {f.Quality}, "encoder": {f.Encoder}}
	if f.Enabled {
		v.Set("enabled", "on")
	}
	if f.AllowHDR {
		v.Set("allow_hdr_reduction", "on")
	}
	for i, c := range f.Conds {
		p := "c" + strconv.Itoa(i) + "."
		v.Set(p+"field", c.Field)
		v.Set(p+"prev", c.Field)
		v.Set(p+"op", c.Op)
		v.Set(p+"bool", c.Bool)
		v.Set(p+"number", c.Number)
		v.Set(p+"text", c.Text)
		v.Set(p+"words", c.Words)
		v[p+"list"] = c.List
	}
	return v
}

func TestPolicyFormRoundTrip(t *testing.T) {
	ps := policy.Starter()
	ps = append(ps, policy.Policy{
		Name: "Everything", Enabled: true,
		Scope: policy.Scope{Version: 1, Libraries: []string{"a"}, Types: []string{"Episode"}, Series: []string{"s"}, Seasons: []int{1, 3}, Collections: []string{"c"}},
		Conditions: policy.Conditions{Version: 1, All: []policy.Condition{
			{Field: "added", Op: "less_than_days", Number: 30},
			{Field: "bitrate", Op: "above", Number: 12.5},
			{Field: "size", Op: "below", Number: 2},
			{Field: "hdr", Op: "is_not", List: []string{"sdr", "dv-hdr10", "dv"}},
			{Field: "tag", Op: "has_not", List: []string{"keep", "kids"}},
			{Field: "genre", Op: "is", List: []string{"Drama"}},
			{Field: "codec", Op: "is_not", List: []string{"hevc", "av1"}},
		}},
		Action: policy.Action{Version: 1, Kind: "optimise", MaxResolution: "480p", Codec: "keep", Quality: "balanced",
			Encoder: "auto", Audio: "preserve", Subtitles: "preserve", AllowHDRReduction: true},
	})
	for _, want := range ps {
		want.Priority = 0
		got, errs := policyFromForm(policyFormValues(policyFormFrom(want)))
		if len(errs) != 0 {
			t.Fatalf("%s: errors %v", want.Name, errs)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got  %+v\n want %+v", want.Name, got, want)
		}
	}
}

func TestPolicyApplyOp(t *testing.T) {
	st := policyEditorState{form: readPolicyForm(policyWith(map[string][]string{
		"c0.field": {"watched"}, "c0.op": {"is"}, "c0.bool": {"yes"},
		"c1.field": {"size"}, "c1.op": {"above"}, "c1.number": {"abc"},
	}))}
	if !policyApplyOp("add", &st) || len(st.form.Conds) != 3 || st.focus != "c2-field" {
		t.Fatalf("add: %+v", st)
	}
	if !policyApplyOp("remove-0", &st) || len(st.form.Conds) != 2 || st.form.Conds[0].Number != "abc" {
		t.Fatalf("remove: %+v", st.form.Conds)
	}
	if !policyApplyOp("remove-9", &st) || len(st.form.Conds) != 2 {
		t.Fatalf("remove out of range: %+v", st.form.Conds)
	}
	if !policyApplyOp("refresh", &st) || policyApplyOp("save", &st) || policyApplyOp("", &st) {
		t.Fatal("refresh redraws; save and empty do not")
	}
}

// ---------- Handlers ----------

type policyTestEncoders struct{}

func (policyTestEncoders) Select(media.Codec, bool, string) (string, bool, string) {
	return "x265", true, ""
}

type policyTestEnv struct {
	srv *Server
	st  *store.Store
	lib *library.Service
	h   http.Handler
}

// policySetup builds a server on a real store with three probed films and
// one episode. Alpha (H.264 1080p) is a favourite; Charlie is H.264 2160p;
// Bravo is HEVC 1080p. Only "Protect favourites" is stored.
func policySetup(t *testing.T) policyTestEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	lib := library.New(library.Options{Store: st, Encoders: policyTestEncoders{}, Now: func() time.Time { return now }})
	t.Cleanup(func() {
		policyWaitIdle(t, lib)
		_ = st.Close()
	})
	root := t.TempDir()
	policyMust(t, st.SetSetting(ctx, store.KeySetupComplete, "true"))
	policyMust(t, st.ReplaceLibraries(ctx, []store.Library{
		{ID: "lm", Name: "Movies", CollectionType: "movies"}, {ID: "lt", Name: "TV", CollectionType: "tvshows"},
	}))
	policyMust(t, st.SetManagedLibraries(ctx, []string{"lm", "lt"}))
	policyMust(t, st.ReplaceUsers(ctx, []store.JellyfinUser{{ID: "u1", Name: "alex"}}))
	policyMust(t, st.SetSelectedUsers(ctx, []string{"u1"}))
	policyMust(t, st.ReplacePathMappings(ctx, []store.PathMapping{{JellyfinPrefix: "/media", LocalPrefix: filepath.ToSlash(root)}}))
	season := 1
	items := []struct {
		item    store.Item
		fixture string
	}{
		{store.Item{ID: "alpha", LibraryID: "lm", Type: "Movie", Name: "Alpha"}, "h264-1080p"},
		{store.Item{ID: "bravo", LibraryID: "lm", Type: "Movie", Name: "Bravo"}, "hevc-1080p"},
		{store.Item{ID: "charlie", LibraryID: "lm", Type: "Movie", Name: "Charlie"}, "h264-2160p"},
		{store.Item{ID: "mike1", LibraryID: "lt", Type: "Episode", Name: "Pilot", SeriesID: "mike", SeriesName: "Mike Show", SeasonNumber: &season}, "tv-episode"},
	}
	syncID, err := st.StartSync(ctx)
	policyMust(t, err)
	var rows []store.Item
	for _, it := range items {
		it.item.JellyfinPath = "/media/" + it.item.ID + ".mkv"
		it.item.LocalPath = filepath.ToSlash(filepath.Join(root, it.item.ID+".mkv"))
		rows = append(rows, it.item)
	}
	policyMust(t, st.UpsertItems(ctx, syncID, rows))
	_, err = st.FinishSync(ctx, syncID, true, len(rows), "")
	policyMust(t, err)
	for i, it := range items {
		policySaveProbe(t, st, rows[i], it.fixture)
	}
	policyMust(t, st.ReplaceUserData(ctx, "u1", []store.UserData{{ItemID: "alpha", UserID: "u1", Favourite: true}}))
	ps := policy.Starter()[:1]
	if _, err := lib.SavePolicy(ctx, ps[0]); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Store: st, Library: lib, Now: func() time.Time { return now }})
	return policyTestEnv{srv: srv, st: st, lib: lib, h: srv.Handler()}
}

func policySaveProbe(t *testing.T, st *store.Store, it store.Item, fixture string) {
	t.Helper()
	dir := filepath.Join("..", "media", "testdata", "probe")
	probe, err := os.ReadFile(filepath.Join(dir, fixture+".json"))
	policyMust(t, err)
	frames, _ := os.ReadFile(filepath.Join(dir, fixture+".frames.json"))
	f, err := media.Parse(probe, frames, 0)
	policyMust(t, err)
	policyMust(t, os.WriteFile(it.LocalPath, []byte("x"), 0o644))
	policyMust(t, os.Truncate(it.LocalPath, f.Size))
	policyMust(t, st.SaveProbe(context.Background(), store.Probe{
		ItemID: it.ID, LocalPath: it.LocalPath, FileIdentity: store.FileIdentity{Size: f.Size, Inode: 1},
		Nlink: 1, ProbeJSON: string(probe), FrameJSON: string(frames),
	}))
}

func policyMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// policyWaitIdle waits for a background evaluation to finish, so it never
// outlives the test's database.
func policyWaitIdle(t *testing.T, lib *library.Service) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for lib.Status().Running {
		if time.Now().After(deadline) {
			t.Fatal("evaluation still running")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e policyTestEnv) do(t *testing.T, method, target string, form url.Values, htmx bool) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res, string(b)
}

func (e policyTestEnv) policies(t *testing.T) []policy.Policy {
	t.Helper()
	ps, err := e.lib.Policies(context.Background())
	policyMust(t, err)
	return ps
}

func policyH264Form(name string) url.Values {
	return policyWith(map[string][]string{
		"name": {name}, "library": {"lm"}, "max_resolution": {"keep"},
		"c0.field": {"codec"}, "c0.op": {"is"}, "c0.list": {"h264"},
	})
}

func TestPolicyPagesRender(t *testing.T) {
	e := policySetup(t)
	id := e.policies(t)[0].ID
	for _, target := range []string{"/policies", "/policies/new", "/policies/" + strconv.FormatInt(id, 10)} {
		res, body := e.do(t, "GET", target, nil, false)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, `class="wordmark"`) {
			t.Fatalf("%s: status %d", target, res.StatusCode)
		}
	}
	_, body := e.do(t, "GET", "/policies", nil, false)
	for _, want := range []string{"Protect favourites", "In every managed library, when a favourite: never change these files.", "How policies work", `id="policy-list"`, templEscape(`Delete "Protect favourites"?`)} {
		if !strings.Contains(body, want) {
			t.Errorf("list is missing %q", want)
		}
	}
	for _, target := range []string{"/policies/999", "/policies/abc"} {
		if res, _ := e.do(t, "GET", target, nil, false); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, res.StatusCode)
		}
	}
}

func TestPolicyListEmpty(t *testing.T) {
	e := policySetup(t)
	policyMust(t, e.st.DeletePolicy(context.Background(), e.policies(t)[0].ID))
	_, body := e.do(t, "GET", "/policies", nil, false)
	if !strings.Contains(body, "No policies yet") {
		t.Fatal("empty state missing")
	}
}

func TestPolicyCreateAndEdit(t *testing.T) {
	e := policySetup(t)
	res, _ := e.do(t, "POST", "/policies", policyH264Form("Efficient films"), false)
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/policies?saved=") {
		t.Fatalf("create: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	policyWaitIdle(t, e.lib)
	ps := e.policies(t)
	if len(ps) != 2 || ps[1].Name != "Efficient films" || ps[1].Conditions.All[0].List[0] != "h264" || ps[1].Priority <= ps[0].Priority {
		t.Fatalf("policies %+v", ps)
	}
	_, body := e.do(t, "GET", res.Header.Get("Location"), nil, false)
	if !strings.Contains(body, "Saved Efficient films") || !strings.Contains(body, "In Movies, when codec is H.264") {
		t.Fatal("saved notice or sentence missing")
	}

	id := strconv.FormatInt(ps[1].ID, 10)
	form := policyH264Form("Renamed")
	form.Set("id", id)
	res, _ = e.do(t, "POST", "/policies/"+id, form, false)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit: %d", res.StatusCode)
	}
	policyWaitIdle(t, e.lib)
	if after := e.policies(t); len(after) != 2 || after[1].Name != "Renamed" || after[1].ID != ps[1].ID {
		t.Fatalf("after edit %+v", after)
	}
	if res, _ := e.do(t, "POST", "/policies/999", form, false); res.StatusCode != http.StatusNotFound {
		t.Fatalf("edit missing: %d", res.StatusCode)
	}
}

func TestPolicySaveErrorKeepsInput(t *testing.T) {
	e := policySetup(t)
	form := policyWith(map[string][]string{
		"name": {""}, "library": {"lt"}, "c0.field": {"last_watched"}, "c0.op": {"more_than_days"}, "c0.number": {"ninety"},
		"c1.field": {"tag"}, "c1.op": {"has"}, "c1.words": {"keep, <b>x</b>"},
	})
	res, body := e.do(t, "POST", "/policies", form, false)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, want := range []string{
		"The policy was not saved", "Give the policy a name.", `value="ninety"`, `aria-invalid="true"`,
		`value="keep, &lt;b&gt;x&lt;/b&gt;"`, "Condition 1: Enter a number, such as 90.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q", want)
		}
	}
	if !strings.Contains(body, `value="lt" checked`) {
		t.Error("library choice lost")
	}
	if len(e.policies(t)) != 1 {
		t.Fatal("an invalid policy was saved")
	}
}

func TestPolicyAddAndRemoveConditions(t *testing.T) {
	e := policySetup(t)
	form := policyH264Form("")
	form.Set("op", "add")
	res, body := e.do(t, "POST", "/policies", form, true)
	if res.StatusCode != http.StatusOK || strings.Contains(body, `class="wordmark"`) {
		t.Fatalf("HTMX add should return the form alone: %d", res.StatusCode)
	}
	if !strings.Contains(body, `id="c1-field"`) || !strings.Contains(body, "autofocus") || !strings.Contains(body, "load, input delay:500ms") {
		t.Fatal("new condition row or focus missing")
	}
	// Without JavaScript the whole page comes back, with the preview.
	res, body = e.do(t, "POST", "/policies", form, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `class="wordmark"`) || !strings.Contains(body, "Would optimise") {
		t.Fatalf("full-page add: %d", res.StatusCode)
	}
	form.Set("op", "remove-0")
	_, body = e.do(t, "POST", "/policies", form, true)
	if strings.Contains(body, `id="c0-field"`) || !strings.Contains(body, "No conditions yet") {
		t.Fatal("condition not removed")
	}
	if len(e.policies(t)) != 1 {
		t.Fatal("add or remove saved the policy")
	}
}

func TestPolicyToggle(t *testing.T) {
	e := policySetup(t)
	p := e.policies(t)[0]
	id := strconv.FormatInt(p.ID, 10)
	res, body := e.do(t, "POST", "/policies/"+id+"/toggle", url.Values{}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `id="policy-`+id+`"`) || !strings.Contains(body, "Protect favourites turned off.") {
		t.Fatalf("toggle off: %d %s", res.StatusCode, body)
	}
	policyWaitIdle(t, e.lib)
	if e.policies(t)[0].Enabled {
		t.Fatal("still enabled")
	}
	res, _ = e.do(t, "POST", "/policies/"+id+"/toggle", url.Values{"enabled": {"on"}}, false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/policies#policy-"+id {
		t.Fatalf("toggle on: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	policyWaitIdle(t, e.lib)
	if !e.policies(t)[0].Enabled {
		t.Fatal("not enabled")
	}
	if res, _ := e.do(t, "POST", "/policies/999/toggle", url.Values{}, true); res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing: %d", res.StatusCode)
	}
}

func TestPolicyMove(t *testing.T) {
	e := policySetup(t)
	res, _ := e.do(t, "POST", "/policies", policyH264Form("Second"), false)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", res.StatusCode)
	}
	policyWaitIdle(t, e.lib)
	second := e.policies(t)[1]
	id := strconv.FormatInt(second.ID, 10)
	res, body := e.do(t, "POST", "/policies/"+id+"/move", url.Values{"dir": {"up"}}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Second moved to position 1.") {
		t.Fatalf("move: %d %s", res.StatusCode, body)
	}
	// At the top the Up button is gone, so focus moves to Down.
	if !strings.Contains(body, `id="policy-`+id+`-down"`) || strings.Contains(body, `id="policy-`+id+`-up"`) {
		t.Fatal("move buttons wrong at the top")
	}
	policyWaitIdle(t, e.lib)
	if ps := e.policies(t); ps[0].ID != second.ID {
		t.Fatalf("order %+v", ps)
	}
	res, _ = e.do(t, "POST", "/policies/"+id+"/move", url.Values{"dir": {"down"}}, false)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("move without JS: %d", res.StatusCode)
	}
	policyWaitIdle(t, e.lib)
	if ps := e.policies(t); ps[1].ID != second.ID {
		t.Fatalf("order %+v", ps)
	}
	if res, _ := e.do(t, "POST", "/policies/"+id+"/move", url.Values{"dir": {"sideways"}}, false); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad direction: %d", res.StatusCode)
	}
}

func TestPolicyDelete(t *testing.T) {
	e := policySetup(t)
	p := e.policies(t)[0]
	res, _ := e.do(t, "POST", "/policies/"+strconv.FormatInt(p.ID, 10)+"/delete", url.Values{}, false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/policies?deleted=Protect+favourites" {
		t.Fatalf("delete: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	policyWaitIdle(t, e.lib)
	if len(e.policies(t)) != 0 {
		t.Fatal("not deleted")
	}
	_, body := e.do(t, "GET", res.Header.Get("Location"), nil, false)
	if !strings.Contains(body, "Deleted Protect favourites") {
		t.Fatal("deleted notice missing")
	}
	if res, _ := e.do(t, "POST", "/policies/"+strconv.FormatInt(p.ID, 10)+"/delete", url.Values{}, false); res.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete: %d", res.StatusCode)
	}
}

func TestPolicyPreview(t *testing.T) {
	e := policySetup(t)
	ctx := context.Background()
	names, err := e.srv.loadPolicyNames(ctx)
	policyMust(t, err)

	// Alpha and Charlie are H.264; Alpha is a favourite, so the Protect
	// policy above decides it.
	v := e.srv.policyPreviewView(ctx, 0, readPolicyForm(policyH264Form("")), names)
	if !v.Ready || v.Matches != 2 || v.Wins != 1 || v.Higher != 1 || v.Optimise != 1 || len(v.Samples) != 1 {
		t.Fatalf("preview %+v", v)
	}
	if v.Samples[0].Href != "/library/charlie" || v.Current == "" || v.Saving == "" {
		t.Fatalf("sample or sizes %+v", v)
	}
	if !strings.HasPrefix(v.Sentence, "In Movies, when codec is H.264: convert to HEVC") {
		t.Fatalf("sentence %q", v.Sentence)
	}

	// Narrowing to 2160p changes the numbers.
	form := policyH264Form("")
	form.Set("c1.field", "resolution")
	form.Set("c1.op", "above")
	form.Set("c1.text", "1080p")
	if v := e.srv.policyPreviewView(ctx, 0, readPolicyForm(form), names); v.Matches != 1 || v.Wins != 1 {
		t.Fatalf("narrowed preview %+v", v)
	}

	// Editing the Protect policy previews it in its own place.
	protect := e.policies(t)[0]
	v = e.srv.policyPreviewView(ctx, protect.ID, policyFormFrom(protect), names)
	if !v.Protect || v.Matches != 1 || v.Wins != 1 {
		t.Fatalf("protect preview %+v", v)
	}

	// An incomplete policy explains itself instead.
	bad := policyH264Form("")
	bad.Del("c0.list")
	if v := e.srv.policyPreviewView(ctx, 0, readPolicyForm(bad), names); v.Ready || v.Problem == "" {
		t.Fatalf("incomplete preview %+v", v)
	}

	res, body := e.do(t, "POST", "/policies/preview", policyH264Form(""), true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Would optimise") || !strings.Contains(body, `href="/library/charlie"`) ||
		!strings.Contains(body, `hx-swap-oob="innerHTML"`) || !strings.Contains(body, "Estimated saving") {
		t.Fatalf("preview response %d: %s", res.StatusCode, body)
	}
}

func TestPolicyPreviewSampledSaysSo(t *testing.T) {
	render := func(v views.PolicyPreviewView) string {
		var b strings.Builder
		policyMust(t, views.PolicyPreview(v).Render(context.Background(), &b))
		return b.String()
	}
	res := library.Preview{
		Matches: 14_000, Wins: 12_345, Optimise: 9_000, Optimal: 3_000, Skipped: 345,
		Current: 90_000_000_000_000, AfterMin: 30_000_000_000_000, AfterMax: 40_000_000_000_000,
		Sampled: true, SampleSize: 1_000,
	}
	var v views.PolicyPreviewView
	policyPreviewResult(&v, res)
	body := render(v)
	for _, want := range []string{
		"Estimated from 1,000 of 12,345 items.", "Matches and the decided counts are exact.",
		"14,000", "12,345", "9,000",
	} {
		if !strings.Contains(body, templEscape(want)) {
			t.Errorf("sampled preview: missing %q", want)
		}
	}
	// Every outcome count and the current size carry the estimate mark;
	// Matches and the decided counts do not.
	if n := strings.Count(body, `<span class="visually-hidden">About </span>`); n != 7 {
		t.Errorf("sampled preview: %d estimate marks, want 7 (metric, three counts, three sizes)", n)
	}

	res.Sampled, res.SampleSize = false, 0
	v = views.PolicyPreviewView{}
	policyPreviewResult(&v, res)
	if body := render(v); strings.Contains(body, "Estimated from") {
		t.Error("a counted preview says it is estimated from a sample")
	}
}
