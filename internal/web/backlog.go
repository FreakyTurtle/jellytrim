package web

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/timetable"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// Waiting list paging: the first page, each "Show more" step, and the most
// rows one page shows. A large library can queue tens of thousands of jobs,
// so the list never loads them all.
const (
	queueWaitingStep = 100
	queueWaitingMax  = 1000
)

// queueWaitingLimit reads the ?waiting= row count, rounded up to a whole
// step and capped.
func queueWaitingLimit(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n <= queueWaitingStep {
		return queueWaitingStep
	}
	n = (n + queueWaitingStep - 1) / queueWaitingStep * queueWaitingStep
	return min(n, queueWaitingMax)
}

// queueBacklog is the backlog fragment, refreshed once a minute. It is kept
// out of the 2-second poll because it totals every waiting job and reads
// free space.
func (s *Server) queueBacklog(w http.ResponseWriter, r *http.Request) {
	v, err := s.queueBacklogView(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.QueueBacklogFragment(v))
}

// queueBacklogView estimates the waiting work and checks it fits on disk.
func (s *Server) queueBacklogView(ctx context.Context) (views.QueueBacklog, error) {
	if s.Queue == nil {
		return views.QueueBacklog{}, nil
	}
	b, err := s.Queue.Backlog(ctx)
	if err != nil {
		return views.QueueBacklog{}, err
	}
	out, err := s.Queue.SpaceOutlook(ctx)
	if err != nil {
		return views.QueueBacklog{}, err
	}
	v := backlogView(b)
	for _, fs := range out.Filesystems {
		if fs.AtRisk {
			v.Risks = append(v.Risks, spaceRiskText(fs, out.BackupDays))
		}
	}
	// Like the live fragment, it stops asking once nothing is waiting.
	v.Poll = b.Jobs > 0
	return v, nil
}

// backlogView turns the estimate into the panel's plain words.
func backlogView(b queue.Backlog) views.QueueBacklog {
	v := views.QueueBacklog{Show: b.Jobs > 0, Files: libraryCount(b.Jobs), FilesUnit: backlogWord(b.Jobs, "file", "files")}
	mid := (b.SavingMin + b.SavingMax) / 2
	v.Saving, v.SavingUnit = dashSplit(units.Bytes(mid))
	v.SavingNote = "Estimate: " + dashRange(b.SavingMin, b.SavingMax) + " from " + units.Bytes(b.SourceBytes) + " of files."
	v.Speed = "Rough estimate, based on the speed of recent jobs."
	if b.Speed <= 0 {
		v.Speed = "Rough estimate, based on typical speeds, until JellyTrim has finished a few jobs."
	}
	if b.UnknownDuration > 0 {
		v.Unknown = backlogFiles(b.UnknownDuration) + " no known length and " + backlogWord(b.UnknownDuration, "is", "are") + " not counted."
	}
	switch {
	case b.ActiveHoursPerWeek == 0:
		v.TimeNote = "No processing hours are switched on, so nothing will run."
		v.ScheduleLink = true
	case b.ProcessingTime <= 0:
		v.TimeNote = "No waiting file has a known length."
	case b.ActiveHoursPerWeek >= timetable.Slots:
		v.TimeValue, v.TimeUnit = backlogDuration(b.CalendarTime)
		v.TimeNote = "Estimated encoding time. The schedule allows any hour."
	default:
		v.TimeValue, v.TimeUnit = backlogDuration(b.CalendarTime)
		v.TimeNote = "Estimate at your " + strconv.Itoa(b.ActiveHoursPerWeek) + " active " + backlogWord(b.ActiveHoursPerWeek, "hour", "hours") + " a week."
		v.ScheduleLink = true
	}
	return v
}

// backlogFiles is "1 file has" or "12 files have".
func backlogFiles(n int) string {
	if n == 1 {
		return "1 file has"
	}
	return libraryCount(n) + " files have"
}

func backlogWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Thresholds for backlogDuration: hours below 2 days, days below 3 weeks,
// weeks below 12 weeks, then months.
const (
	backlogDay       = 24 * time.Hour
	backlogWeek      = 7 * backlogDay
	backlogMonthDays = 30.44
)

// backlogDuration rounds a long duration to the unit a person would use:
// "36" "hours", "9" "days", "3" "weeks", "5" "months".
func backlogDuration(d time.Duration) (value, unit string) {
	count := func(n float64, one, many string) (string, string) {
		c := max(int(math.Round(n)), 1)
		return strconv.Itoa(c), backlogWord(c, one, many)
	}
	switch {
	case d < 2*backlogDay:
		return count(d.Hours(), "hour", "hours")
	case d < 3*backlogWeek:
		return count(d.Hours()/24, "day", "days")
	case d < 12*backlogWeek:
		return count(d.Hours()/24/7, "week", "weeks")
	}
	return count(d.Hours()/24/backlogMonthDays, "month", "months")
}

// spaceRiskText warns that the waiting jobs may not fit on a filesystem.
func spaceRiskText(fs queue.FilesystemOutlook, backupDays int) string {
	text := "The queue needs about " + units.Bytes(fs.Peak) + " on " + fs.Path + ", but only " + units.Bytes(fs.Free) + " is free"
	if backupDays > 0 {
		return text + " while backups are kept for " + backlogDays(backupDays) + "."
	}
	return text + "."
}

func backlogDays(n int) string {
	if n == 1 {
		return "1 day"
	}
	return strconv.Itoa(n) + " days"
}

// queueHoldView explains a job held back for free space, or nil.
func (s *Server) queueHoldView() *views.QueueHold {
	if s.Queue == nil {
		return nil
	}
	h, ok := s.Queue.SpaceHold()
	if !ok {
		return nil
	}
	return &views.QueueHold{Reason: h.Reason()}
}

// backupUse is the Settings hint on the space backups hold now.
func (s *Server) backupUse(ctx context.Context) (string, error) {
	if s.Queue == nil {
		return "", nil
	}
	out, err := s.Queue.SpaceOutlook(ctx)
	if err != nil {
		return "", err
	}
	var bytes int64
	count := 0
	for _, fs := range out.Filesystems {
		bytes += fs.Backups
		count += fs.BackupCount
	}
	if count == 0 {
		return "No backups are kept at the moment.", nil
	}
	return "Backups currently hold " + units.Bytes(bytes) + " in " + libraryCount(count) + " " + backlogWord(count, "file", "files") + ".", nil
}
