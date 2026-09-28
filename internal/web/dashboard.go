package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// dashRefreshEvent is the HTMX event that reloads the dashboard's live
// region when a sync the page was watching finishes.
const dashRefreshEvent = "dash-refresh"

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shell(r, "Dashboard", "dashboard")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	live, err := s.dashLive(r.Context(), sh.DryRun)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.DashPage(views.DashPageData{Shell: sh, Live: live, Sync: s.dashSync(r)}))
}

// dashboardLive renders the metrics and panels, for a refresh after a sync.
func (s *Server) dashboardLive(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	live, err := s.dashLive(r.Context(), st.DryRun)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.DashLiveView(live))
}

// startSync starts a sync in the background. HTMX gets the status fragment;
// a plain form post goes back to the Dashboard.
func (s *Server) startSync(w http.ResponseWriter, r *http.Request) {
	if s.Library != nil {
		s.Library.RunAsync()
	}
	if r.Header.Get("HX-Request") != "true" {
		redirect(w, r, "/")
		return
	}
	s.render(w, r, views.DashSyncView(s.dashSync(r)))
}

// syncStatus is the polled status fragment. When a sync the page watched
// has just finished, it asks the page to reload its live region.
func (s *Server) syncStatus(w http.ResponseWriter, r *http.Request) {
	v := s.dashSync(r)
	if r.URL.Query().Get("was") == "running" && !v.Running {
		w.Header().Set("HX-Trigger", dashRefreshEvent)
	}
	s.render(w, r, views.DashSyncView(v))
}

func (s *Server) dashSync(r *http.Request) views.DashSync {
	top := s.syncState(r)
	v := views.DashSync{Available: s.Library != nil, State: top.State, Text: top.Text}
	if s.Library == nil {
		return v
	}
	st := s.Library.Status()
	v.Running = st.Running
	v.Error = st.LastError
	if st.Running {
		v.Phase = st.Phase
		if v.Phase == "" {
			v.Phase = "Starting"
		}
		v.Percent = -1
		if st.Total > 0 {
			v.Percent = st.Done * 100 / st.Total
			v.Readout = libraryCount(st.Done) + " of " + libraryCount(st.Total)
		}
	}
	return v
}

// dashLive gathers everything the metrics and panels show.
func (s *Server) dashLive(ctx context.Context, dryRun bool) (views.DashLive, error) {
	v := views.DashLive{DryRun: dryRun}
	totals, err := s.Store.Totals(ctx)
	if err != nil {
		return v, err
	}
	v.HasItems = totals.Items > 0
	var sum library.Summary
	if s.Library != nil {
		if sum, err = s.Library.DryRun(ctx); err != nil {
			return v, err
		}
	}
	if err := s.dashMetrics(ctx, &v, totals, sum); err != nil {
		return v, err
	}
	v.Summary = s.dashSummary(ctx, sum)
	if v.Now, err = s.dashNow(ctx, dryRun); err != nil {
		return v, err
	}
	if v.Recent, err = s.dashRecent(ctx); err != nil {
		return v, err
	}
	v.Problems, err = s.dashProblems(ctx, totals)
	return v, err
}

func (s *Server) dashMetrics(ctx context.Context, v *views.DashLive, totals store.OutcomeTotals, sum library.Summary) error {
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return err
	}
	managed := 0
	for _, l := range libs {
		if l.Managed {
			managed++
		}
	}
	if totals.Items > 0 {
		v.Storage = dashMetric(totals.TotalBytes, dashPlural(totals.Items, "item")+" in "+dashPlural(managed, "library"))
	} else {
		v.Storage = views.DashMetric{Note: "Sync not run yet."}
	}

	saved, files, err := s.Store.SavedBytes(ctx)
	if err != nil {
		return err
	}
	v.Saved = dashMetric(saved, "From "+dashPlural(files, "file")+". Restored files are not counted.")
	if files == 0 {
		v.Saved.Note = "No files optimised yet."
	}

	switch {
	case sum.Optimise > 0:
		mid := (sum.SavingMin() + sum.SavingMax()) / 2
		v.Optimisable = dashMetric(mid, "If every current plan ran: "+dashRange(sum.SavingMin(), sum.SavingMax())+".")
	case sum.Evaluated > 0:
		v.Optimisable = views.DashMetric{Value: "0", Unit: "B", Note: "No item needs optimising under the current policies."}
	default:
		v.Optimisable = views.DashMetric{Note: "Nothing evaluated yet."}
	}

	queued, err := s.Store.ActiveJobCount(ctx)
	if err != nil {
		return err
	}
	v.Queued = views.DashMetric{Value: libraryCount(queued), Note: "Waiting or running."}
	if v.DryRun {
		v.Queued.Note = "Dry Run is on, so nothing is queued."
	} else if queued == 0 {
		v.Queued.Note = "Nothing waiting."
	}
	return nil
}

