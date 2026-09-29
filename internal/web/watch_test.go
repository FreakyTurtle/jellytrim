package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// watchUsers adds alex, sam and robin (inactive for 200 days) and kim
// (disabled) to the fixture's dev (last active 96 days before the test
// clock), and gives them a spread of watch state:
//
//	Alpha:   dev (fixture), alex, sam      3 of 4 with the filter off
//	Echo:    dev, alex, sam, robin         4 of 4
//	Charlie: robin                         1 of 4
//	Bravo:   dev's favourite (fixture)
func watchUsers(t *testing.T, e libraryTestEnv) {
	t.Helper()
	ctx := context.Background()
	now := e.srv.Now()
	recent, old := now.Add(-48*time.Hour), now.Add(-200*24*time.Hour)
	e.jf.AddUser(jellyfintest.User{ID: "alex", Name: "alex", LastActivityDate: &recent})
	e.jf.AddUser(jellyfintest.User{ID: "sam", Name: "sam", LastActivityDate: &recent})
	e.jf.AddUser(jellyfintest.User{ID: "robin", Name: "robin", LastActivityDate: &old})
	e.jf.AddUser(jellyfintest.User{ID: "kim", Name: "kim", IsDisabled: true, LastActivityDate: &recent})
	played := func(user, item string, plays int) {
		e.jf.SetUserData(user, item, jellyfintest.UserData{Played: true, PlayCount: plays, LastPlayedDate: &recent})
	}
	alpha, echo, charlie := e.itemID(t, "Alpha"), e.itemID(t, "Echo"), e.itemID(t, "Charlie")
	played("alex", alpha, 2)
	played("sam", alpha, 1)
	for _, u := range []string{jellyfintest.FixtureUserID, "alex", "sam", "robin"} {
		played(u, echo, 1)
	}
	played("robin", charlie, 1)
	e.jf.SetUserData("kim", charlie, jellyfintest.UserData{Played: true, PlayCount: 1, IsFavorite: true})
	libraryTestMust(t, e.st.SetSettings(ctx, map[string]string{store.KeyWatchUsers: store.WatchUsersEveryone}))
	libraryTestMust(t, e.lib.RefreshServerInfo(ctx))
	_, err := e.lib.Run(ctx)
	libraryTestMust(t, err)
}

func watchSet(t *testing.T, e libraryTestEnv, percent, inactive int) {
	t.Helper()
	libraryTestMust(t, e.st.SetSettings(context.Background(), map[string]string{
		store.KeyWatchPercent:      strconv.Itoa(percent),
		store.KeyWatchInactiveDays: strconv.Itoa(inactive),
	}))
}

// watchedTitles is the films the Library lists as watched.
func watchedTitles(t *testing.T, e libraryTestEnv) []string {
	t.Helper()
	res, body := e.get(t, "/library?watched=yes", false)
	expectStatus(t, res, body, http.StatusOK)
	var out []string
	for _, name := range []string{"Alpha (2019)", "Bravo (2020)", "Charlie (2021)", "Echo (2022)"} {
		if strings.Contains(body, name) {
			out = append(out, name)
		}
	}
	return out
}

func TestLibraryWatchedFilterFollowsTheRule(t *testing.T) {
	e := newLibraryTestEnv(t)
	watchUsers(t, e)
	cases := []struct {
		name              string
		percent, inactive int
		want              string
	}{
		// Filter off: dev, alex, sam and robin count.
		{"any one", 0, 0, "Alpha (2019),Charlie (2021),Echo (2022)"},
		{"majority", 51, 0, "Alpha (2019),Echo (2022)"},
		{"everyone", 100, 0, "Echo (2022)"},
		{"custom 60", 60, 0, "Alpha (2019),Echo (2022)"},
		// 90 days: only alex and sam count, so robin's play of Charlie
		// does not, and Alpha is watched by both of them.
		{"everyone, inactive left out", 100, 90, "Alpha (2019),Echo (2022)"},
	}
	for _, c := range cases {
		watchSet(t, e, c.percent, c.inactive)
		if got := strings.Join(watchedTitles(t, e), ","); got != c.want {
			t.Errorf("%s: watched %q, want %q", c.name, got, c.want)
		}
	}
}

