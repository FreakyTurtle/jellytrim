package policy

import (
	"encoding/json"
	"fmt"
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

// users returns n counted users, the first played of them having played.
func users(n, played int) []UserState {
	out := make([]UserState, n)
	for i := range out {
		out[i] = UserState{UserID: fmt.Sprintf("u%d", i+1), Played: i < played}
		if out[i].Played {
			out[i].LastPlayed = daysAgo(200)
		}
	}
	return out
}

func TestWatchRuleNeeded(t *testing.T) {
	cases := []struct {
		percent, counted, want int
	}{
		{0, 0, 0}, {51, 0, 0}, {100, 0, 0},
		{0, 1, 1}, {0, 5, 1},
		{51, 1, 1}, {51, 2, 2}, {51, 3, 2}, {51, 4, 3}, {51, 5, 3},
		{51, 51, 26}, {51, 101, 51}, {51, 102, 52}, // more than half, not 51% rounded up
		{67, 1, 1}, {67, 2, 2}, {67, 3, 3}, {67, 4, 3}, {67, 5, 4},
		{100, 1, 1}, {100, 2, 2}, {100, 3, 3}, {100, 4, 4}, {100, 5, 5},
		{60, 3, 2}, {1, 5, 1}, {-10, 3, 1}, {150, 3, 3},
	}
	for _, c := range cases {
		if got := (WatchRule{Percent: c.percent}).Needed(c.counted); got != c.want {
			t.Errorf("%d%% of %d users: needed %d, want %d", c.percent, c.counted, got, c.want)
		}
	}
}

func TestWatchRuleLabel(t *testing.T) {
	for p, want := range map[int]string{0: "any one", 51: "a majority", 100: "everyone", 60: "at least 60%", 50: "at least 50%"} {
		if got := (WatchRule{Percent: p}).Label(); got != want {
			t.Errorf("%d: %q, want %q", p, got, want)
		}
	}
}

func TestWatchedByShareOfUsers(t *testing.T) {
	cases := []struct {
		name             string
		percent, n, seen int
		pass             bool
		text             string
	}{
		{"any one: one of three watched", 0, 3, 1, true, "watched"},
		{"any one: nobody watched", 0, 3, 0, false, "not watched"},
		{"one counted user keeps the short text under majority", 51, 1, 1, true, "watched"},
		{"one counted user keeps the short text under everyone", 100, 1, 0, false, "not watched"},
		{"majority: 1 of 2 is not enough", 51, 2, 1, false, "watched by 1 of 2 users (majority needed)"},
		{"majority: 2 of 2", 51, 2, 2, true, "watched by 2 of 2 users (majority needed)"},
		{"majority: 2 of 3", 51, 3, 2, true, "watched by 2 of 3 users (majority needed)"},
		{"majority: 1 of 3", 51, 3, 1, false, "watched by 1 of 3 users (majority needed)"},
		{"majority: 2 of 4 is only half", 51, 4, 2, false, "watched by 2 of 4 users (majority needed)"},
		{"majority: 3 of 4", 51, 4, 3, true, "watched by 3 of 4 users (majority needed)"},
		{"majority: 3 of 5", 51, 5, 3, true, "watched by 3 of 5 users (majority needed)"},
		{"majority: 2 of 5", 51, 5, 2, false, "watched by 2 of 5 users (majority needed)"},
		{"everyone: 2 of 2", 100, 2, 2, true, "watched by every counted user"},
		{"everyone: 1 of 2", 100, 2, 1, false, "watched by 1 of 2 users (everyone needed)"},
		{"everyone: 4 of 5", 100, 5, 4, false, "watched by 4 of 5 users (everyone needed)"},
		{"everyone: 5 of 5", 100, 5, 5, true, "watched by every counted user"},
		{"67%: 2 of 3 is short and shown rounded down", 67, 3, 2, false, "watched by 2 of 3 users (66%; 67% needed)"},
		{"67%: 3 of 3", 67, 3, 3, true, "watched by 3 of 3 users (100%; 67% needed)"},
		{"67%: 3 of 4", 67, 4, 3, true, "watched by 3 of 4 users (75%; 67% needed)"},
		{"67%: 1 of 2", 67, 2, 1, false, "watched by 1 of 2 users (50%; 67% needed)"},
		{"60%: 2 of 3", 60, 3, 2, true, "watched by 2 of 3 users (67%; 60% needed)"},
		{"60%: 1 of 3", 60, 3, 1, false, "watched by 1 of 3 users (33%; 60% needed)"},
		{"60%: 3 of 5 is exactly 60%", 60, 5, 3, true, "watched by 3 of 5 users (60%; 60% needed)"},
		{"60%: 1 of 2 is short", 60, 2, 1, false, "watched by 1 of 2 users (50%; 60% needed)"},
		{"34%: 1 of 3 is just short", 34, 3, 1, false, "watched by 1 of 3 users (33%; 34% needed)"},
		{"33%: 1 of 3 passes", 33, 3, 1, true, "watched by 1 of 3 users (33%; 33% needed)"},
		{"10% still needs one user", 10, 4, 0, false, "watched by 0 of 4 users (0%; 10% needed)"},
	}
	watched := Condition{Field: FieldWatched, Op: OpIs, Bool: B(true)}
	notWatched := Condition{Field: FieldWatched, Op: OpIs, Bool: B(false)}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := baseItem()
			it.Users, it.Watch = users(c.n, c.seen), WatchRule{Percent: c.percent}
			l := checkCondition(watched, it, now)
			if l.Pass != c.pass || l.Text != c.text {
				t.Fatalf("watched: got %v %q, want %v %q", l.Pass, l.Text, c.pass, c.text)
			}
			if l := checkCondition(notWatched, it, now); l.Pass == c.pass || l.Text != c.text {
				t.Fatalf("not watched: got %v %q", l.Pass, l.Text)
			}
		})
	}
}

