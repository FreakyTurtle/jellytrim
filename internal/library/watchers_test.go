package library

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

var watchNow = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

func activeDaysAgo(d int) *time.Time {
	t := watchNow.Add(-time.Duration(d) * 24 * time.Hour)
	return &t
}

// watchState is a compact view of countWatchers' result: counted names,
// reasons and notes by name, and the summary.
type watchState struct {
	counted []string
	reasons map[string]string
	notes   map[string]string
	summary string
}

func stateOf(w watchers) watchState {
	out := watchState{reasons: map[string]string{}, notes: map[string]string{}, summary: w.summary()}
	for _, u := range w.counted {
		out.counted = append(out.counted, u.Name)
	}
	for _, u := range w.users {
		if u.Counted != (u.Reason == "") {
			panic("a user is counted with a reason, or not counted without one: " + u.User.Name)
		}
		if u.Reason != "" {
			out.reasons[u.User.Name] = u.Reason
		}
		if u.Note != "" {
			out.notes[u.User.Name] = u.Note
		}
	}
	return out
}

func TestCountWatchers(t *testing.T) {
	five := []store.JellyfinUser{
		{ID: "a", Name: "alex", Selected: true, LastActivityAt: activeDaysAgo(10)},
		{ID: "d", Name: "dev", LastActivityAt: activeDaysAgo(96)},
		{ID: "r", Name: "robin", Selected: true},
		{ID: "s", Name: "sam", Selected: true, Disabled: true, LastActivityAt: activeDaysAgo(1)},
		{ID: "z", Name: "zed", Hidden: true, LastActivityAt: activeDaysAgo(90)}, // exactly 90 days: still active
	}
	allInactive := []store.JellyfinUser{
		{ID: "d", Name: "dev", LastActivityAt: activeDaysAgo(96)},
		{ID: "r", Name: "robin"},
		{ID: "s", Name: "sam", Disabled: true, LastActivityAt: activeDaysAgo(1)},
	}
	three := []store.JellyfinUser{{ID: "a", Name: "alex"}, {ID: "d", Name: "dev"}, {ID: "r", Name: "robin"}}
	set := func(users string, percent, days int) store.Settings {
		return store.Settings{WatchUsers: users, WatchPercent: percent, WatchInactiveDays: days}
	}
	cases := []struct {
		name  string
		users []store.JellyfinUser
		st    store.Settings
		want  watchState
	}{
		{"everyone leaves out disabled and inactive users", five, set("everyone", 0, 90), watchState{
			counted: []string{"alex", "zed"},
			reasons: map[string]string{"dev": "inactive for 96 days", "robin": "never used Jellyfin", "sam": "disabled in Jellyfin"},
			summary: "Counting 2 of 5 users (1 disabled, 2 inactive). A file counts as watched when any one of them has watched it.",
		}},
		{"selected counts only ticked, enabled, active users", five, set("selected", 51, 90), watchState{
			counted: []string{"alex"},
			reasons: map[string]string{"dev": "not selected", "robin": "never used Jellyfin", "sam": "disabled in Jellyfin", "zed": "not selected"},
			summary: "Counting 1 of 5 users (1 disabled, 2 not selected, 1 inactive). A file counts as watched when alex has watched it.",
		}},
		{"inactive filter off counts users who never used Jellyfin", five, set("everyone", 100, 0), watchState{
			counted: []string{"alex", "dev", "robin", "zed"},
			reasons: map[string]string{"sam": "disabled in Jellyfin"},
			summary: "Counting 4 of 5 users (1 disabled). A file counts as watched when all of them have watched it.",
		}},
		{"the inactive filter never removes everyone", allInactive, set("everyone", 51, 90), watchState{
			counted: []string{"dev", "robin"},
			reasons: map[string]string{"sam": "disabled in Jellyfin"},
			notes: map[string]string{
				"dev":   "inactive for 96 days; counted because none of the counted people has been active in the last 90 days",
				"robin": "never used Jellyfin; counted because none of the counted people has been active in the last 90 days",
			},
			summary: "Counting 2 of 3 users (1 disabled). None of them has been active in the last 90 days, so inactive users are counted. " +
				"A file counts as watched when a majority of them have watched it.",
		}},
		{"nobody selected", three, set("selected", 0, 90), watchState{
			reasons: map[string]string{"alex": "not selected", "dev": "not selected", "robin": "not selected"},
			summary: "No users are counted (3 not selected), so watched and favourite conditions never match.",
		}},
		{"a custom share", three, set("everyone", 60, 0), watchState{
			counted: []string{"alex", "dev", "robin"},
			summary: "Counting all 3 users. A file counts as watched when at least 60% of them (2 of 3) have watched it.",
		}},
		{"the only user", three[1:2], set("everyone", 100, 0), watchState{
			counted: []string{"dev"},
			summary: "Counting the only user, dev. A file counts as watched when dev has watched it.",
		}},
		{"no users known", nil, set("everyone", 0, 90), watchState{
			summary: "No Jellyfin users are known yet. They are read from Jellyfin at the next sync.",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stateOf(countWatchers(c.users, c.st, watchNow))
			if c.want.reasons == nil {
				c.want.reasons = map[string]string{}
			}
			if c.want.notes == nil {
				c.want.notes = map[string]string{}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got  %#v\nwant %#v", got, c.want)
			}
		})
	}
}