func TestItemPageListsEachPersonsWatchState(t *testing.T) {
	e := newLibraryTestEnv(t)
	watchUsers(t, e)
	watchSet(t, e, 51, 90)
	res, body := e.get(t, "/library/"+e.itemID(t, "Charlie"), false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Watch history",
		"A file counts as watched when a majority of the counted people have watched it (2 counted, so 2 needed).",
		"Watched by", "0 of 2 counted people", "inactive for 200 days", "inactive for 95 days", "disabled in Jellyfin",
		"1 play, last 2 days ago", "Not counted", "Counted")

	for _, name := range []string{"alex", "dev", "kim", "robin", "sam"} {
		if !strings.Contains(body, `<td role="cell" class="item-watch__name">`+name+`</td>`) {
			t.Errorf("%s is not listed", name)
		}
	}

	_, body = e.get(t, "/library/"+e.itemID(t, "Alpha"), false)
	expectContains(t, body, "2 of 2 counted people", "2 plays, last 2 days ago")

	// Nobody counted: the page says so instead of a rule.
	libraryTestMust(t, e.st.SetSettings(context.Background(), map[string]string{store.KeyWatchUsers: store.WatchUsersSelected}))
	libraryTestMust(t, e.st.SetSelectedUsers(context.Background(), nil))
	_, body = e.get(t, "/library/"+e.itemID(t, "Alpha"), false)
	queueExpect(t, body, "Nobody's watch history counts, so watched and favourite conditions never match.")
}

func TestSettingsWatchedStateTable(t *testing.T) {
	e := newLibraryTestEnv(t)
	watchUsers(t, e)
	watchSet(t, e, 51, 90)
	res, body := watchGetSettings(t, e)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body,
		"Counting 2 of 5 users (1 disabled, 2 inactive). A file counts as watched when a majority of them have watched it.",
		"inactive for 200 days", "inactive for 95 days", "disabled in Jellyfin", "2 days ago", "Last used Jellyfin")
	// kim is listed in the checklist, greyed and not tickable.
	if !strings.Contains(body, `value="kim" disabled`) {
		t.Error("the disabled user can be ticked")
	}
	if !strings.Contains(body, `id="watch_rule-majority" name="watch_rule" value="majority" checked`) {
		t.Error("A majority is not chosen")
	}

	// Every counted user inactive: they count anyway, with a note.
	watchSet(t, e, 51, 1)
	_, body = watchGetSettings(t, e)
	expectContains(t, body, "None of them has been active in the last 1 days, so inactive users are counted.",
		"counted because none of the counted people has been active in the last 1 days")
}

func TestDashboardFlagsNobodyCounted(t *testing.T) {
	e := newLibraryTestEnv(t)
	ctx := context.Background()
	_, body := e.get(t, "/", false)
	if strings.Contains(body, "No users are counted") {
		t.Fatal("a problem shows while someone counts")
	}
	libraryTestMust(t, e.st.SetSettings(ctx, map[string]string{store.KeyWatchUsers: store.WatchUsersSelected}))
	libraryTestMust(t, e.st.SetSelectedUsers(ctx, nil))
	_, body = e.get(t, "/", false)
	expectContains(t, body, "No users are counted (1 not selected), so watched and favourite conditions never match.",
		"Choose whose viewing counts")
	if !strings.Contains(body, `href="/settings#watched"`) {
		t.Error("the problem does not link to the Watched state section")
	}
}

