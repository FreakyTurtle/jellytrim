package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// historyPageSize is the number of finished jobs per History page.
const historyPageSize = 50

// queueStages are the running stages in order, for the stage strip.
var queueStages = []string{store.JobAnalysing, store.JobEncoding, store.JobValidating, store.JobReplacing}

// historyFilters are the History status filters. Anything else is ignored.
var historyFilters = []views.Option{
	{Value: "", Label: "All"},
	{Value: store.JobComplete, Label: "Complete"},
	{Value: store.JobSkipped, Label: "Skipped"},
	{Value: store.JobFailed, Label: "Failed"},
	{Value: store.JobCancelled, Label: "Cancelled"},
	{Value: store.JobAttention, Label: "Needs attention"},
}

// queueNotices are the messages a redirect back to /queue can carry.
var queueNotices = map[string]views.QueueNotice{
	"cancelled":      {Variant: "ok", Title: "Job cancelled", Text: "The original file is not affected."},
	"notcancellable": {Variant: "warn", Title: "That job could not be cancelled", Text: "It is no longer running. If it finished, its result is in History."},
}

// ---------- Queue page ----------

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shell(r, "Queue", "queue")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v, err := s.queueLiveView(r.Context(), "", queueWaitingLimit(r.URL.Query().Get("waiting")))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if n, ok := queueNotices[r.URL.Query().Get("notice")]; ok {
		v.Notice = &n
	}
	b, err := s.queueBacklogView(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.QueuePage(views.QueuePageData{Shell: sh, Live: v, Backlog: b}))
}

// queueLive is the polled fragment: the queue state, running and waiting jobs.
func (s *Server) queueLive(w http.ResponseWriter, r *http.Request) {
	v, err := s.queueLiveView(r.Context(), r.URL.Query().Get("was"), queueWaitingLimit(r.URL.Query().Get("waiting")))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.QueueLiveFragment(v))
}

// queueRunningJobs lists jobs in a running stage, oldest first, without
// loading the waiting jobs, which can number tens of thousands.
func (s *Server) queueRunningJobs(ctx context.Context) ([]store.Job, error) {
	// InterruptedJobs is every job in a running stage plus those that need
	// attention; only the running ones are active.
	jobs, err := s.Store.InterruptedJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := jobs[:0]
	for _, j := range jobs {
		if j.Active() {
			out = append(out, j)
		}
	}
	return out, nil
}

// queueLiveView builds the live part of the queue page. was is the running
// jobs' signature the browser last saw; a change is announced once. limit
// is how many waiting jobs to list. It stays light, because it runs on
// every 2-second poll: the waiting total is a count, and only the listed
// jobs are loaded.
func (s *Server) queueLiveView(ctx context.Context, was string, limit int) (views.QueueLive, error) {
	st, err := s.Store.Settings(ctx)
	if err != nil {
		return views.QueueLive{}, err
	}
	running, err := s.queueRunningJobs(ctx)
	if err != nil {
		return views.QueueLive{}, err
	}
	active, err := s.Store.ActiveJobCount(ctx)
	if err != nil {
		return views.QueueLive{}, err
	}
	waiting, err := s.Store.NextWaitingJobs(ctx, limit)
	if err != nil {
		return views.QueueLive{}, err
	}
	live := map[int64]queue.Live{}
	paused := false
	if s.Queue != nil {
		live = s.Queue.Live()
		paused = s.Queue.Paused()
	}
	v := views.QueueLive{DryRun: st.DryRun, Paused: paused, Hold: s.queueHoldView()}
	for _, j := range running {
		v.Running = append(v.Running, s.queueRunningView(j, live[j.ID]))
	}
	for _, j := range waiting {
		v.Waiting = append(v.Waiting, queueWaitingRow(j, len(v.Waiting)+1))
	}
	queueWaitingPaging(&v, max(active-len(running), len(waiting)), limit)
	v.Lamp, v.State = s.queueState(ctx, st, paused, len(v.Running))
	sc, err := s.scheduleNow(ctx)
	if err != nil {
		return v, err
	}
	v.Schedule = sc.queueLine()
	v.Poll = len(v.Running) > 0 || (v.WaitingTotal > 0 && !paused && !st.DryRun)
	sig := queueSignature(v.Running)
	v.LiveURL = "/queue/live?was=" + url.QueryEscape(sig)
	if was != "" && was != sig {
		v.Announce = s.queueAnnounce(ctx, was, v.Running)
	}
	if v.Failed, err = s.queueRecentFailed(ctx); err != nil {
		return v, err
	}
	return v, nil
}

