package store

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestWatchSettingsDefaults(t *testing.T) {
	s := openTest(t)
	st, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.WatchUsers != WatchUsersEveryone || st.WatchPercent != 0 || st.WatchInactiveDays != 90 {
		t.Fatalf("new install: users %q, percent %d, inactive days %d", st.WatchUsers, st.WatchPercent, st.WatchInactiveDays)
	}
}

func TestWatchSettingsRead(t *testing.T) {
	cases := []struct {
		name      string
		kv        map[string]string
		users     string
		percent   int
		inactive  int
		watchMode string
	}{
		{"saved values", map[string]string{KeyWatchUsers: "selected", KeyWatchPercent: "51", KeyWatchInactiveDays: "30"}, "selected", 51, 30, "any"},
		{"old all mode without a percentage", map[string]string{KeyWatchMode: "all"}, "everyone", 100, 90, "all"},
		{"old any mode without a percentage", map[string]string{KeyWatchMode: "any"}, "everyone", 0, 90, "any"},
		{"a saved percentage wins over the old mode", map[string]string{KeyWatchMode: "all", KeyWatchPercent: "60"}, "everyone", 60, 90, "all"},
		{"percentage out of range is clamped", map[string]string{KeyWatchPercent: "250"}, "everyone", 100, 90, "any"},
		{"negative percentage is clamped", map[string]string{KeyWatchPercent: "-5"}, "everyone", 0, 90, "any"},
		{"unreadable percentage is any one", map[string]string{KeyWatchPercent: "most"}, "everyone", 0, 90, "any"},
		{"unknown users value is everyone", map[string]string{KeyWatchUsers: "friends"}, "everyone", 0, 90, "any"},
		{"inactive filter off", map[string]string{KeyWatchInactiveDays: "0"}, "everyone", 0, 0, "any"},
		{"negative inactive days is off", map[string]string{KeyWatchInactiveDays: "-3"}, "everyone", 0, 0, "any"},
		{"unreadable inactive days is the default", map[string]string{KeyWatchInactiveDays: "soon"}, "everyone", 0, 90, "any"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openTest(t)
			ctx := context.Background()
			if err := s.SetSettings(ctx, c.kv); err != nil {
				t.Fatal(err)
			}
			st, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if st.WatchUsers != c.users || st.WatchPercent != c.percent || st.WatchInactiveDays != c.inactive || st.WatchMode != c.watchMode {
				t.Fatalf("got users %q percent %d inactive %d mode %q", st.WatchUsers, st.WatchPercent, st.WatchInactiveDays, st.WatchMode)
			}
		})
	}
}