func TestSettingsSaveWatchedState(t *testing.T) {
	e := newSettingsEnv(t)
	ctx := context.Background()
	users, _ := e.st.Users(ctx)
	dev := users[0].ID
	// Saving must never write the old key.
	libraryTestMust(t, e.st.SetSetting(ctx, store.KeyWatchMode, "sentinel"))
	save := func(form url.Values) {
		t.Helper()
		res, body := e.post("/settings/users", form, false)
		e.expectRedirect(res, body, "/settings?saved=users#watched")
		e.waitIdle()
	}
	form := func(who, rule, percent, days string, ticks ...string) url.Values {
		return url.Values{"watch_users": {who}, "watch_rule": {rule}, "watch_percent": {percent}, "watch_inactive_days": {days}, "user": ticks}
	}
	cases := []struct {
		name      string
		form      url.Values
		who       string
		percent   int
		inactive  int
		ruleShown string
	}{
		{"everyone, any one", form("everyone", "any", "", "90"), store.WatchUsersEveryone, 0, 90, "any"},
		{"selected, majority", form("selected", "majority", "", "30", dev), store.WatchUsersSelected, 51, 30, "majority"},
		{"everyone", form("everyone", "everyone", "", "0"), store.WatchUsersEveryone, 100, 0, "everyone"},
		{"custom 60", form("everyone", "custom", " 60 ", "3650"), store.WatchUsersEveryone, 60, 3650, "custom"},
	}
	for _, c := range cases {
		save(c.form)
		st := e.settings()
		if st.WatchUsers != c.who || st.WatchPercent != c.percent || st.WatchInactiveDays != c.inactive {
			t.Errorf("%s: saved %q %d %d", c.name, st.WatchUsers, st.WatchPercent, st.WatchInactiveDays)
		}
		_, body := e.get("/settings", false)
		if !strings.Contains(body, `value="`+c.ruleShown+`" checked`) {
			t.Errorf("%s: %s is not shown as chosen", c.name, c.ruleShown)
		}
	}
	_, body := e.get("/settings", false)
	if !strings.Contains(body, `name="watch_percent" type="text" inputmode="numeric" autocomplete="off" value="60"`) {
		t.Error("the custom share is not shown")
	}
	if v, err := e.st.Setting(ctx, store.KeyWatchMode); err != nil || v != "sentinel" {
		t.Errorf("the old watch_mode key was written: %q %v", v, err)
	}
	// Everyone does not need ticks, and does not clear the saved ones.
	save(form("everyone", "any", "", "90"))
	if sel, _ := e.st.SelectedUsers(ctx); len(sel) != 1 {
		t.Errorf("choosing Everyone changed the ticks: %+v", sel)
	}
	// Selected with nobody ticked is refused.
	res, body := e.post("/settings/users", form("selected", "any", "", "90"), false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, "Choose at least one person whose viewing counts, or choose Everyone.")
	e.checkNoKey()
}

func TestSettingsWatchedStateFromOldMode(t *testing.T) {
	cases := []struct{ mode, want string }{{"all", "everyone"}, {"any", "any"}}
	for _, c := range cases {
		e := newSettingsEnv(t)
		// An install upgraded before it saved a share has only watch_mode.
		if err := e.st.SetSetting(context.Background(), store.KeyWatchMode, c.mode); err != nil {
			t.Fatal(err)
		}
		_, body := e.get("/settings", false)
		if !strings.Contains(body, `id="watch_rule-`+c.want+`" name="watch_rule" value="`+c.want+`" checked`) {
			t.Errorf("watch_mode %s: %s is not chosen", c.mode, c.want)
		}
		if !strings.Contains(body, `name="watch_users" value="everyone" checked`) {
			t.Errorf("watch_mode %s: Everyone is not chosen", c.mode)
		}
		e.checkNoKey()
	}
}

