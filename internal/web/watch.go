package web

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// watchMaxInactiveDays is the largest "ignore accounts not used for more
// than" value Settings accepts: ten years.
const watchMaxInactiveDays = 3650

// Values of the "A file counts as watched when" control.
const (
	watchRuleAny      = "any"
	watchRuleMajority = "majority"
	watchRuleEveryone = "everyone"
	watchRuleCustom   = "custom"
)

// watchChoice is the watched state controls as sent, checked. Form holds
// what to show again, with any errors.
type watchChoice struct {
	Who      string   // store.WatchUsersEveryone or store.WatchUsersSelected
	IDs      []string // the ticked enabled users
	Percent  int
	Inactive int
	Form     views.WatchForm
}

func (c watchChoice) invalid() bool {
	return c.Form.UserError != "" || c.Form.RuleError != "" || c.Form.InactiveError != ""
}

// watchUserViews lists every known user for the checklist. Disabled users
// are listed but cannot be ticked.
func watchUserViews(users []store.JellyfinUser, checked func(store.JellyfinUser) bool) []views.SetupUser {
	out := make([]views.SetupUser, 0, len(users))
	for _, u := range users {
		out = append(out, views.SetupUser{ID: u.ID, Name: u.Name, Disabled: u.Disabled, Checked: !u.Disabled && checked(u)})
	}
	return out
}

// watchRuleOf names the control's choice for a saved percentage.
func watchRuleOf(percent int) (rule, custom string) {
	switch percent {
	case policy.WatchAnyOne:
		return watchRuleAny, ""
	case policy.WatchMajority:
		return watchRuleMajority, ""
	case policy.WatchEveryone:
		return watchRuleEveryone, ""
	}
	return watchRuleCustom, strconv.Itoa(percent)
}

// watchFormSaved is the controls as saved. With tickAll, every enabled user
// is ticked while nobody is (the setup wizard's starting point).
func (s *Server) watchFormSaved(ctx context.Context, tickAll bool) (views.WatchForm, error) {
	st, err := s.Store.Settings(ctx)
	if err != nil {
		return views.WatchForm{}, err
	}
	users, err := s.Store.Users(ctx)
	if err != nil {
		return views.WatchForm{}, err
	}
	none := !slices.ContainsFunc(users, func(u store.JellyfinUser) bool { return u.Selected && !u.Disabled })
	f := views.WatchForm{
		Who:          st.WatchUsers,
		InactiveDays: strconv.Itoa(st.WatchInactiveDays),
		Users:        watchUserViews(users, func(u store.JellyfinUser) bool { return u.Selected || (tickAll && none) }),
	}
	f.Rule, f.Percent = watchRuleOf(st.WatchPercent)
	return f, nil
}

// watchParse reads and checks the watched state controls. Ticks only
// matter, and are only required, when only selected people count.
func (s *Server) watchParse(ctx context.Context, form url.Values) (watchChoice, error) {
	users, err := s.Store.Users(ctx)
	if err != nil {
		return watchChoice{}, err
	}
	c := watchChoice{Who: form.Get("watch_users")}
	c.Form = views.WatchForm{
		Who:          c.Who,
		Rule:         form.Get("watch_rule"),
		Percent:      strings.TrimSpace(form.Get("watch_percent")),
		InactiveDays: strings.TrimSpace(form.Get("watch_inactive_days")),
		Users:        watchUserViews(users, func(u store.JellyfinUser) bool { return slices.Contains(form["user"], u.ID) }),
	}
	for _, u := range c.Form.Users {
		if u.Checked {
			c.IDs = append(c.IDs, u.ID)
		}
	}
	switch c.Who {
	case store.WatchUsersEveryone:
	case store.WatchUsersSelected:
		if len(c.IDs) == 0 {
			c.Form.UserError = "Choose at least one person whose viewing counts, or choose Everyone."
		}
	default:
		c.Form.Who = store.WatchUsersEveryone
		c.Form.UserError = "Choose whose watch history counts."
	}
	switch c.Form.Rule {
	case watchRuleAny:
		c.Percent = policy.WatchAnyOne
	case watchRuleMajority:
		c.Percent = policy.WatchMajority
	case watchRuleEveryone:
		c.Percent = policy.WatchEveryone
	case watchRuleCustom:
		n, err := strconv.Atoi(c.Form.Percent)
		if err != nil || n < 1 || n > 100 {
			c.Form.RuleError = "Enter a whole number from 1 to 100."
		}
		c.Percent = n
	default:
		c.Form.Rule = watchRuleAny
		c.Form.RuleError = "Choose when a file counts as watched."
	}
	n, err := strconv.Atoi(c.Form.InactiveDays)
	if err != nil || n < 0 || n > watchMaxInactiveDays {
		c.Form.InactiveError = "Enter a whole number of days from 0 to 3650. Enter 0 to count every account."
	}
	c.Inactive = n
	return c, nil
}

