package queue

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// SpaceFS reports free space and which filesystem a folder is on. Tests
// replace it.
type SpaceFS interface {
	// Free returns the bytes available to JellyTrim in dir's filesystem.
	Free(dir string) (uint64, error)
	// Device returns the ID of dir's filesystem.
	Device(dir string) (uint64, error)
}

type osSpace struct{}

func (osSpace) Free(dir string) (uint64, error)   { return pipeline.OSFS{}.Free(dir) }
func (osSpace) Device(dir string) (uint64, error) { return deviceOf(dir) }

// riskMarginPercent is the share of free space the outlook keeps in hand
// before it calls a filesystem at risk.
const riskMarginPercent = 5

// startNeed is the free space a job needs before the dispatcher starts it.
// It matches the pipeline's own check (the largest estimate, a fifth more,
// and 64 MiB), so a job waits for space rather than starting and being
// skipped. The pipeline still checks again before encoding.
func startNeed(estMax int64) int64 {
	estMax = max(estMax, 0)
	return estMax + estMax/5 + 64<<20
}

// FilesystemOutlook is the space outlook for one filesystem.
type FilesystemOutlook struct {
	Device uint64
	// Path is the deepest folder that holds every waiting job's file and
	// backup on this filesystem, to name it in the UI.
	Path string
	// Jobs is the number of waiting jobs whose files are here.
	Jobs int
	// Free is the space available now. FreeError is set, and Free is 0,
	// when it could not be read.
	Free      int64
	FreeError string
	// Backups is the space held by the originals JellyTrim keeps as
	// backups here (BackupCount files). It is freed as they expire.
	Backups     int64
	BackupCount int
	// Peak is the most extra space the waiting jobs may need at once. While
	// backups are kept, every replaced original stays on disk until it
	// expires, so it is the sum of every job's largest estimated output.
	// With no backups kept, it is the largest outputs of the jobs that run
	// at the same time.
	Peak int64
	// AtRisk is true when Peak is more than the free space less a 5%
	// margin: the queue is likely to stop for lack of space.
	AtRisk bool
}

// SpaceOutlook reports, per filesystem, whether the waiting jobs fit.
type SpaceOutlook struct {
	Filesystems []FilesystemOutlook
	BackupDays  int
	// AtRisk is true when any filesystem is at risk.
	AtRisk bool
}

// fsGroup collects one filesystem's jobs and backups.
type fsGroup struct {
	out     FilesystemOutlook
	dirs    []string // a folder to read free space from, first found first
	outputs []int64  // largest estimated output of each waiting job
}

// SpaceOutlook checks whether the waiting jobs fit on their filesystems,
// grouping jobs by the device their file is on.
func (q *Service) SpaceOutlook(ctx context.Context) (SpaceOutlook, error) {
	st, err := q.store.Settings(ctx)
	if err != nil {
		return SpaceOutlook{}, err
	}
	waiting, err := q.store.WaitingJobSpace(ctx)
	if err != nil {
		return SpaceOutlook{}, err
	}
	backups, err := q.store.JobsWithBackups(ctx)
	if err != nil {
		return SpaceOutlook{}, err
	}
	devices := map[string]uint64{} // folder -> device, so each folder is read once
	groups := map[uint64]*fsGroup{}
	group := func(dev uint64) *fsGroup {
		g := groups[dev]
		if g == nil {
			g = &fsGroup{out: FilesystemOutlook{Device: dev}}
			groups[dev] = g
		}
		return g
	}
	for _, w := range waiting {
		if w.LocalPath == "" {
			continue
		}
		dir := filepath.Dir(w.LocalPath)
		dev, ok := w.Dev, w.DevKnown
		if !ok {
			if dev, ok = q.deviceCached(devices, dir); !ok {
				continue
			}
		}
		g := group(dev)
		g.out.Jobs++
		g.dirs = append(g.dirs, dir)
		g.outputs = append(g.outputs, w.EstMax)
	}
	for _, j := range backups {
		dir := filepath.Dir(j.BackupPath)
		dev, ok := q.deviceCached(devices, dir)
		if !ok {
			continue
		}
		g := group(dev)
		g.out.BackupCount++
		if j.SourceSize != nil {
			g.out.Backups += *j.SourceSize
		}
		g.dirs = append(g.dirs, dir)
	}
	return q.summariseSpace(groups, st), nil
}

func (q *Service) deviceCached(cache map[string]uint64, dir string) (uint64, bool) {
	if dev, ok := cache[dir]; ok {
		return dev, true
	}
	dev, err := q.space.Device(dir)
	if err != nil {
		return 0, false
	}
	cache[dir] = dev
	return dev, true
}