// Jellyfin IDs for users added to the fake server in these tests.
const (
	alexID  = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	samID   = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
	robinID = "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
)

func setSettings(t *testing.T, e env, kv map[string]string) {
	t.Helper()
	must(t, e.st.SetSettings(context.Background(), kv))
}

// watchedLine evaluates "watched is yes" against the named item as the
// policies would see it now.
func watchedLine(t *testing.T, e env, name string) policy.Line {
	t.Helper()
	return conditionLine(t, e, name, policy.FieldWatched)
}

// conditionLine is the explanation line for "<field> is true" on the named
// item.
func conditionLine(t *testing.T, e env, name, field string) policy.Line {
	t.Helper()
	ctx := context.Background()
	it, err := e.st.Item(ctx, mustItemID(t, e, name))
	must(t, err)
	c, _, err := e.svc.loadCandidate(ctx, it, nil)
	must(t, err)
	p := policy.Policy{Conditions: policy.Conditions{All: []policy.Condition{
		{Field: field, Op: policy.OpIs, Bool: policy.B(true)}}}}
	return policy.Check(p, c.Policy, e.svc.now()).Lines[0]
}

func countedNames(t *testing.T, e env) map[string]string {
	t.Helper()
	us, err := e.svc.WatchUsers(context.Background())
	must(t, err)
	out := map[string]string{}
	for _, u := range us {
		out[u.User.Name] = u.Reason
		if u.Counted {
			out[u.User.Name] = "counted"
		}
	}
	return out
}

func itemWatchFor(t *testing.T, e env, name, user string) ItemWatch {
	t.Helper()
	ws, err := e.svc.ItemWatchState(context.Background(), mustItemID(t, e, name))
	must(t, err)
	for _, w := range ws {
		if w.User.Name == user {
			return w
		}
	}
	t.Fatalf("no watch state for %s", user)
	return ItemWatch{}
}

func TestNewJellyfinUserCountsInEveryoneModeOnly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	setSettings(t, e, map[string]string{store.KeyWatchInactiveDays: "0", store.KeyWatchPercent: "100"})
	_, err := e.svc.Run(ctx)
	must(t, err)
	if got := watchedLine(t, e, "Alpha"); !got.Pass || got.Text != "watched" {
		t.Fatalf("dev alone watched Alpha: %+v", got)
	}

	// A user added in Jellyfin after the first sync, who watched Alpha and
	// Bravo.
	played := time.Date(2026, 12, 1, 20, 0, 0, 0, time.UTC)
	e.jf.AddUser(jellyfintest.User{ID: alexID, Name: "alex"})
	alpha, bravo := mustItemID(t, e, "Alpha"), mustItemID(t, e, "Bravo")
	e.jf.SetUserData(alexID, alpha, jellyfintest.UserData{Played: true, PlayCount: 2, LastPlayedDate: &played})
	e.jf.SetUserData(alexID, bravo, jellyfintest.UserData{Played: true, PlayCount: 1, LastPlayedDate: &played, IsFavorite: true})
	_, err = e.svc.Run(ctx)
	must(t, err)
	if got := countedNames(t, e); !reflect.DeepEqual(got, map[string]string{"alex": "counted", "dev": "counted"}) {
		t.Fatalf("everyone mode: %v", got)
	}
	if got := watchedLine(t, e, "Alpha"); !got.Pass || got.Text != "watched by every counted user" {
		t.Fatalf("Alpha: %+v", got)
	}
	if got := watchedLine(t, e, "Bravo"); got.Pass || got.Text != "watched by 1 of 2 users (everyone needed)" {
		t.Fatalf("Bravo: %+v", got)
	}
	w := itemWatchFor(t, e, "Bravo", "alex")
	if !w.Counted || !w.Played || w.PlayCount != 1 || !w.Favourite || w.LastPlayed == nil || !w.LastPlayed.Equal(played) {
		t.Fatalf("alex's view of Bravo: %+v", w)
	}

	// In selected mode only the ticked user (dev) counts, and the next sync
	// forgets alex's watch state.
	setSettings(t, e, map[string]string{store.KeyWatchUsers: store.WatchUsersSelected})
	if got := countedNames(t, e); !reflect.DeepEqual(got, map[string]string{"alex": "not selected", "dev": "counted"}) {
		t.Fatalf("selected mode: %v", got)
	}
	if got := watchedLine(t, e, "Bravo"); got.Pass || got.Text != "not watched" {
		t.Fatalf("Bravo in selected mode: %+v", got)
	}
	_, err = e.svc.Run(ctx)
	must(t, err)
	ud, err := e.st.ItemUserData(ctx, bravo)
	must(t, err)
	for _, d := range ud {
		if d.UserID == alexID {
			t.Fatalf("alex's watch state kept after selected-mode sync: %+v", d)
		}
	}
	if w := itemWatchFor(t, e, "Bravo", "alex"); w.Counted || w.Reason != "not selected" || w.Played {
		t.Fatalf("alex's view of Bravo in selected mode: %+v", w)
	}
}

