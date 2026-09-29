package library

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// Why a Jellyfin user's watch state does not count. Inactive users get
// "inactive for N days".
const (
	ReasonDisabled    = "disabled in Jellyfin"
	ReasonNotSelected = "not selected"
	ReasonNeverUsed   = "never used Jellyfin"
)

// WatchUser is a Jellyfin user and whether their watch state counts now.
type WatchUser struct {
	User    store.JellyfinUser
	Counted bool
	// Reason says why the user is not counted: ReasonDisabled,
	// ReasonNotSelected, "inactive for 124 days" or ReasonNeverUsed. It is
	// empty when the user is counted.
	Reason string
	// Note is set on a counted user who is inactive but still counted,
	// because leaving out every inactive user would have left nobody.
	Note string
	// Inactive is set on a user left out of the watched share only because
	// they have not used Jellyfin recently. Their favourites and plays
	// still count, so an absent person's favourites keep protecting files.
	Inactive bool
}

// watchers is whose watch state counts under the current settings.
type watchers struct {
	// users is every known user, in the store's order (by name).
	users []WatchUser
	// counted are the users who count towards the watched share now.
	counted []store.JellyfinUser
	// history are the users whose favourites and plays count: the counted
	// users plus the inactive ones.
	history []store.JellyfinUser
	// fallback is true when every candidate was inactive, so the inactive
	// filter was not applied.
	fallback     bool
	rule         policy.WatchRule
	inactiveDays int
}

// isCandidate reports whether a user's watch state is read from Jellyfin:
// every enabled user in "everyone" mode, the ticked enabled users in
// "selected" mode. Whether they are active is decided at evaluation time,
// so changing that setting needs no sync.
func isCandidate(u store.JellyfinUser, st store.Settings) bool {
	return !u.Disabled && (st.WatchUsers != store.WatchUsersSelected || u.Selected)
}

// candidates returns the users whose watch state sync stores.
func candidates(users []store.JellyfinUser, st store.Settings) []store.JellyfinUser {
	var out []store.JellyfinUser
	for _, u := range users {
		if isCandidate(u, st) {
			out = append(out, u)
		}
	}
	return out
}

// inactiveReason says why u counts as inactive at now, or "" when u is
// active or the filter is off. A user who has never been active counts as
// inactive only while the filter is on.
func inactiveReason(u store.JellyfinUser, days int, now time.Time) string {
	if days <= 0 {
		return ""
	}
	if u.LastActivityAt == nil {
		return ReasonNeverUsed
	}
	if u.LastActivityAt.Before(now.Add(-time.Duration(days) * 24 * time.Hour)) {
		return fmt.Sprintf("inactive for %d days", units.DaysSince(*u.LastActivityAt, now))
	}
	return ""
}

// countWatchers works out whose watch state counts. Disabled users never
// count; in "selected" mode only ticked users do; then users inactive for
// more than the configured days are left out of the watched share, unless
// that would leave nobody, in which case the filter is not applied. Inactive
// users' favourites and plays always count.
func countWatchers(users []store.JellyfinUser, st store.Settings, now time.Time) watchers {
	w := watchers{users: make([]WatchUser, len(users)), rule: policy.WatchRule{Percent: st.WatchPercent},
		inactiveDays: st.WatchInactiveDays}
	active := 0
	for i, u := range users {
		wu := WatchUser{User: u}
		switch {
		case u.Disabled:
			wu.Reason = ReasonDisabled
		case !isCandidate(u, st):
			wu.Reason = ReasonNotSelected
		default:
			wu.Reason = inactiveReason(u, st.WatchInactiveDays, now)
			wu.Counted = true // for now: see below
			if wu.Reason == "" {
				active++
			}
		}
		w.users[i] = wu
	}
	w.fallback = active == 0
	for i := range w.users {
		wu := &w.users[i]
		if !wu.Counted {
			continue
		}
		w.history = append(w.history, wu.User)
		if wu.Reason != "" {
			if !w.fallback {
				wu.Counted, wu.Inactive = false, true
				continue
			}
			wu.Note = fmt.Sprintf("%s; counted because none of the counted people has been active in the last %d days",
				wu.Reason, st.WatchInactiveDays)
			wu.Reason = ""
		}
		w.counted = append(w.counted, wu.User)
	}
	if len(w.counted) == 0 {
		w.fallback = false // there was nobody to fall back to
	}
	return w
}

// loadWatchers reads the users and settings and works out who counts now.
func (s *Service) loadWatchers(ctx context.Context) (watchers, store.Settings, error) {
	st, err := s.store.Settings(ctx)
	if err != nil {
		return watchers{}, st, err
	}
	users, err := s.store.Users(ctx)
	if err != nil {
		return watchers{}, st, err
	}
	return countWatchers(users, st, s.now()), st, nil
}

