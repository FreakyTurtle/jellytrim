package policy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func daysAgo(d int) *time.Time {
	t := now.Add(-time.Duration(d) * 24 * time.Hour)
	return &t
}

func file(codec media.Codec, w, h int, bitrate int64) *media.File {
	return &media.File{
		Size:      50_000_000_000,
		Duration:  2 * time.Hour,
		Container: media.Matroska,
		Video: []media.VideoStream{{
			Index: 0, Codec: codec, Width: w, Height: h, BitDepth: 8,
			Transfer: "bt709", Primaries: "bt709", Matrix: "bt709", BitRate: bitrate,
		}},
	}
}

func baseItem() Item {
	return Item{
		ID: "i1", Name: "Interstellar", Type: "Movie", LibraryID: "lib-movies", LibraryName: "Movies",
		DateAdded: daysAgo(203),
		Users:     []UserState{{UserID: "u1", Name: "alex", Played: true, LastPlayed: daysAgo(143)}},
		WatchMode: WatchAny,
		File:      file(media.CodecH264, 3840, 2160, 48_200_000),
	}
}

func archive() Policy {
	return Policy{
		ID: 1, Name: "Archive watched movies", Enabled: true, Priority: 20,
		Scope: Scope{Version: 1, Libraries: []string{"lib-movies"}},
		Conditions: Conditions{Version: 1, All: []Condition{
			{Field: FieldWatched, Op: OpIs, Bool: B(true)},
			{Field: FieldLastWatched, Op: OpMoreThanDays, Number: 90},
			{Field: FieldResolution, Op: OpAbove, Text: "1080p"},
			{Field: FieldFavourite, Op: OpIs, Bool: B(false)},
		}},
		Action: Action{Version: 1, Kind: KindOptimise, MaxResolution: "1080p", Codec: "hevc", Quality: QualityHigh},
	}
}

func TestExplanationMatchesBrief(t *testing.T) {
	m := Check(archive(), baseItem(), now)
	if !m.Matched {
		t.Fatalf("expected a match: %+v", m.Lines)
	}
	want := []Line{
		{true, "in Movies"},
		{true, "watched"},
		{true, "last watched 143 days ago (more than 90)"},
		{true, "2160p is above 1080p"},
		{true, "not a favourite"},
	}
	if !reflect.DeepEqual(m.Lines, want) {
		t.Fatalf("lines:\n got %+v\nwant %+v", m.Lines, want)
	}
}

func TestDayBoundaries(t *testing.T) {
	c := Condition{Field: FieldLastWatched, Op: OpMoreThanDays, Number: 90}
	exactly := now.Add(-90 * 24 * time.Hour)
	justOver := exactly.Add(-time.Second)
	cases := []struct {
		name string
		last *time.Time
		want bool
	}{
		{"exactly 90 days is not more than 90", &exactly, false},
		{"one second over matches", &justOver, true},
		{"recent does not match", daysAgo(5), false},
		{"never watched does not match", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := baseItem()
			it.Users = []UserState{{UserID: "u1", Played: tc.last != nil, LastPlayed: tc.last}}
			if got := checkCondition(c, it, now); got.Pass != tc.want {
				t.Fatalf("pass = %v (%q)", got.Pass, got.Text)
			}
		})
	}
	less := Condition{Field: FieldAdded, Op: OpLessThanDays, Number: 30}
	it := baseItem()
	it.DateAdded = daysAgo(10)
	if l := checkCondition(less, it, now); !l.Pass || l.Text != "added 10 days ago (less than 30)" {
		t.Fatalf("added less than: %+v", l)
	}
	it.DateAdded = nil
	if l := checkCondition(less, it, now); l.Pass || l.Text != "date added unknown" {
		t.Fatalf("unknown date must not match: %+v", l)
	}
}