// queueWaitingPaging fills the waiting list's count line and its Show more
// step.
func queueWaitingPaging(v *views.QueueLive, total, limit int) {
	v.WaitingTotal = total
	v.WaitingLimit = limit
	shown := len(v.Waiting)
	switch {
	case total == 0:
		return
	case shown >= total:
		v.WaitingCount = backlogWord(total, "1 job waiting.", libraryCount(total)+" jobs waiting.")
		return
	}
	v.WaitingCount = "Showing the first " + libraryCount(shown) + " of " + libraryCount(total) + " waiting jobs."
	if limit < queueWaitingMax {
		v.MoreLimit = min(limit+queueWaitingStep, queueWaitingMax)
	}
}

// queueState is the status line at the top of the queue.
func (s *Server) queueState(ctx context.Context, st store.Settings, paused bool, running int) (lamp, text string) {
	switch {
	case st.DryRun:
		return "idle", "Dry Run is on. Nothing is processed."
	case paused && running > 0:
		return "warn", "Paused. The running job finishes, then nothing new starts."
	case paused:
		return "warn", "Paused. Nothing new starts until you resume."
	case !s.scheduleActive(ctx):
		return "idle", s.scheduleWaitText(ctx)
	}
	n := max(st.Concurrency, 1)
	if n == 1 {
		return "ok", "Running. 1 job at a time."
	}
	return "ok", "Running. " + strconv.Itoa(n) + " jobs at a time."
}

func (s *Server) queueRunningView(j store.Job, l queue.Live) views.QueueRunning {
	status, frac, speed := j.Status, j.Progress, j.Speed
	var eta time.Duration
	if j.ETASeconds != nil {
		eta = time.Duration(*j.ETASeconds) * time.Second
	}
	encoder := j.Encoder
	if l.Status != "" {
		status, frac, speed, eta = l.Status, l.Fraction, l.Speed, l.ETA
	}
	if l.Encoder != "" {
		encoder = l.Encoder
	}
	v := views.QueueRunning{
		ID: j.ID, Title: j.ItemName, Href: jobItemHref(j), Library: j.LibraryName, Change: jobChange(j, true),
		Policy: j.PolicyName, Status: jobStatusWord(status), Stages: queueStageStrip(status), Encoder: encoder,
		Saving: jobEstSaving(j), Percent: -1, Readout: queueStageText(status),
	}
	if status == store.JobEncoding {
		v.Percent = int(frac * 100)
		v.Readout = queueReadout(frac, speed, eta)
	}
	if j.StartedAt != nil {
		v.Elapsed = units.Duration(s.Now().Sub(*j.StartedAt))
	}
	return v
}

func queueWaitingRow(j store.Job, pos int) views.QueueWaiting {
	return views.QueueWaiting{
		ID: j.ID, Position: pos, Title: j.ItemName, Href: jobItemHref(j), Library: j.LibraryName,
		Change: jobChange(j, true), Policy: j.PolicyName, Saving: jobEstSaving(j),
		// A waiting job only has a summary when it was stopped and queued
		// again: by the processing schedule, or by a restart.
		Note: j.Summary,
	}
}

// queueStageStrip marks each running stage as done, current or still to come.
func queueStageStrip(status string) []views.QueueStage {
	current := -1
	for i, st := range queueStages {
		if st == status {
			current = i
		}
	}
	out := make([]views.QueueStage, 0, len(queueStages))
	for i, st := range queueStages {
		state := "todo"
		switch {
		case i < current:
			state = "done"
		case i == current:
			state = "current"
		}
		out = append(out, views.QueueStage{Name: jobStatusWord(st), State: state})
	}
	return out
}

