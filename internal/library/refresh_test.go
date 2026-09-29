package library

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/store"
)

func TestRefreshItemUserData(t *testing.T) {
	watched := time.Date(2026, 9, 28, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		// change alters the fake Jellyfin after the sync.
		change  func(jf *jellyfintest.Server, alpha string)
		wantErr error
		// want is Alpha's stored watch state for the fixture user afterwards;
		// nil means it must be exactly what the sync stored.
		want *store.UserData
	}{
		{
			name: "stores what Jellyfin says now",
			change: func(jf *jellyfintest.Server, alpha string) {
				jf.SetUserData(jellyfintest.FixtureUserID, alpha, jellyfintest.UserData{
					Played: true, PlayCount: 3, IsFavorite: true, LastPlayedDate: &watched})
			},
			want: &store.UserData{Played: true, PlayCount: 3, Favourite: true, LastPlayedAt: &watched},
		},
		{
			name: "Jellyfin failing stores nothing",
			change: func(jf *jellyfintest.Server, alpha string) {
				jf.SetUserData(jellyfintest.FixtureUserID, alpha, jellyfintest.UserData{IsFavorite: true})
				for range 5 { // every attempt the client makes
					jf.FailNext("/Items/"+alpha, http.StatusServiceUnavailable, 0)
				}
			},
			wantErr: jellyfin.ErrUnavailable,
		},
		{
			name: "an item Jellyfin no longer lists keeps its watch state",
			change: func(jf *jellyfintest.Server, alpha string) {
				jf.RemoveItem(alpha)
			},
			wantErr: jellyfin.ErrNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			ctx := context.Background()
			_, err := e.svc.Run(ctx)
			must(t, err)
			alpha, bravo := mustItemID(t, e, "Alpha"), mustItemID(t, e, "Bravo")
			before, err := e.st.ItemUserData(ctx, alpha)
			must(t, err)
			bravoBefore, err := e.st.ItemUserData(ctx, bravo)
			must(t, err)
			tc.change(e.jf, alpha)

			err = e.svc.RefreshItemUserData(ctx, alpha)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil) != (err == nil) {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			after, err := e.st.ItemUserData(ctx, alpha)
			must(t, err)
			if tc.want == nil {
				assertSameUserData(t, after, before)
			} else {
				if len(after) != 1 || after[0].UserID != jellyfintest.FixtureUserID {
					t.Fatalf("stored %+v, want one row for the fixture user", after)
				}
				got := after[0]
				if got.Played != tc.want.Played || got.PlayCount != tc.want.PlayCount || got.Favourite != tc.want.Favourite ||
					got.LastPlayedAt == nil || !got.LastPlayedAt.Equal(*tc.want.LastPlayedAt) {
					t.Fatalf("stored %+v, want %+v", got, *tc.want)
				}
			}
			// Only the one item is touched.
			bravoAfter, err := e.st.ItemUserData(ctx, bravo)
			must(t, err)
			assertSameUserData(t, bravoAfter, bravoBefore)
		})
	}
}

// TestRefreshItemUserDataAsksTheUsersSyncReads checks the refresh reads the
// same users sync does: every enabled user, or only the ticked ones.
func TestRefreshItemUserDataAsksTheUsersSyncReads(t *testing.T) {
	const alex = "0123456789abcdef0123456789abcdef"
	tests := []struct {
		mode string
		want []string
	}{
		{store.WatchUsersEveryone, []string{alex, jellyfintest.FixtureUserID}},
		{store.WatchUsersSelected, []string{jellyfintest.FixtureUserID}},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			e := setup(t)
			ctx := context.Background()
			e.jf.AddUser(jellyfintest.User{ID: alex, Name: "alex"})
			must(t, e.svc.RefreshServerInfo(ctx))
			must(t, e.st.SetSetting(ctx, store.KeyWatchUsers, tc.mode))
			_, err := e.svc.Run(ctx)
			must(t, err)
			alpha := mustItemID(t, e, "Alpha")
			n := len(e.jf.RequestsFor("/Items/" + alpha))
			must(t, e.svc.RefreshItemUserData(ctx, alpha))
			var asked []string
			for _, r := range e.jf.RequestsFor("/Items/" + alpha)[n:] {
				asked = append(asked, r.Query.Get("userId"))
			}
			slices.Sort(asked)
			if !slices.Equal(asked, tc.want) {
				t.Fatalf("asked for %v, want %v", asked, tc.want)
			}
		})
	}
}

func assertSameUserData(t *testing.T, got, want []store.UserData) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("watch state %+v, want %+v", got, want)
	}
	for i := range got {
		g, w := got[i], want[i]
		sameTime := (g.LastPlayedAt == nil) == (w.LastPlayedAt == nil) &&
			(g.LastPlayedAt == nil || g.LastPlayedAt.Equal(*w.LastPlayedAt))
		if g.UserID != w.UserID || g.Played != w.Played || g.PlayCount != w.PlayCount || g.Favourite != w.Favourite || !sameTime {
			t.Fatalf("watch state %+v, want %+v", got, want)
		}
	}
}