func TestWatchModes(t *testing.T) {
	two := []UserState{{UserID: "a", Played: true, LastPlayed: daysAgo(200)}, {UserID: "b", Played: false}}
	watched := Condition{Field: FieldWatched, Op: OpIs, Bool: B(true)}
	it := baseItem()
	it.Users = two
	it.WatchMode = WatchAny
	if !checkCondition(watched, it, now).Pass {
		t.Fatal("any: one user watched should count as watched")
	}
	it.WatchMode = WatchAll
	l := checkCondition(watched, it, now)
	if l.Pass || l.Text != "watched by 1 of 2 selected users" {
		t.Fatalf("all: %+v", l)
	}
	it.Users = nil
	if checkCondition(watched, it, now).Pass {
		t.Fatal("no users selected must never match watched")
	}
	notWatched := Condition{Field: FieldWatched, Op: OpIs, Bool: B(false)}
	if checkCondition(notWatched, it, now).Pass {
		t.Fatal("no users selected must not match 'not watched' either")
	}
}

func TestLastWatchedUsesMostRecentPlayByAnyone(t *testing.T) {
	it := baseItem()
	it.WatchMode = WatchAll
	it.Users = []UserState{
		{UserID: "a", Played: true, LastPlayed: daysAgo(200)},
		{UserID: "b", Played: true, LastPlayed: daysAgo(3)},
	}
	c := Condition{Field: FieldLastWatched, Op: OpMoreThanDays, Number: 90}
	if checkCondition(c, it, now).Pass {
		t.Fatal("a recent play by any user must reset the clock")
	}
}

func TestFavourites(t *testing.T) {
	it := baseItem()
	it.Users = []UserState{{UserID: "a", Favourite: true}, {UserID: "b"}}
	fav := Condition{Field: FieldFavourite, Op: OpIs, Bool: B(true)}
	notFav := Condition{Field: FieldFavourite, Op: OpIs, Bool: B(false)}
	if !checkCondition(fav, it, now).Pass || checkCondition(notFav, it, now).Pass {
		t.Fatal("any: one user's favourite protects the item")
	}
	it.WatchMode = WatchAll
	if checkCondition(fav, it, now).Pass {
		t.Fatal("all: only one of two users favourited")
	}
}

func TestUnknownValuesNeverMatch(t *testing.T) {
	it := baseItem()
	it.File = nil
	it.Size = 0
	conds := []Condition{
		{Field: FieldResolution, Op: OpAbove, Text: "720p"},
		{Field: FieldResolution, Op: OpAtMost, Text: "2160p"},
		{Field: FieldCodec, Op: OpIs, List: []string{"h264"}},
		{Field: FieldCodec, Op: OpIsNot, List: []string{"hevc"}},
		{Field: FieldBitrate, Op: OpAbove, Number: 1},
		{Field: FieldBitrate, Op: OpBelow, Number: 1000},
		{Field: FieldSize, Op: OpAbove, Number: 1},
		{Field: FieldHDR, Op: OpIsNot, List: []string{"hdr10"}},
	}
	for _, c := range conds {
		if l := checkCondition(c, it, now); l.Pass {
			t.Errorf("%s %s matched an unprobed item: %q", c.Field, c.Op, l.Text)
		}
	}
	// Probed, but the bitrate cannot be worked out.
	it.File = file(media.CodecH264, 1920, 1080, 0)
	it.File.Size = 0
	it.File.Duration = 0
	it.File.BitRate = 0
	l := checkCondition(Condition{Field: FieldBitrate, Op: OpBelow, Number: 1000}, it, now)
	if l.Pass || !strings.Contains(l.Text, "unknown") {
		t.Fatalf("unknown bitrate: %+v", l)
	}
}

func TestResolutionCodecBitrateSizeHDR(t *testing.T) {
	it := baseItem()
	it.File = file(media.CodecHEVC, 1920, 800, 6_000_000)
	cases := []struct {
		c    Condition
		pass bool
		text string
	}{
		{Condition{Field: FieldResolution, Op: OpAbove, Text: "1080p"}, false, "1080p is not above 1080p"},
		{Condition{Field: FieldResolution, Op: OpAtMost, Text: "1080p"}, true, "1080p is at most 1080p"},
		{Condition{Field: FieldResolution, Op: OpIs, Text: "1080p"}, true, "resolution is 1080p"},
		{Condition{Field: FieldCodec, Op: OpIs, List: []string{"h264"}}, false, "codec is HEVC, not H.264"},
		{Condition{Field: FieldCodec, Op: OpIsNot, List: []string{"h264", "av1"}}, true, "codec is HEVC, not H.264 or AV1"},
		{Condition{Field: FieldBitrate, Op: OpAbove, Number: 20}, false, "bitrate 6 Mbps is not above 20 Mbps"},
		{Condition{Field: FieldBitrate, Op: OpBelow, Number: 20}, true, "bitrate 6 Mbps is below 20 Mbps"},
		{Condition{Field: FieldSize, Op: OpAbove, Number: 10}, true, "size 50 GB is above 10 GB"},
		{Condition{Field: FieldHDR, Op: OpIs, List: []string{"sdr"}}, true, "dynamic range is SDR"},
	}
	for _, tc := range cases {
		l := checkCondition(tc.c, it, now)
		if l.Pass != tc.pass || l.Text != tc.text {
			t.Errorf("%s %s: got {%v %q}, want {%v %q}", tc.c.Field, tc.c.Op, l.Pass, l.Text, tc.pass, tc.text)
		}
	}
}