// queueStageText is the meter readout for stages without a percentage.
func queueStageText(status string) string {
	switch status {
	case store.JobAnalysing:
		return "Checking the file and planning the encode"
	case store.JobEncoding:
		return "Starting the encoder"
	case store.JobValidating:
		return "Checking the new file against the plan"
	case store.JobReplacing:
		return "Swapping the new file in and keeping a backup"
	}
	return "Working"
}

// queueReadout is "38% · 2.4× · 12 min left", leaving out what is unknown.
func queueReadout(frac, speed float64, eta time.Duration) string {
	parts := []string{units.Percent(frac)}
	if speed > 0 {
		parts = append(parts, strconv.FormatFloat(speed, 'f', 1, 64)+"×")
	}
	if eta > 0 {
		parts = append(parts, units.Duration(eta)+" left")
	}
	return strings.Join(parts, " · ")
}

// queueSignature identifies the running jobs and their stages, so the next
// poll can tell whether anything worth announcing changed.
func queueSignature(running []views.QueueRunning) string {
	if len(running) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(running))
	for _, r := range running {
		parts = append(parts, strconv.FormatInt(r.ID, 10)+":"+r.Status)
	}
	return strings.Join(parts, ",")
}

// queueAnnounce describes stage changes and finished jobs since the last poll,
// for the polite live region. Percentages are never announced.
func (s *Server) queueAnnounce(ctx context.Context, was string, running []views.QueueRunning) string {
	before := map[int64]string{}
	for _, part := range strings.Split(was, ",") {
		id, status, ok := strings.Cut(part, ":")
		n, err := strconv.ParseInt(id, 10, 64)
		if ok && err == nil {
			before[n] = status
		}
	}
	var msgs []string
	for _, r := range running {
		if before[r.ID] != r.Status {
			msgs = append(msgs, r.Title+": "+r.Status+".")
		}
		delete(before, r.ID)
	}
	for id := range before {
		j, err := s.Store.Job(ctx, id)
		if err != nil || j.Active() {
			continue
		}
		msgs = append(msgs, j.ItemName+": "+jobStatusWord(j.Status)+".")
	}
	return strings.Join(msgs, " ")
}

// queueRecentFailed lists jobs that failed in the last hour.
func (s *Server) queueRecentFailed(ctx context.Context) ([]views.QueueFailed, error) {
	jobs, err := s.Store.HistoryJobs(ctx, store.JobFailed, 5, 0)
	if err != nil {
		return nil, err
	}
	cutoff := s.Now().Add(-time.Hour)
	var out []views.QueueFailed
	for _, j := range jobs {
		if j.FinishedAt == nil || j.FinishedAt.Before(cutoff) {
			continue
		}
		out = append(out, views.QueueFailed{
			ID: j.ID, Title: j.ItemName, Href: jobItemHref(j), Summary: j.Summary, When: units.Ago(*j.FinishedAt, s.Now()),
		})
	}
	return out, nil
}

// ---------- Queue actions ----------

func (s *Server) pauseQueue(w http.ResponseWriter, r *http.Request)  { s.setPaused(w, r, true) }
func (s *Server) resumeQueue(w http.ResponseWriter, r *http.Request) { s.setPaused(w, r, false) }

func (s *Server) setPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	if s.Queue == nil {
		s.queueUnavailable(w, r)
		return
	}
	s.Queue.SetPaused(paused)
	s.queueAfterAction(w, r, "")
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	if s.Queue == nil {
		s.queueUnavailable(w, r)
		return
	}
	id, ok := jobID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	notice := "cancelled"
	if err := s.Queue.Cancel(r.Context(), id); errors.Is(err, queue.ErrNotCancelled) {
		notice = "notcancellable"
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.queueAfterAction(w, r, notice)
}