// watchSave stores a valid choice. The ticks are saved only when only
// selected people count, so choosing Everyone keeps the last ticks for
// later.
func (s *Server) watchSave(ctx context.Context, c watchChoice) error {
	if c.Who == store.WatchUsersSelected {
		if err := s.Store.SetSelectedUsers(ctx, c.IDs); err != nil {
			return err
		}
	}
	return s.Store.SetSettings(ctx, map[string]string{
		store.KeyWatchUsers:        c.Who,
		store.KeyWatchPercent:      strconv.Itoa(c.Percent),
		store.KeyWatchInactiveDays: strconv.Itoa(c.Inactive),
	})
}

// watchSaved is what decides whose watch state is read from Jellyfin and
// how it is counted, for telling a sync from a re-evaluation.
type watchSaved struct {
	who      string
	selected []string // enabled users ticked, sorted
	percent  int
	inactive int
}

func (s *Server) watchSavedNow(ctx context.Context) (watchSaved, error) {
	st, err := s.Store.Settings(ctx)
	if err != nil {
		return watchSaved{}, err
	}
	users, err := s.Store.Users(ctx)
	if err != nil {
		return watchSaved{}, err
	}
	w := watchSaved{who: st.WatchUsers, percent: st.WatchPercent, inactive: st.WatchInactiveDays}
	for _, u := range users {
		if u.Selected && !u.Disabled {
			w.selected = append(w.selected, u.ID)
		}
	}
	slices.Sort(w.selected)
	return w, nil
}

// watchFollowUp brings the library up to date after the watched state was
// saved. Watch state is read from Jellyfin only for the people who may
// count, so a change of whose history counts needs a sync; the share and
// the inactive filter are applied at evaluation, so they need only that.
func (s *Server) watchFollowUp(ctx context.Context, before watchSaved) {
	if s.Library == nil {
		return
	}
	after, err := s.watchSavedNow(ctx)
	if err != nil {
		s.Log.Warn("settings: could not read the watched state back", "err", err)
		s.Library.RunAsync()
		return
	}
	switch watchFollowUpKind(before, after) {
	case "sync":
		s.Library.RunAsync()
	case "evaluate":
		s.Library.EvaluateAsync()
	}
}

// watchFollowUpKind is "sync", "evaluate" or "none".
func watchFollowUpKind(before, after watchSaved) string {
	switch {
	case before.who != after.who,
		after.who == store.WatchUsersSelected && !slices.Equal(before.selected, after.selected):
		return "sync"
	case before.percent != after.percent || before.inactive != after.inactive:
		return "evaluate"
	}
	return "none"
}

// watchLastActivity is when a user last used Jellyfin, in plain words.
func (s *Server) watchLastActivity(u store.JellyfinUser) string {
	if u.LastActivityAt == nil {
		return "never"
	}
	return units.Ago(*u.LastActivityAt, s.Now())
}

// watchWhy is the reason a user does not count, or the note on one who
// counts although inactive.
func watchWhy(u library.WatchUser) string {
	switch {
	case u.Counted:
		return u.Note
	case u.Inactive:
		return u.Reason + "; favourites and plays still count"
	}
	return u.Reason
}

// watchPeople is the Settings summary and table of who counts now.
func (s *Server) watchPeople(ctx context.Context) (string, []views.WatchPerson, error) {
	if s.Library == nil {
		return "", nil, nil
	}
	summary, err := s.Library.WatchSummary(ctx)
	if err != nil {
		return "", nil, err
	}
	users, err := s.Library.WatchUsers(ctx)
	if err != nil {
		return "", nil, err
	}
	out := make([]views.WatchPerson, 0, len(users))
	for _, u := range users {
		out = append(out, views.WatchPerson{
			Name: u.User.Name, Hidden: u.User.Hidden, Counted: u.Counted, Why: watchWhy(u),
			LastActivity: s.watchLastActivity(u.User),
		})
	}
	return summary, out, nil
}

// watchRuleSentence says when a file counts as watched, for the item page:
// "A file counts as watched when a majority of counted people have watched
// it (3 counted, so 2 needed)."
func watchRuleSentence(percent int, counted []string) string {
	n := len(counted)
	rule := policy.WatchRule{Percent: percent}
	switch n {
	case 0:
		return "Nobody's watch history counts, so watched and favourite conditions never match. Choose whose history counts in Settings."
	case 1:
		return "A file counts as watched when " + counted[0] + " has watched it, the only person counted."
	}
	switch percent {
	case policy.WatchAnyOne:
		return fmt.Sprintf("A file counts as watched when any one of the counted people has watched it (%d counted).", n)
	case policy.WatchEveryone:
		return fmt.Sprintf("A file counts as watched when every counted person has watched it (%d counted).", n)
	case policy.WatchMajority:
		return fmt.Sprintf("A file counts as watched when a majority of the counted people have watched it (%d counted, so %d needed).", n, rule.Needed(n))
	}
	return fmt.Sprintf("A file counts as watched when %s of the counted people have watched it (%d counted, so %d needed).",
		rule.Label(), n, rule.Needed(n))
}
