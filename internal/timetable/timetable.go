// Package timetable is the weekly processing schedule: 7 days of 24 hour
// blocks, each on or off. Encoding only runs in active hours. It is pure:
// callers pass the time, in the zone the schedule is meant for.
package timetable

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Days and Hours are the grid's dimensions. Day 0 is Monday.
const (
	Days  = 7
	Hours = 24
	Slots = Days * Hours
)

// DayNames are the full day names, Monday first.
var DayNames = [Days]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// Week is the schedule: Week[day][hour] is true when processing may run.
type Week [Days][Hours]bool

// ErrInvalid is returned by Parse for a malformed schedule.
var ErrInvalid = errors.New("invalid processing schedule")

// Always is a schedule with every hour active.
func Always() Week {
	var w Week
	for d := range w {
		for h := range w[d] {
			w[d][h] = true
		}
	}
	return w
}

// Parse reads the stored form: 168 characters of '0' and '1', Monday 00:00
// first. An empty string means Always.
func Parse(s string) (Week, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Always(), nil
	}
	if len(s) != Slots {
		return Week{}, ErrInvalid
	}
	var w Week
	for i, c := range s {
		switch c {
		case '1':
			w[i/Hours][i%Hours] = true
		case '0':
		default:
			return Week{}, ErrInvalid
		}
	}
	return w, nil
}

// String is the stored form (see Parse).
func (w Week) String() string {
	var b strings.Builder
	b.Grow(Slots)
	for d := range w {
		for h := range w[d] {
			if w[d][h] {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
	}
	return b.String()
}

// Full reports whether every hour is active.
func (w Week) Full() bool { return w == Always() }

// Empty reports whether no hour is active.
func (w Week) Empty() bool { return w == Week{} }

// ActiveHours counts active hours in the week.
func (w Week) ActiveHours() int {
	n := 0
	for d := range w {
		for h := range w[d] {
			if w[d][h] {
				n++
			}
		}
	}
	return n
}

// Day returns Monday=0 ... Sunday=6 for t.
func Day(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

// Active reports whether processing may run at t (in t's location).
func (w Week) Active(t time.Time) bool { return w[Day(t)][t.Hour()] }

// NextChange returns when the schedule next switches state after t: the
// start of the next active hour if t is inactive, or the end of the current
// active run. ok is false for a full or empty schedule, which never change.
func (w Week) NextChange(t time.Time) (time.Time, bool) {
	if w.Full() || w.Empty() {
		return time.Time{}, false
	}
	now := w.Active(t)
	hour := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	for i := 1; i <= Slots+1; i++ {
		// Step by wall-clock hour so daylight saving changes keep day and
		// hour meaning what the user set.
		next := hour.Add(time.Duration(i) * time.Hour)
		if w.Active(next) != now {
			return next, true
		}
	}
	return time.Time{}, false
}

// FromWindow converts the old daily "HH:MM to HH:MM" window. Partial hours
// round outwards so nothing that used to run stops running. Empty or
// invalid values give Always.
func FromWindow(start, end string) Week {
	s, okS := clock(start)
	e, okE := clock(end)
	if !okS || !okE || s == e {
		return Always()
	}
	var w Week
	startHour := s / 60
	endHour := (e + 59) / 60 // round up: 06:30 includes the 06:00 block
	for d := 0; d < Days; d++ {
		for h := 0; h < Hours; h++ {
			if s < e {
				w[d][h] = h >= startHour && h < endHour
			} else {
				w[d][h] = h >= startHour || h < endHour
			}
		}
	}
	return w
}

func clock(v string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// Preset is a named starting point offered in the UI.
type Preset struct {
	ID    string
	Label string
	Week  Week
}

// Presets are quick choices for common schedules.
func Presets() []Preset {
	nights := hoursEveryDay(1, 7)
	weekdayNights := hoursEveryDay(1, 7)
	for d := 5; d < 7; d++ { // all weekend
		for h := 0; h < Hours; h++ {
			weekdayNights[d][h] = true
		}
	}
	return []Preset{
		{ID: "always", Label: "Any time", Week: Always()},
		{ID: "nights", Label: "Nights (01:00 to 07:00)", Week: nights},
		{ID: "nights-weekends", Label: "Nights, and all weekend", Week: weekdayNights},
		{ID: "off-hours", Label: "Outside 17:00 to 23:00", Week: offHours()},
	}
}

func hoursEveryDay(from, to int) Week {
	var w Week
	for d := 0; d < Days; d++ {
		for h := from; h < to; h++ {
			w[d][h] = true
		}
	}
	return w
}

func offHours() Week {
	w := Always()
	for d := 0; d < Days; d++ {
		for h := 17; h < 23; h++ {
			w[d][h] = false
		}
	}
	return w
}

// Describe summarises a schedule in a short phrase for status lines.
func Describe(w Week) string {
	switch {
	case w.Full():
		return "any time"
	case w.Empty():
		return "never (no hours are active)"
	}
	for _, p := range Presets() {
		if p.Week == w {
			return strings.ToLower(p.Label[:1]) + p.Label[1:]
		}
	}
	if n := w.ActiveHours(); n == 1 {
		return "1 hour a week"
	} else {
		return strconv.Itoa(n) + " hours a week"
	}
}