// queueAfterAction answers an HTMX request with the live fragment and a
// fresh backlog, and a plain form post with a redirect back to the queue.
func (s *Server) queueAfterAction(w http.ResponseWriter, r *http.Request, notice string) {
	if r.Header.Get("HX-Request") != "true" {
		to := "/queue"
		if notice != "" {
			to += "?notice=" + url.QueryEscape(notice)
		}
		redirect(w, r, to)
		return
	}
	v, err := s.queueLiveView(r.Context(), "", queueWaitingLimit(r.FormValue("waiting")))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if n, ok := queueNotices[notice]; ok {
		n.Alert = true
		v.Notice = &n
	}
	b, err := s.queueBacklogView(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	b.OOB = true
	s.render(w, r, views.QueueActionResult(v, b))
}

func (s *Server) queueUnavailable(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "The queue is not running. Restart JellyTrim and try again.", http.StatusServiceUnavailable)
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	if s.Queue == nil {
		s.queueUnavailable(w, r)
		return
	}
	id, ok := jobID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := s.Store.Job(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if _, err := s.Queue.Retry(r.Context(), id); err != nil {
		s.Log.Warn("queue: retry refused", "job", id, "err", err)
		s.historyJobPage(w, r, id, retryProblem(err))
		return
	}
	redirect(w, r, "/queue")
}

// retryProblem explains why a job could not be queued again.
func retryProblem(err error) *views.QueueNotice {
	p := &views.QueueNotice{Variant: "bad", Title: "JellyTrim could not queue this item again", Alert: true}
	switch {
	case errors.Is(err, queue.ErrDryRun):
		p.Variant, p.Text = "warn", "Dry Run is on, so JellyTrim will not change files. Turn off Dry Run in Settings, then try again."
	case errors.Is(err, queue.ErrNotOptimise):
		p.Variant, p.Text = "warn", "After checking the file again, JellyTrim no longer plans to change it. The item's page explains why."
	case errors.Is(err, queue.ErrNotRetryable):
		p.Variant, p.Text = "warn", "Only failed, skipped or cancelled jobs can be tried again."
	default:
		p.Text = "Checking the file again did not work, so nothing was queued. The original file is unchanged."
		p.Detail = err.Error()
	}
	return p
}

func (s *Server) restoreJob(w http.ResponseWriter, r *http.Request) {
	if s.Queue == nil {
		s.queueUnavailable(w, r)
		return
	}
	id, ok := jobID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.Queue.Restore(r.Context(), id)
	switch {
	case err == nil:
		redirect(w, r, "/history/"+strconv.FormatInt(id, 10)+"?restored=1")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	default:
		s.Log.Warn("queue: restore refused", "job", id, "err", err)
		s.historyJobPage(w, r, id, restoreProblem(err))
	}
}

// restoreProblem explains why the original could not be put back. Nothing
// was changed in every case.
func restoreProblem(err error) *views.QueueNotice {
	p := &views.QueueNotice{Variant: "warn", Alert: true}
	switch {
	case errors.Is(err, queue.ErrNoBackup):
		p.Title = "There is no backup to restore"
		p.Text = "The backup was already restored or deleted. The file in your library is the optimised one."
	case errors.Is(err, queue.ErrItemBusy):
		p.Title = "Another job is working on this item"
		p.Text = "Nothing was changed. Try again when that job has finished; the Queue page shows its progress."
	case errors.Is(err, pipeline.ErrNotOurFile):
		p.Title = "The file has changed since JellyTrim optimised it"
		p.Text = "Something else, such as a new download, replaced the file. Restoring would delete it, so nothing was changed. The backup is kept until backups expire."
	default:
		p.Variant, p.Title = "bad", "JellyTrim could not put the original back"
		p.Text = "The optimised file is still in place and the backup is kept. Check the technical details, then try again."
		p.Detail = err.Error()
	}
	return p
}

// ---------- Item actions ----------

// optimiseItem queues one item now, at the user's request.
func (s *Server) optimiseItem(w http.ResponseWriter, r *http.Request) {
	it, err := s.Store.Item(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if s.Queue == nil {
		s.queueUnavailable(w, r)
		return
	}
	back := "/library/" + url.PathEscape(it.ID)
	_, _, err = s.Queue.Enqueue(r.Context(), it.ID, "manual")
	switch {
	case err == nil:
		redirect(w, r, "/queue")
	case errors.Is(err, queue.ErrDryRun):
		redirect(w, r, back+"?error=dryrun")
	case errors.Is(err, queue.ErrNotOptimise):
		redirect(w, r, back+"?error=notplanned")
	default:
		s.serverError(w, r, err)
	}
}

// includeItem lets JellyTrim change an excluded item again and re-evaluates.
func (s *Server) includeItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.Store.Item(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.Store.IncludeItem(ctx, it.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	busy := false
	if s.Library != nil {
		// Evaluating is quick, so the page shows the new decision at once.
		// During a sync, the sync's own evaluation picks the change up.
		if _, err := s.Library.Evaluate(ctx); errors.Is(err, library.ErrBusy) {
			busy = true
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	itemRedirect(w, r, it.ID, busy)
}

// itemQueueState fills in the Optimise now controls on the item page: the
// button, an active job, the exclusion control and any error from a
// previous attempt.
func (s *Server) itemQueueState(ctx context.Context, r *http.Request, it store.Item, dryRun bool, v *views.ItemPageData) error {
	id := url.PathEscape(it.ID)
	o := &v.Optimise
	if o.Show {
		o.Enabled = !dryRun && s.Queue != nil
		o.Action = "/library/" + id + "/optimise"
		if dryRun {
			o.Reason = "Dry Run is on. Turn it off in Settings to optimise files."
		} else if s.Queue == nil {
			o.Reason = "The queue is not running."
		}
		if o.Enabled {
			st, err := s.Store.Settings(ctx)
			if err != nil {
				return err
			}
			o.Backup = itemBackupText(st.BackupDays)
		}
	}
	// One indexed lookup: the queue can hold tens of thousands of jobs.
	_, queued, err := s.Store.ActiveJobForItem(ctx, it.ID)
	if err != nil {
		return err
	}
	o.Queued = queued
	ev, err := s.Store.Evaluation(ctx, it.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	o.Excluded = itemExcluded(ev)
	o.IncludeAction = "/library/" + id + "/include"
	switch r.URL.Query().Get("error") {
	case "dryrun":
		o.Error = &views.QueueNotice{Variant: "warn", Title: "Dry Run is on", Text: "JellyTrim will not change files while Dry Run is on. Turn it off in Settings to optimise this item."}
	case "notplanned":
		o.Error = &views.QueueNotice{Variant: "warn", Title: "Nothing to optimise", Text: "JellyTrim no longer plans to change this item. The Policy section explains why."}
	}
	return nil
}

// itemBackupText says how long the original is kept after Optimise now.
func itemBackupText(days int) string {
	switch {
	case days <= 0:
		return "The original is kept as a backup until Jellyfin has picked up the change."
	case days == 1:
		return "The original is kept as a backup for 1 day."
	}
	return "The original is kept as a backup for " + strconv.Itoa(days) + " days."
}

// itemExcluded reports whether the user excluded the item (by restoring it).
func itemExcluded(ev store.Evaluation) bool {
	var reasons []plan.Reason
	if json.Unmarshal([]byte(ev.Reasons), &reasons) != nil {
		return false
	}
	for _, r := range reasons {
		if r.Code == "excluded" {
			return true
		}
	}
	return false
}

// ---------- History ----------

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	status := historyStatus(q.Get("status"))
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	sh, err := s.shell(r, "History", "history")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	jobs, err := s.Store.HistoryJobs(ctx, status, historyPageSize+1, (page-1)*historyPageSize)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	saved, files, err := s.Store.SavedBytes(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := views.HistoryPageData{Shell: sh, Status: status, Page: page, SavedFiles: files}
	v.SavedValue, v.SavedUnit = dashSplit(units.Bytes(saved))
	v.Total = historyTotal(saved, files)
	for _, f := range historyFilters {
		v.Filters = append(v.Filters, views.HistoryFilter{Label: f.Label, Href: historyHref(f.Value, 1), Current: f.Value == status})
	}
	if len(jobs) > historyPageSize {
		jobs = jobs[:historyPageSize]
		v.NextHref = historyHref(status, page+1)
	}
	if page > 1 {
		v.PrevHref = historyHref(status, page-1)
	}
	for _, j := range jobs {
		v.Rows = append(v.Rows, s.historyRow(j))
	}
	if status == "" && page == 1 {
		attention, err := s.Store.JobsNeedingAttention(ctx)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		for _, j := range attention {
			v.Attention = append(v.Attention, s.historyRow(j))
		}
	}
	s.render(w, r, views.HistoryPage(v))
}

func historyStatus(v string) string {
	for _, f := range historyFilters {
		if f.Value == v {
			return v
		}
	}
	return ""
}

func historyHref(status string, page int) string {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/history"
	}
	return "/history?" + q.Encode()
}

func historyTotal(saved int64, files int) string {
	if files == 0 {
		return "Nothing optimised yet. Savings appear here once jobs complete."
	}
	noun := "files"
	if files == 1 {
		noun = "file"
	}
	return "JellyTrim has saved " + units.Bytes(saved) + " across " + libraryCount(files) + " " + noun + "."
}

func (s *Server) historyRow(j store.Job) views.HistoryRow {
	row := views.HistoryRow{
		ID: j.ID, Title: j.ItemName, Href: jobItemHref(j), Change: jobChange(j, false),
		Lamp: jobLamp(j), Status: jobResultWord(j), Result: jobResult(j),
		ViewHref: "/history/" + strconv.FormatInt(j.ID, 10),
	}
	if t := j.FinishedAt; t != nil {
		row.When = units.Ago(*t, s.Now())
		row.WhenFull = t.Local().Format("2 Jan 2006 15:04")
		row.WhenISO = t.UTC().Format(time.RFC3339)
	}
	return row
}

// ---------- Job detail ----------

func (s *Server) historyJob(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.historyJobPage(w, r, id, nil)
}

// historyJobPage renders one job, with an optional problem from an action
// that did not work (shown with status 409).
func (s *Server) historyJobPage(w http.ResponseWriter, r *http.Request, id int64, problem *views.QueueNotice) {
	j, err := s.Store.Job(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sh, err := s.shell(r, j.ItemName, "history")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := s.historyJobView(j)
	v.Shell = sh
	v.Problem = problem
	v.JustRestored = r.URL.Query().Get("restored") == "1" && j.RestoredAt != nil
	if problem != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
	}
	s.render(w, r, views.HistoryJobPage(v))
}

func (s *Server) historyJobView(j store.Job) views.HistoryJobData {
	var d pipeline.Diagnostics
	_ = json.Unmarshal([]byte(j.Diagnostics), &d)
	sid := strconv.FormatInt(j.ID, 10)
	v := views.HistoryJobData{
		ID: j.ID, Title: j.ItemName, ItemHref: jobItemHref(j), Summary: historySummary(j),
		Facts: s.historyFacts(j, d), Warnings: d.Warnings, Error: d.Error, Stderr: d.StderrTail,
		RestoreAction: "/history/" + sid + "/restore", RetryAction: "/queue/" + sid + "/retry",
		Active: j.Active(),
	}
	if len(d.Args) > 0 {
		v.Command = shellCommand("ffmpeg", d.Args)
	}
	for _, c := range d.Checks {
		v.Checks = append(v.Checks, views.ExplainLine{Pass: c.Pass, Text: historyCheckText(c)})
	}
	switch j.Status {
	case store.JobComplete:
		if j.BackupPath != "" {
			v.CanRestore = true
			if j.BackupExpiresAt != nil {
				v.BackupNote = "Backup kept until " + j.BackupExpiresAt.Local().Format("2 Jan 2006") + "."
			} else {
				v.BackupNote = "The original is kept as a backup."
			}
		}
	case store.JobFailed, store.JobSkipped, store.JobCancelled:
		v.CanRetry = true
		v.RetryPrimary = j.Status == store.JobFailed
	}
	v.Kept = historyKept(j)
	if j.RestoredAt != nil {
		v.RestoredNote = "Original restored " + j.RestoredAt.Local().Format("2 Jan 2006 15:04") + "."
	}
	if t := j.FinishedAt; t != nil {
		v.Subtitle = "Finished " + units.Ago(*t, s.Now())
	} else {
		v.Subtitle = jobStatusWord(j.Status)
	}
	return v
}

// historyKept says where a file JellyTrim set aside is, for jobs that are
// not a normal completion: the original after a job that needs attention, or
// the previous version when another program replaced the file mid-job.
func historyKept(j store.Job) *views.HistoryKept {
	if j.BackupPath == "" {
		return nil
	}
	switch j.Status {
	case store.JobAttention:
		return &views.HistoryKept{Prefix: "The original is safe at", Path: j.BackupPath,
			Suffix: "JellyTrim will not change this item until you move it back."}
	case store.JobSkipped:
		k := &views.HistoryKept{Prefix: "Kept at", Path: j.BackupPath}
		if j.BackupExpiresAt != nil {
			k.Suffix = "until " + j.BackupExpiresAt.Local().Format("2 Jan 2006") + "."
		}
		return k
	}
	return nil
}

// historySummary is the callout at the top of a job: ok, warn or bad.
func historySummary(j store.Job) views.QueueNotice {
	n := views.QueueNotice{Text: j.Summary}
	switch j.Status {
	case store.JobComplete:
		n.Variant, n.Title = "ok", "Complete"
		if j.RestoredAt != nil {
			n.Variant, n.Title = "info", "Original restored"
			n.Text = "The optimised file was deleted and Jellyfin was told about the change. JellyTrim leaves this item alone until you allow it again on its page."
		} else if saved, ok := jobSaved(j); ok {
			n.Title = "Complete. Saved " + units.Bytes(saved)
		}
	case store.JobSkipped:
		n.Variant, n.Title = "warn", "Skipped"
	case store.JobFailed:
		n.Variant, n.Title = "bad", "Failed"
	case store.JobCancelled:
		n.Variant, n.Title = "warn", "Cancelled"
	case store.JobAttention:
		n.Variant, n.Title = "bad", "Needs attention"
	default:
		n.Variant, n.Title = "info", "In the queue: "+jobStatusWord(j.Status)
		n.Text = "This job has not finished yet. Its progress is on the Queue page."
	}
	if n.Text == "" {
		n.Text = "No further detail was recorded."
	}
	return n
}

func (s *Server) historyFacts(j store.Job, d pipeline.Diagnostics) []views.ItemStat {
	facts := []views.ItemStat{
		{Label: "Library", Value: j.LibraryName},
		{Label: "File", Value: j.LocalPath, Mono: true},
		{Label: "Change", Value: jobChange(j, true), Mono: true},
		{Label: "Policy", Value: orText(j.PolicyName, "None recorded")},
		{Label: "Started by", Value: jobTrigger(j.Trigger)},
		{Label: "Queued", Value: historyTime(&j.CreatedAt)},
		{Label: "Started", Value: historyTime(j.StartedAt)},
		{Label: "Finished", Value: historyTime(j.FinishedAt)},
	}
	if j.StartedAt != nil && j.FinishedAt != nil {
		facts = append(facts, views.ItemStat{Label: "Duration", Value: units.Duration(j.FinishedAt.Sub(*j.StartedAt)), Mono: true})
	}
	if enc := orText(d.Encoder, j.Encoder); enc != "" {
		facts = append(facts, views.ItemStat{Label: "Encoder", Value: enc})
	}
	if d.Speed > 0 {
		facts = append(facts, views.ItemStat{Label: "Speed", Value: strconv.FormatFloat(d.Speed, 'f', 1, 64) + "× real time", Mono: true})
	}
	if d.EncodeTime != "" {
		facts = append(facts, views.ItemStat{Label: "Encode time", Value: d.EncodeTime, Mono: true})
	}
	if j.SourceSize != nil {
		facts = append(facts, views.ItemStat{Label: "Size before", Value: units.Bytes(*j.SourceSize), Mono: true})
	}
	if j.OutputSize != nil {
		facts = append(facts, views.ItemStat{Label: "Size after", Value: units.Bytes(*j.OutputSize), Mono: true})
	}
	if saved, ok := jobSaved(j); ok && *j.SourceSize > 0 {
		pct := units.Percent(float64(saved) / float64(*j.SourceSize))
		facts = append(facts, views.ItemStat{Label: "Saved", Value: units.Bytes(saved) + " (" + pct + ")", Mono: true})
	}
	return facts
}

func historyTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("2 Jan 2006 15:04")
}

// historyCheckText is one validation check in words.
func historyCheckText(c pipeline.Check) string {
	name := strings.ReplaceAll(c.Name, "_", " ")
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	switch {
	case c.Detail == "":
		return name
	case name == "":
		return c.Detail
	}
	return name + ": " + c.Detail
}

// shellCommand joins a command for display with shell-style quoting, so it
// can be copied into a terminal. It is never executed.
func shellCommand(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, bin)
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(a string) string {
	if a == "" {
		return "''"
	}
	for _, r := range a {
		if !shellSafe(r) {
			return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return a
}

func shellSafe(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_./:=,+@%", r)
}

// ---------- Shared job helpers ----------

func jobID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func jobItemHref(j store.Job) string {
	return "/library/" + url.PathEscape(j.ItemID)
}

// jobChange is "2160p H.264 → 1080p HEVC", with " · High" when withQuality.
func jobChange(j store.Job, withQuality bool) string {
	c := dashChange(j.SourceSummary, j.TargetSummary)
	if !withQuality {
		return c
	}
	var p plan.Plan
	if json.Unmarshal([]byte(j.Plan), &p) == nil && p.Quality != "" {
		c += " · " + policy.QualityLabel(p.Quality)
	}
	return c
}

// jobEstSaving is the estimated saving range, without the "~".
func jobEstSaving(j store.Job) string {
	if j.SourceSize == nil || j.EstMin == nil || j.EstMax == nil {
		return ""
	}
	src := *j.SourceSize
	return dashRange(max(src-*j.EstMax, 0), max(src-*j.EstMin, 0))
}

func jobSaved(j store.Job) (int64, bool) {
	if j.Status != store.JobComplete || j.SourceSize == nil || j.OutputSize == nil {
		return 0, false
	}
	return *j.SourceSize - *j.OutputSize, true
}

// jobResult is the History result column: sizes for a complete job, the
// plain-English summary for anything else.
func jobResult(j store.Job) string {
	if saved, ok := jobSaved(j); ok {
		return units.Bytes(*j.SourceSize) + " → " + units.Bytes(*j.OutputSize) + ", saved " + units.Bytes(saved)
	}
	return j.Summary
}

// jobStatusWord is a job status as the queue shows it.
func jobStatusWord(status string) string {
	switch status {
	case store.JobWaiting:
		return "Waiting"
	case store.JobAnalysing:
		return "Analysing"
	case store.JobEncoding:
		return "Encoding"
	case store.JobValidating:
		return "Validating"
	case store.JobReplacing:
		return "Replacing"
	case store.JobComplete:
		return "Complete"
	case store.JobSkipped:
		return "Skipped"
	case store.JobFailed:
		return "Failed"
	case store.JobCancelled:
		return "Cancelled"
	case store.JobAttention:
		return "Needs attention"
	}
	return status
}

// jobResultWord is a finished job's status, reading "Restored" once the
// original was put back.
func jobResultWord(j store.Job) string {
	if j.Status == store.JobComplete && j.RestoredAt != nil {
		return "Restored"
	}
	return jobStatusWord(j.Status)
}

func jobLamp(j store.Job) string {
	switch j.Status {
	case store.JobComplete:
		if j.RestoredAt != nil {
			return "info"
		}
		return "ok"
	case store.JobSkipped, store.JobCancelled:
		return "warn"
	case store.JobFailed, store.JobAttention:
		return "bad"
	case store.JobWaiting:
		return "idle"
	}
	return "active"
}

func jobTrigger(t string) string {
	switch t {
	case "manual":
		return "You (Optimise now or Retry)"
	case "auto":
		return "Automatic, after a sync"
	}
	return orText(t, "")
}

func orText(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