func TestInactiveUsersAreLeftOutAtEvaluation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// dev (from the fixtures) was last active on 2026-09-27, 95 days before
	// the test clock. sam is active; robin has never signed in.
	e.jf.AddUser(jellyfintest.User{ID: samID, Name: "sam", LastActivityDate: activeDaysAgo(12)})
	e.jf.AddUser(jellyfintest.User{ID: robinID, Name: "robin", IsHidden: true})
	must(t, e.svc.RefreshServerInfo(ctx))
	const bravo = "2fb4a183f1a88abb7f9defd79c21b0f2" // from the fixtures
	e.jf.SetUserData(samID, bravo, jellyfintest.UserData{Played: true, PlayCount: 1, LastPlayedDate: activeDaysAgo(12)})
	_, err := e.svc.Run(ctx)
	must(t, err)

	if got := countedNames(t, e); !reflect.DeepEqual(got, map[string]string{
		"dev": "inactive for 95 days", "robin": "never used Jellyfin", "sam": "counted"}) {
		t.Fatalf("default filter: %v", got)
	}
	us, err := e.svc.WatchUsers(ctx)
	must(t, err)
	for _, u := range us {
		if u.User.Name == "robin" && (!u.User.Hidden || u.User.LastActivityAt != nil) {
			t.Fatalf("robin %+v", u.User)
		}
		if u.User.Name == "sam" && (u.User.LastActivityAt == nil || !u.User.LastActivityAt.Equal(*activeDaysAgo(12))) {
			t.Fatalf("sam %+v", u.User)
		}
	}
	summary, err := e.svc.WatchSummary(ctx)
	must(t, err)
	if want := "Counting 1 of 3 users (2 inactive). A file counts as watched when sam has watched it."; summary != want {
		t.Fatalf("summary %q", summary)
	}
	if got := watchedLine(t, e, "Alpha"); got.Pass || got.Text != "not watched" {
		t.Fatalf("Alpha, watched only by inactive dev: %+v", got)
	}
	if w := itemWatchFor(t, e, "Alpha", "dev"); w.Counted || !w.Played || w.Reason != "inactive for 95 days" {
		t.Fatalf("dev's view of Alpha: %+v", w)
	}
	// dev's favourites still count, so Protect favourites keeps protecting.
	const charlie = "Charlie"
	e.jf.SetUserData(jellyfintest.FixtureUserID, mustItemID(t, e, charlie), jellyfintest.UserData{IsFavorite: true})
	_, err = e.svc.Run(ctx)
	must(t, err)
	if got := conditionLine(t, e, charlie, policy.FieldFavourite); !got.Pass {
		t.Fatalf("a favourite of inactive dev: %+v", got)
	}
	if w := itemWatchFor(t, e, charlie, "dev"); w.Counted || !w.Inactive || !w.Favourite {
		t.Fatalf("dev's view of Charlie: %+v", w)
	}

	// Turning the filter off needs no sync: dev's watch state is stored.
	setSettings(t, e, map[string]string{store.KeyWatchInactiveDays: "0"})
	if got := watchedLine(t, e, "Alpha"); !got.Pass || got.Text != "watched" {
		t.Fatalf("Alpha with the filter off: %+v", got)
	}

	// A filter that would leave nobody is not applied.
	setSettings(t, e, map[string]string{store.KeyWatchInactiveDays: "5", store.KeyWatchPercent: "51"})
	if got := countedNames(t, e); !reflect.DeepEqual(got, map[string]string{"dev": "counted", "robin": "counted", "sam": "counted"}) {
		t.Fatalf("fallback: %v", got)
	}
	summary, err = e.svc.WatchSummary(ctx)
	must(t, err)
	if want := "Counting all 3 users. None of them has been active in the last 5 days, so inactive users are counted. " +
		"A file counts as watched when a majority of them have watched it."; summary != want {
		t.Fatalf("fallback summary %q", summary)
	}
	if got := watchedLine(t, e, "Bravo"); got.Pass || got.Text != "watched by 1 of 3 users (majority needed)" {
		t.Fatalf("Bravo under the fallback: %+v", got)
	}
	if w := itemWatchFor(t, e, "Bravo", "sam"); !w.Counted || w.Note != "inactive for 12 days; counted because none of the counted people has been active in the last 5 days" {
		t.Fatalf("sam's note: %+v", w)
	}
}

func TestOldWatchModeStillApplies(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// An install whose settings still say "all" and have no percentage
	// (the web UI before it saves the new settings) needs every user.
	setSettings(t, e, map[string]string{store.KeyWatchMode: "all"})
	ec, err := e.svc.loadEvalContext(ctx)
	must(t, err)
	if ec.watch.Percent != policy.WatchEveryone {
		t.Fatalf("rule %+v", ec.watch)
	}
	wc, err := e.svc.WatchCount(ctx)
	must(t, err)
	if len(wc.Users) != 1 || wc.Users[0] != jellyfintest.FixtureUserID || wc.Needed != 1 {
		t.Fatalf("watch count %+v", wc)
	}
}