// WatchUsers lists every known Jellyfin user, by name, with whether their
// watch state counts now and, if not, why.
func (s *Service) WatchUsers(ctx context.Context) ([]WatchUser, error) {
	w, _, err := s.loadWatchers(ctx)
	return w.users, err
}

// WatchCount is the Library list's view of the watch rule (see
// store.LibraryFilter), so the list agrees with the policies.
func (s *Service) WatchCount(ctx context.Context) (*store.WatchCount, error) {
	w, _, err := s.loadWatchers(ctx)
	if err != nil {
		return nil, err
	}
	out := &store.WatchCount{Users: make([]string, 0, len(w.counted)), Needed: w.rule.Needed(len(w.counted)),
		Favourites: make([]string, 0, len(w.history))}
	for _, u := range w.counted {
		out.Users = append(out.Users, u.ID)
	}
	for _, u := range w.history {
		out.Favourites = append(out.Favourites, u.ID)
	}
	return out, nil
}

// WatchSummary describes, in one or two plain sentences, whose watch state
// counts and when a file counts as watched: for example "Counting 3 of 4
// users (1 inactive). A file counts as watched when a majority of them
// have watched it."
func (s *Service) WatchSummary(ctx context.Context) (string, error) {
	w, _, err := s.loadWatchers(ctx)
	if err != nil {
		return "", err
	}
	return w.summary(), nil
}

func (w watchers) summary() string {
	total, n := len(w.users), len(w.counted)
	if total == 0 {
		return "No Jellyfin users are known yet. They are read from Jellyfin at the next sync."
	}
	left := w.notCounted()
	if n == 0 {
		return "No users are counted (" + left + "), so watched and favourite conditions never match."
	}
	var b strings.Builder
	switch {
	case total == 1:
		fmt.Fprintf(&b, "Counting the only user, %s.", w.counted[0].Name)
	case n == total:
		fmt.Fprintf(&b, "Counting all %d users.", total)
	default:
		fmt.Fprintf(&b, "Counting %d of %d users (%s).", n, total, left)
	}
	if w.fallback {
		fmt.Fprintf(&b, " None of them has been active in the last %d days, so inactive users are counted.", w.inactiveDays)
	}
	b.WriteString(" " + w.ruleSentence())
	return b.String()
}

// notCounted says how many users are left out and why: "1 disabled,
// 2 inactive".
func (w watchers) notCounted() string {
	var disabled, unselected, inactive int
	for _, u := range w.users {
		switch {
		case u.Counted:
		case u.Reason == ReasonDisabled:
			disabled++
		case u.Reason == ReasonNotSelected:
			unselected++
		default:
			inactive++
		}
	}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{disabled, "disabled"}, {unselected, "not selected"}, {inactive, "inactive"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	return strings.Join(parts, ", ")
}

func (w watchers) ruleSentence() string {
	n := len(w.counted)
	if n == 1 {
		return "A file counts as watched when " + w.counted[0].Name + " has watched it."
	}
	switch w.rule.Percent {
	case policy.WatchAnyOne:
		return "A file counts as watched when any one of them has watched it."
	case policy.WatchMajority:
		return "A file counts as watched when a majority of them have watched it."
	case policy.WatchEveryone:
		return "A file counts as watched when all of them have watched it."
	}
	return fmt.Sprintf("A file counts as watched when %s of them (%d of %d) have watched it.",
		w.rule.Label(), w.rule.Needed(n), n)
}

// ItemWatch is one Jellyfin user's view of one item, with whether that
// user counts.
type ItemWatch struct {
	WatchUser
	Played     bool
	PlayCount  int
	Favourite  bool
	LastPlayed *time.Time
}

// ItemWatchState lists every known Jellyfin user, by name, with their
// stored watch state for the item and whether they count. Users whose watch
// state is not read from Jellyfin (disabled, or not selected) show as not
// played.
func (s *Service) ItemWatchState(ctx context.Context, itemID string) ([]ItemWatch, error) {
	w, _, err := s.loadWatchers(ctx)
	if err != nil {
		return nil, err
	}
	ud, err := s.store.ItemUserData(ctx, itemID)
	if err != nil {
		return nil, err
	}
	byUser := make(map[string]store.UserData, len(ud))
	for _, d := range ud {
		byUser[d.UserID] = d
	}
	out := make([]ItemWatch, len(w.users))
	for i, u := range w.users {
		d := byUser[u.User.ID]
		out[i] = ItemWatch{WatchUser: u, Played: d.Played, PlayCount: d.PlayCount, Favourite: d.Favourite,
			LastPlayed: d.LastPlayedAt}
	}
	return out, nil
}
