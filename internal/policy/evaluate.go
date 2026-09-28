package policy

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// Line is one explained check: a scope test or a condition.
type Line struct {
	Pass bool   `json:"pass"`
	Text string `json:"text"`
}

// Match is the result of checking one policy against one item.
type Match struct {
	PolicyID   int64  `json:"policy_id"`
	PolicyName string `json:"policy_name"`
	Enabled    bool   `json:"enabled"`
	Matched    bool   `json:"matched"`
	Lines      []Line `json:"lines"`
}

// Result is the outcome of evaluating every policy against an item.
type Result struct {
	// Winner is the first enabled policy that matched, or nil.
	Winner *Policy
	// Matches holds a Match for every policy, in priority order.
	Matches []Match
}

// WinnerMatch returns the Match for the winning policy.
func (r Result) WinnerMatch() (Match, bool) {
	if r.Winner == nil {
		return Match{}, false
	}
	for _, m := range r.Matches {
		if m.PolicyID == r.Winner.ID {
			return m, true
		}
	}
	return Match{}, false
}

// Sort orders policies by priority, then ID, which is the evaluation order.
func Sort(ps []Policy) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Priority != ps[j].Priority {
			return ps[i].Priority < ps[j].Priority
		}
		return ps[i].ID < ps[j].ID
	})
}

// Evaluate checks every policy against the item. The first enabled policy
// whose scope and conditions all pass wins; the others are still reported so
// conflicts are visible. Policies must already be sorted (see Sort).
func Evaluate(ps []Policy, it Item, now time.Time) Result {
	var res Result
	for i := range ps {
		m := Check(ps[i], it, now)
		res.Matches = append(res.Matches, m)
		if res.Winner == nil && m.Matched && ps[i].Enabled {
			res.Winner = &ps[i]
		}
	}
	return res
}

// Check evaluates one policy against the item, ignoring whether it is enabled.
func Check(p Policy, it Item, now time.Time) Match {
	m := Match{PolicyID: p.ID, PolicyName: p.Name, Enabled: p.Enabled, Matched: true}
	add := func(l Line) {
		m.Lines = append(m.Lines, l)
		if !l.Pass {
			m.Matched = false
		}
	}
	for _, l := range checkScope(p.Scope, it) {
		add(l)
	}
	for _, c := range p.Conditions.All {
		add(checkCondition(c, it, now))
	}
	return m
}

func checkScope(s Scope, it Item) []Line {
	var out []Line
	if len(s.Libraries) > 0 {
		lib := it.LibraryName
		if lib == "" {
			lib = "this library"
		}
		if contains(s.Libraries, it.LibraryID) {
			out = append(out, Line{true, "in " + lib})
		} else {
			out = append(out, Line{false, "in " + lib + ", which this policy does not cover"})
		}
	}
	if len(s.Types) > 0 {
		label := strings.ToLower(it.Type)
		if contains(s.Types, it.Type) {
			out = append(out, Line{true, "is " + article(label)})
		} else {
			out = append(out, Line{false, "is " + article(label) + ", which this policy does not cover"})
		}
	}
	if len(s.Series) > 0 {
		switch {
		case it.SeriesID == "":
			out = append(out, Line{false, "not part of a series"})
		case contains(s.Series, it.SeriesID):
			out = append(out, Line{true, "series is " + it.SeriesName})
		default:
			out = append(out, Line{false, "series " + it.SeriesName + " is not covered"})
		}
	}
	if len(s.Seasons) > 0 {
		out = append(out, checkSeason(s.Seasons, it.SeasonNumber))
	}
	if len(s.Collections) > 0 {
		hit := false
		for _, c := range it.Collections {
			if contains(s.Collections, c) {
				hit = true
				break
			}
		}
		if hit {
			out = append(out, Line{true, "in a chosen collection"})
		} else {
			out = append(out, Line{false, "not in a chosen collection"})
		}
	}
	return out
}

func checkSeason(seasons []int, n *int) Line {
	if n == nil {
		return Line{false, "season unknown"}
	}
	for _, s := range seasons {
		if s == *n {
			return Line{true, fmt.Sprintf("season %d", *n)}
		}
	}
	return Line{false, fmt.Sprintf("season %d is not covered", *n)}
}