// dashMetric splits a size into the metric's number and unit.
func dashMetric(bytes int64, note string) views.DashMetric {
	value, unit := dashSplit(units.Bytes(bytes))
	return views.DashMetric{Value: value, Unit: unit, Note: note}
}

func dashSplit(size string) (value, unit string) {
	if i := strings.LastIndexByte(size, ' '); i > 0 {
		return size[:i], size[i+1:]
	}
	return size, ""
}

// dashRange formats a size range as "1.2 to 1.5 TB", or one size when both
// ends round to the same text.
func dashRange(lo, hi int64) string {
	a, b := units.Bytes(lo), units.Bytes(hi)
	if a == b {
		return a
	}
	av, au := dashSplit(a)
	bv, bu := dashSplit(b)
	if au == bu {
		return av + " to " + bv + " " + bu
	}
	return a + " to " + b
}

func dashPlural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "y") {
		return libraryCount(n) + " " + strings.TrimSuffix(word, "y") + "ies"
	}
	return libraryCount(n) + " " + word + "s"
}

// dashLineFilter maps a summary line kind to its Library outcome filter.
var dashLineFilter = map[string]string{
	"optimal": "optimal", "convert": "optimise", "downscale": "optimise", "skipped_hdr": "skipped",
	"skipped": "skipped", "protected": "protected", "no_policy": "no_policy", "pending": "pending",
}

// dashLineLamp gives each kind of summary line its lamp.
var dashLineLamp = map[string]string{
	"optimal": "ok", "convert": "info", "downscale": "info", "skipped_hdr": "warn",
	"skipped": "warn", "protected": "idle", "no_policy": "idle", "pending": "idle",
}

func (s *Server) dashSummary(ctx context.Context, sum library.Summary) views.DashSummary {
	v := views.DashSummary{Evaluated: libraryCount(sum.Evaluated)}
	lines := sum.Lines
	if sum.Pending > 0 {
		lines = append(lines, library.Line{Count: sum.Pending, Kind: "pending", Text: "not inspected yet"})
	}
	for _, l := range lines {
		v.Lines = append(v.Lines, views.DashLine{
			Count: libraryCount(l.Count),
			Text:  l.Text,
			Lamp:  dashLineLamp[l.Kind],
			Href:  "/library?outcome=" + dashLineFilter[l.Kind],
		})
	}
	v.Current = units.Bytes(sum.CurrentBytes)
	v.After = dashRange(sum.AfterMin, sum.AfterMax)
	v.Saving = dashRange(sum.SavingMin(), sum.SavingMax())
	if last, err := s.Store.LastSync(ctx); err == nil && last.FinishedAt != nil {
		v.LastRun = "Last sync finished " + units.Ago(*last.FinishedAt, s.Now()) + "."
	}
	return v
}

func (s *Server) dashNow(ctx context.Context, dryRun bool) (views.DashNow, error) {
	v := views.DashNow{DryRun: dryRun}
	jobs, err := s.Store.ActiveJobs(ctx)
	if err != nil {
		return v, err
	}
	if len(jobs) == 0 {
		return v, nil
	}
	j := jobs[0]
	v.Active = true
	v.Title = j.ItemName
	v.Change = dashChange(j.SourceSummary, j.TargetSummary)
	v.Waiting = len(jobs) - 1
	v.Percent = -1
	v.Readout = dashJobStatus(j.Status)
	if j.Status == store.JobEncoding {
		v.Percent = int(j.Progress * 100)
		v.Readout = units.Percent(j.Progress)
	}
	if j.Encoder != "" {
		v.Encoder = j.Encoder
	}
	return v, nil
}

func dashChange(from, to string) string {
	switch {
	case from != "" && to != "":
		return from + " → " + to
	case to != "":
		return to
	}
	return from
}