func TestTagsAndGenres(t *testing.T) {
	it := baseItem()
	it.Tags = []string{"Keep", "4K"}
	it.Genres = []string{"Documentary"}
	if l := checkCondition(Condition{Field: FieldTag, Op: OpHas, List: []string{"keep"}}, it, now); !l.Pass {
		t.Fatalf("tag match should ignore case: %+v", l)
	}
	if l := checkCondition(Condition{Field: FieldTag, Op: OpHasNot, List: []string{"keep"}}, it, now); l.Pass {
		t.Fatalf("has_not: %+v", l)
	}
	it.Tags = nil
	if l := checkCondition(Condition{Field: FieldTag, Op: OpHasNot, List: []string{"keep"}}, it, now); !l.Pass {
		t.Fatalf("no tags: 'does not have' should pass: %+v", l)
	}
	if l := checkCondition(Condition{Field: FieldGenre, Op: OpIsNot, List: []string{"Animation"}}, it, now); !l.Pass {
		t.Fatalf("genre is_not: %+v", l)
	}
}

func TestScope(t *testing.T) {
	season := 4
	ep := Item{
		ID: "e1", Type: "Episode", LibraryID: "lib-tv", LibraryName: "TV", SeriesID: "s-csi", SeriesName: "CSI",
		SeasonNumber: &season, Collections: []string{"c1"},
	}
	s := Scope{Libraries: []string{"lib-tv"}, Types: []string{"Episode"}, Series: []string{"s-csi"}, Seasons: []int{4}, Collections: []string{"c1"}}
	for _, l := range checkScope(s, ep) {
		if !l.Pass {
			t.Fatalf("scope line failed: %+v", l)
		}
	}
	s.Series = []string{"s-other"}
	lines := checkScope(s, ep)
	if lines[2].Pass {
		t.Fatalf("other series should fail: %+v", lines[2])
	}
	movie := baseItem()
	if l := checkScope(Scope{Types: []string{"Episode"}}, movie); l[0].Pass || l[0].Text != "is a movie, which this policy does not cover" {
		t.Fatalf("type: %+v", l)
	}
}

func TestPrecedenceFirstEnabledMatchWins(t *testing.T) {
	protect := Policy{ID: 5, Name: "Protect favourites", Enabled: true, Priority: 10,
		Conditions: Conditions{All: []Condition{{Field: FieldFavourite, Op: OpIs, Bool: B(true)}}},
		Action:     Action{Kind: KindProtect}}
	disabled := Policy{ID: 6, Name: "Everything to 480p", Enabled: false, Priority: 15,
		Action: Action{Kind: KindOptimise, MaxResolution: "480p", Codec: "hevc"}}
	ps := []Policy{archive(), disabled, protect}
	Sort(ps)
	if ps[0].ID != 5 || ps[1].ID != 6 || ps[2].ID != 1 {
		t.Fatalf("sort order: %d %d %d", ps[0].ID, ps[1].ID, ps[2].ID)
	}

	it := baseItem()
	res := Evaluate(ps, it, now)
	if res.Winner == nil || res.Winner.ID != 1 {
		t.Fatalf("winner = %+v", res.Winner)
	}
	if !res.Matches[1].Matched || res.Matches[1].Enabled {
		t.Fatal("disabled policy should be reported as matched but not win")
	}

	it.Users[0].Favourite = true
	res = Evaluate(ps, it, now)
	if res.Winner == nil || res.Winner.ID != 5 {
		t.Fatalf("protect should win for a favourite, got %+v", res.Winner)
	}
	if res.Matches[2].Matched {
		t.Fatal("archive policy excludes favourites, so it should not match")
	}
}