func (q *Service) summariseSpace(groups map[uint64]*fsGroup, st store.Settings) SpaceOutlook {
	out := SpaceOutlook{BackupDays: st.BackupDays}
	for _, g := range groups {
		fo := g.out
		fo.Path = commonDir(g.dirs)
		fo.Peak = peakNeed(g.outputs, st.BackupDays, max(st.Concurrency, 1))
		free, err := q.space.Free(g.dirs[0])
		if err != nil {
			fo.FreeError = err.Error()
		} else {
			fo.Free = int64(min(free, 1<<62))
			fo.AtRisk = fo.Peak > fo.Free-fo.Free*riskMarginPercent/100
		}
		out.AtRisk = out.AtRisk || fo.AtRisk
		out.Filesystems = append(out.Filesystems, fo)
	}
	slices.SortFunc(out.Filesystems, func(a, b FilesystemOutlook) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// peakNeed is the most extra space the jobs need at once: every output
// while backups are kept, otherwise the largest outputs of the jobs that
// run together.
func peakNeed(outputs []int64, backupDays, concurrency int) int64 {
	if backupDays > 0 {
		var sum int64
		for _, o := range outputs {
			sum += max(o, 0)
		}
		return sum
	}
	s := slices.Clone(outputs)
	slices.SortFunc(s, func(a, b int64) int { return cmp.Compare(b, a) })
	var sum int64
	for _, o := range s[:min(concurrency, len(s))] {
		sum += max(o, 0)
	}
	return sum
}

// commonDir is the deepest folder that contains every dir.
func commonDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	common := filepath.Clean(dirs[0])
	for _, d := range dirs[1:] {
		d = filepath.Clean(d)
		for common != d && !strings.HasPrefix(d, strings.TrimSuffix(common, string(filepath.Separator))+string(filepath.Separator)) {
			parent := filepath.Dir(common)
			if parent == common {
				break
			}
			common = parent
		}
	}
	return common
}

// SpaceHold describes the next waiting job, which the queue is not
// starting because its filesystem does not have enough free space.
type SpaceHold struct {
	JobID    int64
	ItemName string
	Dir      string
	Need     int64
	Free     int64
	Since    time.Time
}

// Reason is a plain-English line for the UI.
func (h SpaceHold) Reason() string {
	return fmt.Sprintf("Waiting for free space: %s needs about %s in %s and %s is free. "+
		"JellyTrim will start it when space is freed, for example when backups expire.",
		h.ItemName, units.Bytes(h.Need), h.Dir, units.Bytes(h.Free))
}

// SpaceHold reports whether the dispatcher is holding back a waiting job
// for lack of free space, as of its last look at the queue.
func (q *Service) SpaceHold() (SpaceHold, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.hold == nil {
		return SpaceHold{}, false
	}
	return *q.hold, true
}

// startCandidates is how many waiting jobs the dispatcher looks at, in
// order, for one that has room to start.
const startCandidates = 20

// reservation is the space a running job may still use on its filesystem.
type reservation struct {
	dev  uint64
	need int64
}

// nextStartable returns the first waiting job, in queue order, whose
// filesystem has room for it. Jobs passed over stay waiting. The first job
// held back is recorded for SpaceHold.
func (q *Service) nextStartable(ctx context.Context) (store.Job, reservation, bool) {
	jobs, err := q.store.NextWaitingJobs(ctx, startCandidates)
	if err != nil || len(jobs) == 0 {
		q.setHold(nil)
		return store.Job{}, reservation{}, false
	}
	var held *SpaceHold
	for _, j := range jobs {
		r, h := q.roomFor(j)
		if h == nil {
			q.setHold(held)
			return j, r, true
		}
		if held == nil {
			held = h
		}
	}
	q.setHold(held)
	return store.Job{}, reservation{}, false
}

// roomFor checks free space for a job, allowing for what jobs already
// running on the same filesystem may still write. When free space cannot
// be read the job may start: the pipeline's own check then skips it with a
// reason.
func (q *Service) roomFor(j store.Job) (reservation, *SpaceHold) {
	need := int64(0)
	if j.EstMax != nil {
		need = *j.EstMax
	}
	r := reservation{need: startNeed(need)}
	if j.LocalPath == "" {
		return r, nil
	}
	dir := filepath.Dir(j.LocalPath)
	free, err := q.space.Free(dir)
	if err != nil {
		return r, nil
	}
	if r.dev, err = q.space.Device(dir); err != nil {
		return r, nil
	}
	total := r.need
	q.mu.Lock()
	for _, other := range q.reserved {
		if other.dev == r.dev {
			total += other.need
		}
	}
	q.mu.Unlock()
	if uint64(total) <= free {
		return r, nil
	}
	return r, &SpaceHold{JobID: j.ID, ItemName: j.ItemName, Dir: dir, Need: total, Free: int64(min(free, 1<<62))}
}

// holdLogInterval is how often a job held back for space is logged again.
const holdLogInterval = time.Hour

func (q *Service) setHold(h *SpaceHold) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if h == nil {
		q.hold = nil
		return
	}
	now := q.now()
	h.Since = now
	if q.hold != nil && q.hold.JobID == h.JobID {
		h.Since = q.hold.Since
	}
	if q.hold == nil || q.hold.JobID != h.JobID || now.Sub(q.holdLogged) >= holdLogInterval {
		q.holdLogged = now
		q.log.Warn("queue: not starting a job until there is more free space", "job", h.JobID, "dir", h.Dir,
			"need", units.Bytes(h.Need), "free", units.Bytes(h.Free))
	}
	q.hold = h
}