func TestNoCountedUsersNeverMatchWatched(t *testing.T) {
	for _, p := range []int{0, 51, 100, 60} {
		it := baseItem()
		it.Users, it.Watch = nil, WatchRule{Percent: p}
		for _, want := range []bool{true, false} {
			l := checkCondition(Condition{Field: FieldWatched, Op: OpIs, Bool: B(want)}, it, now)
			if l.Pass || l.Text != "no Jellyfin users are counted for watch state" {
				t.Errorf("%d%%, watched is %v: %+v", p, want, l)
			}
		}
	}
}

func TestLastWatchedUsesMostRecentPlayByAnyone(t *testing.T) {
	more := Condition{Field: FieldLastWatched, Op: OpMoreThanDays, Number: 90}
	for _, p := range []int{0, 51, 100, 60} {
		it := baseItem()
		it.Watch = WatchRule{Percent: p}
		it.Users = []UserState{
			{UserID: "a", Played: true, LastPlayed: daysAgo(200)},
			{UserID: "b", Played: true, LastPlayed: daysAgo(3)},
			{UserID: "c"},
		}
		if l := checkCondition(more, it, now); l.Pass || l.Text != "last watched 3 days ago (not more than 90)" {
			t.Errorf("%d%%: a recent play by any user must reset the clock: %+v", p, l)
		}
		// A play by one user is enough to date the last viewing, even
		// when the rule says the item is not watched yet.
		it.Users = []UserState{{UserID: "a", Played: true, LastPlayed: daysAgo(120)}, {UserID: "b"}, {UserID: "c"}}
		if l := checkCondition(more, it, now); !l.Pass || l.Text != "last watched 120 days ago (more than 90)" {
			t.Errorf("%d%%: one user's play: %+v", p, l)
		}
		it.Users = nil
		if l := checkCondition(more, it, now); l.Pass || l.Text != "never watched" {
			t.Errorf("%d%%: no counted users: %+v", p, l)
		}
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
	// The watch rule applies to watched state only: one user's favourite
	// still counts, so Protect favourites is never weakened.
	for _, p := range []int{51, 100, 60} {
		it.Watch = WatchRule{Percent: p}
		if l := checkCondition(fav, it, now); !l.Pass || l.Text != "a favourite" {
			t.Fatalf("%d%%: one user's favourite must still count: %+v", p, l)
		}
		if checkCondition(notFav, it, now).Pass {
			t.Fatalf("%d%%: 'not a favourite' must not match when one user favourited it", p)
		}
	}
	it.Users = []UserState{{UserID: "a"}, {UserID: "b"}}
	if l := checkCondition(notFav, it, now); !l.Pass || l.Text != "not a favourite" {
		t.Fatalf("all: nobody's favourite: %+v", l)
	}
	it.Users = nil
	for _, c := range []Condition{fav, notFav} {
		if l := checkCondition(c, it, now); l.Pass || l.Text != "no Jellyfin users are counted for favourites" {
			t.Fatalf("no counted users must match neither favourite nor not favourite: %+v", l)
		}
	}
}

func TestProtectFavouritesWinsWhenEveryoneMustWatch(t *testing.T) {
	it := baseItem()
	it.Watch = WatchRule{Percent: WatchEveryone}
	it.Users = []UserState{{UserID: "a", Favourite: true}, {UserID: "b"}}
	res := Evaluate(Starter()[:1], it, now)
	if res.Winner == nil || res.Winner.Action.Kind != KindProtect {
		t.Fatalf("Protect favourites did not win: %+v", res.Matches)
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
	eff := Starter()[3]
	if got := Describe(eff, n); got != "In every managed library, when codec is H.264: convert to HEVC, keeping the resolution at High quality." {
		t.Fatalf("efficient: %q", got)
	}
	if got := Describe(Starter()[0], n); got != "In every managed library, when a favourite: never change these files." {
		t.Fatalf("protect: %q", got)
	}
}

func TestInactiveUsersKeepFavouritesAndLastWatched(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * 24 * time.Hour)
	it := Item{Watch: WatchRule{Percent: WatchEveryone}, Users: []UserState{
		{UserID: "a", Name: "alex", Played: true},
		{UserID: "s", Name: "sam", Favourite: true, Played: false, LastPlayed: &recent, Inactive: true},
	}}
	if got, text := watchedState(it); !got || text != "watched" {
		t.Errorf("an inactive user must not count towards the share: %v %q", got, text)
	}
	if got, _ := favouriteState(it); !got {
		t.Error("an inactive user's favourite must still count")
	}
	if last, ok := lastPlayed(it); !ok || !last.Equal(recent) {
		t.Errorf("an inactive user's play must still reset last watched: %v %v", last, ok)
	}
	onlyInactive := Item{Users: []UserState{{UserID: "s", Favourite: true, Inactive: true}}}
	if l := checkWatched(Condition{Field: FieldWatched, Op: OpIs, Bool: B(false)}, onlyInactive); l.Pass {
		t.Errorf("with nobody counted towards the share, watched conditions never match: %+v", l)
	}
	if l := checkFavourite(Condition{Field: FieldFavourite, Op: OpIs, Bool: B(true)}, onlyInactive); !l.Pass {
		t.Errorf("an inactive user's favourite must match: %+v", l)
	}
}