func TestSamePriorityOrderedByID(t *testing.T) {
	a := Policy{ID: 2, Name: "b", Enabled: true, Priority: 1, Action: Action{Kind: KindProtect}}
	b := Policy{ID: 1, Name: "a", Enabled: true, Priority: 1, Action: Action{Kind: KindProtect}}
	ps := []Policy{a, b}
	Sort(ps)
	if Evaluate(ps, baseItem(), now).Winner.ID != 1 {
		t.Fatal("ties must break by ID")
	}
}

func TestEvaluationIsDeterministic(t *testing.T) {
	ps := Starter()
	for i := range ps {
		ps[i].ID = int64(i + 1)
		ps[i].Enabled = true
	}
	Sort(ps)
	first, _ := json.Marshal(Evaluate(ps, baseItem(), now).Matches)
	for i := 0; i < 20; i++ {
		again, _ := json.Marshal(Evaluate(ps, baseItem(), now).Matches)
		if string(again) != string(first) {
			t.Fatal("explanations differ between runs")
		}
	}
}

func TestValidate(t *testing.T) {
	if err := archive().Validate(); err != nil {
		t.Fatalf("archive: %v", err)
	}
	for _, p := range Starter() {
		if err := p.Validate(); err != nil {
			t.Errorf("starter %q: %v", p.Name, err)
		}
	}
	bad := []Policy{
		{Name: "", Action: Action{Kind: KindProtect}},
		{Name: "x", Action: Action{Kind: "delete"}},
		{Name: "x", Action: Action{Kind: KindOptimise, MaxResolution: "keep", Codec: "keep"}},
		{Name: "x", Action: Action{Kind: KindOptimise, MaxResolution: "900p", Codec: "hevc"}},
		{Name: "x", Action: Action{Kind: KindProtect}, Conditions: Conditions{All: []Condition{{Field: FieldWatched, Op: OpIs}}}},
		{Name: "x", Action: Action{Kind: KindProtect}, Conditions: Conditions{All: []Condition{{Field: FieldBitrate, Op: OpIs, Number: 1}}}},
		{Name: "x", Action: Action{Kind: KindProtect}, Conditions: Conditions{All: []Condition{{Field: FieldResolution, Op: OpAbove, Text: "big"}}}},
		{Name: "x", Action: Action{Kind: KindProtect}, Conditions: Conditions{All: []Condition{{Field: "colour", Op: OpIs}}}},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("bad policy %d validated", i)
		}
	}
}

type names map[string]string

func (n names) Library(id string) string    { return n[id] }
func (n names) Series(id string) string     { return n[id] }
func (n names) Collection(id string) string { return n[id] }

func TestDescribe(t *testing.T) {
	n := names{"lib-movies": "Movies", "s-csi": "CSI: Crime Scene Investigation", "lib-tv": "TV"}
	got := Describe(archive(), n)
	want := "In Movies, when watched, last watched more than 90 days ago, resolution above 1080p and not a favourite: convert to 1080p HEVC at High quality."
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	csi := Policy{Name: "CSI space saver",
		Scope:      Scope{Series: []string{"s-csi"}},
		Conditions: Conditions{All: []Condition{{Field: FieldResolution, Op: OpAbove, Text: "720p"}}},
		Action:     Action{Kind: KindOptimise, MaxResolution: "720p", Codec: "hevc", Quality: QualityBalanced}}
	if got := Describe(csi, n); got != "For CSI: Crime Scene Investigation, when resolution above 720p: convert to 720p HEVC at Balanced quality." {
		t.Fatalf("csi: %q", got)
	}
	eff := Starter()[2]
	if got := Describe(eff, n); got != "In every managed library, when codec is H.264: convert to HEVC, keeping the resolution at High quality." {
		t.Fatalf("efficient: %q", got)
	}
	if got := Describe(Starter()[0], n); got != "In every managed library, when a favourite: never change these files." {
		t.Fatalf("protect: %q", got)
	}
}
