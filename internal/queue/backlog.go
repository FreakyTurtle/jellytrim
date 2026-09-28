package queue

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/timetable"
)

// Encode speeds (multiples of real time) assumed before any job has
// finished. They are rough: real speeds depend on the CPU or GPU, the
// preset, the resolution and the source codec.
const (
	defaultSoftwareSpeed = 1.0
	defaultHardwareSpeed = 3.0
	// speedHistory is how many recent complete jobs the median speed uses.
	speedHistory = 20
)

// Backlog is a rough estimate of the work waiting in the queue.
type Backlog struct {
	Jobs        int
	SourceBytes int64
	// SavingMin and SavingMax are the estimated saving range: source size
	// minus the largest and the smallest estimated output.
	SavingMin int64
	SavingMax int64
	// ProcessingTime is the encoding time for every waiting job: each
	// source's duration divided by the encode speed, summed, and divided by
	// the number of jobs run at once.
	ProcessingTime time.Duration
	// Speed is the median encode speed of the latest complete jobs, and
	// SpeedSamples how many it came from. Speed is 0 when there is no
	// history, and each job then uses the default for its encoder (1x for
	// software, 3x for hardware).
	Speed        float64
	SpeedSamples int
	// UnknownDuration counts jobs whose source duration is unknown. They
	// are left out of ProcessingTime.
	UnknownDuration int
	Concurrency     int
	// ActiveHoursPerWeek is the number of hours a week the processing
	// schedule allows encoding.
	ActiveHoursPerWeek int
	// CalendarTime is how long the backlog takes on the processing
	// schedule: ProcessingTime / (ActiveHoursPerWeek / 168). It is 0 when the
	// schedule has no active hours.
	CalendarTime time.Duration
}

// Backlog estimates how much work is waiting and how long it will take.
func (q *Service) Backlog(ctx context.Context) (Backlog, error) {
	st, err := q.store.Settings(ctx)
	if err != nil {
		return Backlog{}, err
	}
	groups, err := q.store.WaitingByEncoder(ctx)
	if err != nil {
		return Backlog{}, err
	}
	speeds, err := q.store.RecentEncodeSpeeds(ctx, speedHistory)
	if err != nil {
		return Backlog{}, err
	}
	b := Backlog{Concurrency: max(st.Concurrency, 1), Speed: median(speeds), SpeedSamples: len(speeds)}
	var seconds float64
	for _, g := range groups {
		b.Jobs += g.Jobs
		b.SourceBytes += g.SourceBytes
		b.SavingMin += g.SavingMin
		b.SavingMax += g.SavingMax
		b.UnknownDuration += g.NoDuration
		speed := b.Speed
		if speed <= 0 {
			speed = q.defaultSpeed(g.Encoder)
		}
		seconds += float64(g.DurationMs) / 1000 / speed
	}
	b.ProcessingTime = time.Duration(seconds / float64(b.Concurrency) * float64(time.Second))
	week, err := timetable.Parse(st.ProcessingSchedule)
	if err != nil {
		return b, fmt.Errorf("reading the processing schedule: %w", err)
	}
	b.ActiveHoursPerWeek = week.ActiveHours()
	if b.ActiveHoursPerWeek > 0 {
		b.CalendarTime = time.Duration(float64(b.ProcessingTime) * timetable.Slots / float64(b.ActiveHoursPerWeek))
	}
	return b, nil
}

// defaultSpeed is the assumed speed of an encoder with no history.
func (q *Service) defaultSpeed(name string) float64 {
	var b encoder.Backend
	ok := false
	if q.registry != nil {
		b, ok = q.registry.Backend(name)
	}
	if !ok {
		for _, d := range encoder.DefaultBackends("") {
			if d.Name() == name {
				b, ok = d, true
			}
		}
	}
	if ok && b.Hardware() {
		return defaultHardwareSpeed
	}
	return defaultSoftwareSpeed
}

// median returns the middle value, or 0 for none.
func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	mid := len(s) / 2
	if len(s)%2 == 0 {
		return (s[mid-1] + s[mid]) / 2
	}
	return s[mid]
}