func article(s string) string {
	if s == "" {
		return "an item"
	}
	if strings.ContainsRune("aeiou", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

func checkCondition(c Condition, it Item, now time.Time) Line {
	switch c.Field {
	case FieldWatched:
		return checkWatched(c, it)
	case FieldFavourite:
		return checkFavourite(c, it)
	case FieldLastWatched:
		last, ok := lastPlayed(it)
		if !ok {
			return Line{false, "never watched"}
		}
		return checkDays(c, "last watched", last, now)
	case FieldAdded:
		if it.DateAdded == nil {
			return Line{false, "date added unknown"}
		}
		return checkDays(c, "added", *it.DateAdded, now)
	case FieldResolution:
		return checkResolution(c, it)
	case FieldCodec:
		return checkCodec(c, it)
	case FieldBitrate:
		return checkBitrate(c, it)
	case FieldSize:
		return checkSize(c, it)
	case FieldHDR:
		return checkHDR(c, it)
	case FieldTag:
		return checkList(c, it.Tags, "tag")
	case FieldGenre:
		return checkList(c, it.Genres, "genre")
	}
	return Line{false, "unknown condition " + c.Field}
}

// watchedState combines the selected users' played flags.
func watchedState(it Item) (bool, string) {
	if len(it.Users) == 0 {
		return false, "no Jellyfin users are selected for watch state"
	}
	played := 0
	for _, u := range it.Users {
		if u.Played {
			played++
		}
	}
	if it.WatchMode == WatchAll {
		if played == len(it.Users) {
			return true, "watched by every selected user"
		}
		return false, fmt.Sprintf("watched by %d of %d selected users", played, len(it.Users))
	}
	if played > 0 {
		return true, "watched"
	}
	return false, "not watched"
}

func checkWatched(c Condition, it Item) Line {
	want := c.Bool != nil && *c.Bool
	got, text := watchedState(it)
	if len(it.Users) == 0 {
		return Line{false, text}
	}
	return Line{got == want, text}
}

// favouriteState is true when any selected user marked the item as a
// favourite, whatever the watch mode. A favourite usually protects an item,
// so requiring every user to agree would quietly weaken that protection.
func favouriteState(it Item) (bool, string) {
	for _, u := range it.Users {
		if u.Favourite {
			return true, "a favourite"
		}
	}
	return false, "not a favourite"
}

func checkFavourite(c Condition, it Item) Line {
	want := c.Bool != nil && *c.Bool
	got, text := favouriteState(it)
	if len(it.Users) == 0 {
		return Line{false, "no Jellyfin users are selected for favourites"}
	}
	return Line{got == want, text}
}

// lastPlayed is the most recent play by any selected user, whatever the
// watch mode: a recent viewing by anyone resets the clock.
func lastPlayed(it Item) (time.Time, bool) {
	var last time.Time
	for _, u := range it.Users {
		if u.LastPlayed != nil && u.LastPlayed.After(last) {
			last = *u.LastPlayed
		}
	}
	return last, !last.IsZero()
}

func checkDays(c Condition, what string, t, now time.Time) Line {
	n := int(c.Number)
	days := units.DaysSince(t, now)
	limit := now.Add(-time.Duration(n) * 24 * time.Hour)
	var pass bool
	var cmp string
	if c.Op == OpMoreThanDays {
		pass = t.Before(limit)
		cmp = "more than"
	} else {
		pass = t.After(limit)
		cmp = "less than"
	}
	not := ""
	if !pass {
		not = "not "
	}
	return Line{pass, fmt.Sprintf("%s %s (%s%s %d)", what, dayText(days), not, cmp, n)}
}

func dayText(days int) string {
	switch days {
	case 0:
		return "today"
	case 1:
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

func mainVideo(it Item) (media.VideoStream, bool) {
	if it.File == nil {
		return media.VideoStream{}, false
	}
	return it.File.MainVideo()
}

func checkResolution(c Condition, it Item) Line {
	want, _ := ParseResolution(c.Text)
	v, ok := mainVideo(it)
	if !ok || v.Resolution() == 0 {
		return Line{false, "resolution unknown (not inspected yet)"}
	}
	got := v.Resolution()
	switch c.Op {
	case OpAbove:
		if got > want {
			return Line{true, got.Label() + " is above " + want.Label()}
		}
		return Line{false, got.Label() + " is not above " + want.Label()}
	case OpAtMost:
		if got <= want {
			return Line{true, got.Label() + " is at most " + want.Label()}
		}
		return Line{false, got.Label() + " is above " + want.Label()}
	}
	if got == want {
		return Line{true, "resolution is " + got.Label()}
	}
	return Line{false, "resolution is " + got.Label() + ", not " + want.Label()}
}

func codecLabels(list []string) string {
	labels := make([]string, len(list))
	for i, c := range list {
		labels[i] = media.Codec(c).Label()
	}
	return strings.Join(labels, " or ")
}

func checkCodec(c Condition, it Item) Line {
	v, ok := mainVideo(it)
	if !ok || v.Codec == "" {
		return Line{false, "codec unknown (not inspected yet)"}
	}
	in := contains(c.List, string(v.Codec))
	label := v.Codec.Label()
	if c.Op == OpIsNot {
		if in {
			return Line{false, "codec is " + label}
		}
		return Line{true, "codec is " + label + ", not " + codecLabels(c.List)}
	}
	if in {
		return Line{true, "codec is " + label}
	}
	return Line{false, "codec is " + label + ", not " + codecLabels(c.List)}
}

func checkBitrate(c Condition, it Item) Line {
	if it.File == nil {
		return Line{false, "bitrate unknown (not inspected yet)"}
	}
	bps, ok := it.File.VideoBitrate()
	if !ok {
		return Line{false, "bitrate unknown, so this condition does not match"}
	}
	limit := int64(math.Round(c.Number * 1_000_000))
	return compare(c.Op, bps, limit, "bitrate "+units.Bitrate(bps), units.Bitrate(limit))
}

func checkSize(c Condition, it Item) Line {
	size := it.Size
	if it.File != nil && it.File.Size > 0 {
		size = it.File.Size
	}
	if size <= 0 {
		return Line{false, "size unknown"}
	}
	limit := int64(math.Round(c.Number * 1_000_000_000))
	return compare(c.Op, size, limit, "size "+units.Bytes(size), units.Bytes(limit))
}

func compare(op string, got, limit int64, gotText, limitText string) Line {
	if op == OpAbove {
		if got > limit {
			return Line{true, gotText + " is above " + limitText}
		}
		return Line{false, gotText + " is not above " + limitText}
	}
	if got < limit {
		return Line{true, gotText + " is below " + limitText}
	}
	return Line{false, gotText + " is not below " + limitText}
}

func checkHDR(c Condition, it Item) Line {
	if it.File == nil {
		return Line{false, "dynamic range unknown (not inspected yet)"}
	}
	class := it.File.HDR().Class
	in := contains(c.List, string(class))
	var names []string
	for _, v := range c.List {
		names = append(names, media.HDRClass(v).Label())
	}
	want := strings.Join(names, " or ")
	if c.Op == OpIsNot {
		if in {
			return Line{false, "dynamic range is " + class.Label()}
		}
		return Line{true, "dynamic range is " + class.Label() + ", not " + want}
	}
	if in {
		return Line{true, "dynamic range is " + class.Label()}
	}
	return Line{false, "dynamic range is " + class.Label() + ", not " + want}
}

func checkList(c Condition, have []string, what string) Line {
	hit := ""
	for _, want := range c.List {
		for _, h := range have {
			if strings.EqualFold(h, want) {
				hit = h
				break
			}
		}
		if hit != "" {
			break
		}
	}
	positive := c.Op == OpHas || c.Op == OpIs
	values := strings.Join(c.List, ", ")
	switch {
	case positive && hit != "":
		return Line{true, "has " + what + " " + hit}
	case positive:
		return Line{false, "does not have " + what + " " + values}
	case hit != "":
		return Line{false, "has " + what + " " + hit}
	}
	return Line{true, "does not have " + what + " " + values}
}
