package web

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/timetable"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// scheduleState is the processing schedule and where it stands now.
type scheduleState struct {
	week    timetable.Week
	active  bool
	next    time.Time // when the state next changes, if changes
	changes bool
	now     time.Time
}

// scheduleNow reads the processing schedule. The queue's view is used when
// there is a queue, so the page says what the dispatcher does; without one
// (in tests) the schedule is read from the store.
func (s *Server) scheduleNow(ctx context.Context) (scheduleState, error) {
	now := s.Now()
	if s.Queue != nil {
		w, active, next, changes := s.Queue.Schedule(ctx)
		return scheduleState{week: w, active: active, next: next, changes: changes, now: now}, nil
	}
	st, err := s.Store.Settings(ctx)
	if err != nil {
		return scheduleState{}, err
	}
	w, err := timetable.Parse(st.ProcessingSchedule)
	if err != nil {
		// As in the queue: an unreadable schedule runs nothing.
		w = timetable.Week{}
	}
	next, changes := w.NextChange(now)
	return scheduleState{week: w, active: w.Active(now), next: next, changes: changes, now: now}, nil
}

// scheduleWhen is a time within the coming week: "Tuesday 01:00".
func scheduleWhen(t time.Time) string {
	return t.Format("Monday 15:04")
}

// stateText says whether encoding may run now, and until or from when.
func (sc scheduleState) stateText() string {
	switch {
	case sc.week.Empty():
		return "Not active: no hours are switched on"
	case sc.active && sc.changes:
		return "Active now, until " + scheduleWhen(sc.next)
	case sc.active:
		return "Active now"
	case sc.changes:
		return "Next active: " + scheduleWhen(sc.next)
	}
	return "Not active"
}

func (sc scheduleState) lamp() string {
	if sc.active {
		return "ok"
	}
	return "idle"
}

// scheduleHours is "42 hours a week".
func scheduleHours(n int) string {
	if n == 1 {
		return "1 hour a week"
	}
	return strconv.Itoa(n) + " hours a week"
}

// scheduleSummary is "Nights (01:00 to 07:00). 42 hours a week."
func scheduleSummary(w timetable.Week) string {
	if w.Empty() {
		return "No hours are active."
	}
	desc, count := timetable.Describe(w), scheduleHours(w.ActiveHours())
	if desc == count {
		return count + "."
	}
	return strings.ToUpper(desc[:1]) + desc[1:] + ". " + count + "."
}

// scheduleQueueLine is the line under the queue's status, or "" when the
// schedule allows any time.
func (sc scheduleState) queueLine() string {
	if sc.week.Full() {
		return ""
	}
	line := "Processing schedule: " + timetable.Describe(sc.week) + "."
	if sc.week.Empty() {
		return line
	}
	return line + " " + sc.stateText() + "."
}

// scheduleZone names the server's time zone: the TZ variable when it names
// a zone, with the current abbreviation ("Europe/London (BST)"), or just
// the abbreviation ("UTC").
func scheduleZone(now time.Time) string {
	abbr, _ := now.Zone()
	name := strings.TrimPrefix(os.Getenv("TZ"), ":")
	if name == "" || strings.HasPrefix(name, "/") || name == abbr {
		return abbr
	}
	return name + " (" + abbr + ")"
}

// scheduleDays builds the grid rows from a week.
func scheduleDays(w timetable.Week) []views.ScheduleDay {
	out := make([]views.ScheduleDay, 0, timetable.Days)
	for d, name := range timetable.DayNames {
		out = append(out, views.ScheduleDay{Index: d, Name: name, Short: name[:3], Hours: w[d]})
	}
	return out
}

// scheduleParse reads the grid's ticked cells, each "<day>-<hour>". It
// returns false for anything else.
func scheduleParse(values []string) (timetable.Week, bool) {
	var w timetable.Week
	for _, v := range values {
		ds, hs, ok := strings.Cut(v, "-")
		d, errD := strconv.Atoi(ds)
		h, errH := strconv.Atoi(hs)
		if !ok || errD != nil || errH != nil || strconv.Itoa(d) != ds || strconv.Itoa(h) != hs ||
			d < 0 || d >= timetable.Days || h < 0 || h >= timetable.Hours {
			return timetable.Week{}, false
		}
		w[d][h] = true
	}
	return w, true
}

// schedulePreset finds a preset by its ID.
func schedulePreset(id string) (timetable.Preset, bool) {
	for _, p := range timetable.Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return timetable.Preset{}, false
}

// scheduleActive reports whether the processing schedule allows encoding now.
func (s *Server) scheduleActive(ctx context.Context) bool {
	if s.Queue == nil {
		return true
	}
	_, active, _, _ := s.Queue.Schedule(ctx)
	return active
}

// scheduleWaitText says when encoding may start again.
func (s *Server) scheduleWaitText(ctx context.Context) string {
	_, _, next, changes := s.Queue.Schedule(ctx)
	if !changes {
		return "Outside the processing schedule: no hours are active. Choose hours in Settings."
	}
	return "Outside the processing schedule. Encoding starts again " + scheduleWhen(next) + "."
}