func TestUsersKeepLastActivityAndHidden(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	active := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	if err := s.ReplaceUsers(ctx, []JellyfinUser{
		{ID: "u1", Name: "alex", LastActivityAt: &active},
		{ID: "u2", Name: "Sam", Hidden: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSelectedUsers(ctx, []string{"u2"}); err != nil {
		t.Fatal(err)
	}
	later := active.Add(48 * time.Hour)
	if err := s.ReplaceUsers(ctx, []JellyfinUser{
		{ID: "u1", Name: "alex", LastActivityAt: &later},
		{ID: "u2", Name: "Sam", Hidden: false},
	}); err != nil {
		t.Fatal(err)
	}
	us, err := s.Users(ctx)
	if err != nil || len(us) != 2 {
		t.Fatalf("users %+v (%v)", us, err)
	}
	if us[0].Name != "alex" || us[0].LastActivityAt == nil || !us[0].LastActivityAt.Equal(later) || us[0].Hidden {
		t.Fatalf("alex %+v", us[0])
	}
	if us[1].Name != "Sam" || us[1].LastActivityAt != nil || us[1].Hidden || !us[1].Selected {
		t.Fatalf("Sam %+v: the tick must survive a refresh", us[1])
	}
}

// migrateFrom6 builds a version 6 database holding users and settings,
// then opens it, applying migration 0007.
func migrateFrom6(t *testing.T, users string, settings string) *Store {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db := openAtVersion(t, dir, 6)
	for _, q := range []string{users, settings} {
		if q == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("upgrading: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigration0007WatchSettings(t *testing.T) {
	const (
		subset = `INSERT INTO jellyfin_users (id, name, disabled, selected, updated_at)
			VALUES ('u1', 'alex', 0, 1, 0), ('u2', 'sam', 0, 0, 0), ('u3', 'robin', 1, 0, 0)`
		allTicked = `INSERT INTO jellyfin_users (id, name, disabled, selected, updated_at)
			VALUES ('u1', 'alex', 0, 1, 0), ('u2', 'sam', 0, 1, 0), ('u3', 'robin', 1, 0, 0)`
		noneTicked = `INSERT INTO jellyfin_users (id, name, disabled, selected, updated_at)
			VALUES ('u1', 'alex', 0, 0, 0), ('u2', 'sam', 0, 0, 0)`
	)
	cases := []struct {
		name     string
		users    string
		settings string
		want     string
		percent  int
		stored   map[string]string // watch_users and watch_percent rows after migrating; "" means none
	}{
		{"a deliberate subset keeps counting only those users", subset,
			`INSERT INTO settings (key, value) VALUES ('watch_mode', 'all')`,
			WatchUsersSelected, 100, map[string]string{"watch_users": "selected", "watch_percent": "100"}},
		{"every enabled user ticked becomes everyone", allTicked,
			`INSERT INTO settings (key, value) VALUES ('watch_mode', 'any')`,
			WatchUsersEveryone, 0, map[string]string{"watch_users": "", "watch_percent": "0"}},
		{"nobody ticked becomes everyone", noneTicked, "",
			WatchUsersEveryone, 0, map[string]string{"watch_users": "", "watch_percent": ""}},
		{"a new database stays at the defaults", "", "",
			WatchUsersEveryone, 0, map[string]string{"watch_users": "", "watch_percent": ""}},
		{"a saved percentage is kept", subset,
			`INSERT INTO settings (key, value) VALUES ('watch_mode', 'all'), ('watch_percent', '60'), ('watch_users', 'everyone')`,
			WatchUsersEveryone, 60, map[string]string{"watch_users": "everyone", "watch_percent": "60"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := migrateFrom6(t, c.users, c.settings)
			ctx := context.Background()
			st, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if st.WatchUsers != c.want || st.WatchPercent != c.percent || st.WatchInactiveDays != 90 {
				t.Fatalf("users %q percent %d inactive %d", st.WatchUsers, st.WatchPercent, st.WatchInactiveDays)
			}
			got := map[string]string{}
			for k := range c.stored {
				var v string
				_ = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, k).Scan(&v)
				got[k] = v
			}
			if !reflect.DeepEqual(got, c.stored) {
				t.Fatalf("stored %v, want %v", got, c.stored)
			}
			us, err := s.Users(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range us {
				if u.LastActivityAt != nil || u.Hidden {
					t.Fatalf("new columns not empty: %+v", u)
				}
			}
		})
	}
}

func TestLibraryListWatchCount(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	if err := s.ReplaceUsers(ctx, []JellyfinUser{{ID: "u1", Name: "alex"}, {ID: "u2", Name: "sam"}, {ID: "u3", Name: "robin"}}); err != nil {
		t.Fatal(err)
	}
	// a: watched by u1 and u2, favourite of u3. b: watched by u1 only.
	for user, data := range map[string][]UserData{
		"u1": {{ItemID: "a", Played: true}, {ItemID: "b", Played: true}},
		"u2": {{ItemID: "a", Played: true}},
		"u3": {{ItemID: "a", Favourite: true}},
	} {
		if err := s.ReplaceUserData(ctx, user, data); err != nil {
			t.Fatal(err)
		}
	}
	all := []string{"u1", "u2", "u3"}
	cases := []struct {
		name       string
		watch      *WatchCount
		watched    []string
		notWatched []string
		favourite  bool // of a
	}{
		{"nil reads any enabled user", nil, []string{"a", "b"}, []string{"e1"}, true},
		{"any one of three", &WatchCount{Users: all, Needed: 1, Favourites: all}, []string{"a", "b"}, []string{"e1"}, true},
		{"two of three", &WatchCount{Users: all, Needed: 2, Favourites: all}, []string{"a"}, []string{"b", "e1"}, true},
		{"everyone", &WatchCount{Users: all, Needed: 3, Favourites: all}, nil, []string{"a", "b", "e1"}, true},
		{"only u2 and u3 counted", &WatchCount{Users: []string{"u2", "u3"}, Needed: 1, Favourites: []string{"u2", "u3"}}, []string{"a"}, []string{"b", "e1"}, true},
		{"u3 not counted: no favourite", &WatchCount{Users: []string{"u1", "u2"}, Needed: 2, Favourites: []string{"u1", "u2"}}, []string{"a"}, []string{"b", "e1"}, false},
		{"u3 inactive: favourite still counts", &WatchCount{Users: []string{"u1", "u2"}, Needed: 2, Favourites: all}, []string{"a"}, []string{"b", "e1"}, true},
		{"nobody counted", &WatchCount{}, nil, []string{"a", "b", "e1"}, false},
	}
	ids := func(rows []LibraryRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Item.ID)
		}
		return out
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			yes, n, err := s.LibraryList(ctx, LibraryFilter{Watched: "yes", Watch: c.watch})
			if err != nil || n != len(c.watched) || !reflect.DeepEqual(ids(yes), c.watched) {
				t.Fatalf("watched %v (%d, %v), want %v", ids(yes), n, err, c.watched)
			}
			no, _, err := s.LibraryList(ctx, LibraryFilter{Watched: "no", Watch: c.watch})
			if err != nil || !reflect.DeepEqual(ids(no), c.notWatched) {
				t.Fatalf("not watched %v (%v), want %v", ids(no), err, c.notWatched)
			}
			rows, _, err := s.LibraryList(ctx, LibraryFilter{Search: "Alpha", Watch: c.watch})
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows %v (%v)", rows, err)
			}
			if rows[0].Watched != slices.Contains(c.watched, "a") || rows[0].Favourite != c.favourite {
				t.Fatalf("row a: watched %v favourite %v", rows[0].Watched, rows[0].Favourite)
			}
			// The watch arguments come before the other filters' arguments.
			both, _, err := s.LibraryList(ctx, LibraryFilter{Watched: "yes", Search: "Alpha", Watch: c.watch})
			if want := slices.Contains(c.watched, "a"); err != nil || (len(both) == 1) != want {
				t.Fatalf("watched and searched: %v (%v), want a: %v", ids(both), err, want)
			}
		})
	}
}