func dashJobStatus(status string) string {
	switch status {
	case store.JobWaiting:
		return "Waiting"
	case store.JobAnalysing:
		return "Analysing"
	case store.JobEncoding:
		return "Encoding"
	case store.JobValidating:
		return "Checking the new file"
	case store.JobReplacing:
		return "Replacing the original"
	case store.JobComplete:
		return "Complete"
	case store.JobSkipped:
		return "Skipped"
	case store.JobFailed:
		return "Failed"
	case store.JobCancelled:
		return "Cancelled"
	}
	return status
}

func dashJobLamp(status string) string {
	switch status {
	case store.JobComplete:
		return "ok"
	case store.JobFailed:
		return "bad"
	case store.JobSkipped:
		return "warn"
	}
	return "idle"
}

// dashRecent lists the last ten finished jobs, newest first.
func (s *Server) dashRecent(ctx context.Context) ([]views.DashActivity, error) {
	jobs, err := s.Store.HistoryJobs(ctx, "", 10, 0)
	if err != nil {
		return nil, err
	}
	out := make([]views.DashActivity, 0, len(jobs))
	for _, j := range jobs {
		a := views.DashActivity{
			Title:  j.ItemName,
			Href:   "/library/" + j.ItemID,
			Change: dashChange(j.SourceSummary, j.TargetSummary),
			Lamp:   dashJobLamp(j.Status),
			Status: dashJobStatus(j.Status),
		}
		if j.Summary != "" {
			a.Status += ": " + strings.TrimSuffix(j.Summary, ".")
		}
		if j.SourceSize != nil && j.OutputSize != nil {
			a.Sizes = units.Bytes(*j.SourceSize) + " → " + units.Bytes(*j.OutputSize)
		}
		if j.FinishedAt != nil {
			a.When = units.Ago(*j.FinishedAt, s.Now())
		}
		out = append(out, a)
	}
	return out, nil
}

// dashProblems lists what needs attention, most serious first.
func (s *Server) dashProblems(ctx context.Context, totals store.OutcomeTotals) ([]views.DashProblem, error) {
	var out []views.DashProblem
	if p, ok := s.dashSyncProblem(ctx); ok {
		out = append(out, p)
	}
	if n := totals.ByOutcome["skipped"]; n > 0 {
		rows, _, err := s.Store.LibraryList(ctx, store.LibraryFilter{Outcome: "skipped", Limit: 5})
		if err != nil {
			return nil, err
		}
		p := views.DashProblem{
			Lamp:   "warn",
			Text:   dashPlural(n, "item") + " skipped for safety. JellyTrim will not change them until the reason is fixed.",
			Href:   "/library?outcome=skipped",
			Action: "Show skipped items",
		}
		for _, r := range rows {
			p.Items = append(p.Items, views.DashProblemItem{
				Title:  libraryTitle(r.Item),
				Reason: strings.TrimPrefix(r.Summary, "Skipped: "),
				Href:   "/library/" + r.Item.ID,
			})
		}
		out = append(out, p)
	}
	running := s.Library != nil && s.Library.Status().Running
	if n := totals.ByOutcome["pending"]; n > 0 && !running {
		out = append(out, views.DashProblem{
			Lamp:   "info",
			Text:   dashPlural(n, "item") + " not inspected yet. The next sync inspects them.",
			Href:   "/library?outcome=pending",
			Action: "Show them",
		})
	}
	return out, nil
}

func (s *Server) dashSyncProblem(ctx context.Context) (views.DashProblem, bool) {
	p := views.DashProblem{
		Lamp:   "bad",
		Text:   "The last sync failed. Check that Jellyfin is running, and the address and API key in Settings.",
		Href:   "/settings#jellyfin",
		Action: "Settings",
	}
	if s.Library != nil {
		if st := s.Library.Status(); st.LastError != "" {
			p.Detail = st.LastError
			return p, true
		}
	}
	last, err := s.Store.LastSync(ctx)
	if errors.Is(err, store.ErrNotFound) || err != nil || last.Status != "failed" {
		return p, false
	}
	if last.FinishedAt != nil {
		p.Text = "The last sync failed " + units.Ago(*last.FinishedAt, s.Now()) +
			". Check that Jellyfin is running, and the address and API key in Settings."
	}
	p.Detail = last.Error
	return p, true
}

// libraryCount formats a count with thousands separators: "1,432".
func libraryCount(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