func TestWatchFollowUpSyncsOnlyWhenPeopleChange(t *testing.T) {
	cases := []struct {
		name          string
		before, after watchSaved
		want          string
	}{
		{"mode", watchSaved{who: "everyone"}, watchSaved{who: "selected", selected: []string{"a"}}, "sync"},
		{"ticks", watchSaved{who: "selected", selected: []string{"a"}}, watchSaved{who: "selected", selected: []string{"a", "b"}}, "sync"},
		{"ticks under everyone", watchSaved{who: "everyone", selected: []string{"a"}}, watchSaved{who: "everyone", selected: []string{"b"}}, "none"},
		{"share", watchSaved{who: "everyone"}, watchSaved{who: "everyone", percent: 51}, "evaluate"},
		{"inactive", watchSaved{who: "everyone", inactive: 90}, watchSaved{who: "everyone", inactive: 30}, "evaluate"},
		{"nothing", watchSaved{who: "everyone", inactive: 90}, watchSaved{who: "everyone", inactive: 90}, "none"},
	}
	for _, c := range cases {
		if got := watchFollowUpKind(c.before, c.after); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestQueueRunningJobShowsPlaybackNote(t *testing.T) {
	e := newQueueTestEnv(t)
	id := e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobReplacing})
	j, err := e.st.Job(context.Background(), id)
	libraryTestMust(t, err)
	r := e.srv.queueRunningView(j, queue.Live{Status: store.JobReplacing, Note: "Waiting: alex is watching this on Living Room TV"})
	w := queueWaitingRow(store.Job{ID: 9, ItemName: "Bravo (2020)",
		Summary: "Not started: alex is watching this on Living Room TV. JellyTrim will try again in 30 min; other jobs go first."}, 1)
	body := renderString(t, views.QueueLiveFragment(views.QueueLive{Running: []views.QueueRunning{r}, Waiting: []views.QueueWaiting{w}, WaitingTotal: 1}))
	queueExpect(t, body, "Waiting: alex is watching this on Living Room TV",
		"The new file is ready. It replaces the original once nobody is watching. JellyTrim waits up to 6 hours, then tries again later.",
		"Not started: alex is watching this on Living Room TV. JellyTrim will try again in 30 min; other jobs go first.")
	queueExpectRaw(t, body, `class="queue-job__note"`, `data-dialog-open="queue-cancel-`+strconv.FormatInt(id, 10)+`"`)
}

func TestHistoryRestoreRefusedWhilePlaying(t *testing.T) {
	e := newQueueTestEnv(t)
	out := int64(13_700_000_000)
	alpha := e.itemID(t, "Alpha")
	id := e.seed(t, queueTestJob{item: alpha, name: "Alpha (2019)", status: store.JobComplete, backup: true, outcome: store.JobOutcome{OutputSize: &out}})
	path := "/history/" + strconv.FormatInt(id, 10) + "/restore"

	e.jf.SetPlaying(alpha, "alex", "Living Room TV", false)
	res, body := e.post(t, path, false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("playing: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Not restored while someone is watching", "alex", "Living Room TV",
		"Try again when they have finished. Nothing was changed and the backup is kept.", "callout--warn")
	queueExpectNot(t, body, "JellyTrim could not put the original back")

	e.jf.StopPlaying(alpha)
	for range 5 {
		e.jf.FailNext("/Sessions", http.StatusUnauthorized, 0)
	}
	res, body = e.post(t, path, false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unknown: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Not restored: Jellyfin did not answer",
		"JellyTrim could not check with Jellyfin whether this is playing. Try again in a moment.", "callout--warn")
}

func TestRestoreProblemForPlayback(t *testing.T) {
	p := restoreProblem(&queue.InUseError{Playing: jellyfin.Playing{UserName: "sam", DeviceName: "Kitchen"}})
	if p.Variant != "warn" || p.Detail != "" || !strings.Contains(p.Text, "sam") {
		t.Errorf("in use: %+v", p)
	}
	p = restoreProblem(queue.ErrPlaybackUnknown)
	if p.Variant != "warn" || p.Detail != "" {
		t.Errorf("unknown: %+v", p)
	}
}

func TestHistoryShowsUnreachableWarning(t *testing.T) {
	e := newQueueTestEnv(t)
	out := int64(13_700_000_000)
	id := e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobComplete, outcome: store.JobOutcome{
		OutputSize: &out, Summary: "Saved 34.5 GB.",
		Diagnostics: `{"step":"replacing","warnings":[` + strconv.Quote(queue.UnreachableWarning) + `]}`,
	}})
	_, body := e.get(t, "/history/"+strconv.FormatInt(id, 10), false)
	queueExpect(t, body, "Warnings", queue.UnreachableWarning)
}

// watchGetSettings fetches Settings, which shows the Jellyfin address in
// its connection form, and checks that the API key is not in it.
func watchGetSettings(t *testing.T, e libraryTestEnv) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
	body := rec.Body.String()
	if strings.Contains(body, e.jf.APIKey()) {
		t.Fatal("/settings: the API key appears in the response")
	}
	return rec.Result(), body
}

func renderString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	libraryTestMust(t, c.Render(context.Background(), &b))
	return b.String()
}
