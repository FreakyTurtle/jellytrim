package library

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// Line is one counted line in a summary, such as "87 would be converted
// H.264 → HEVC".
type Line struct {
	Count int
	Text  string
	Kind  string // optimal, convert, downscale, skipped_hdr, skipped, protected, no_policy, pending
	Bytes int64  // current size of these items
}

// Summary is the Dry Run summary: what JellyTrim would do to the whole
// library right now. Sizes after optimisation are estimates.
type Summary struct {
	Evaluated    int
	Pending      int
	Lines        []Line
	CurrentBytes int64
	AfterMin     int64
	AfterMax     int64
	Optimise     int
}

// SavingMin and SavingMax are the estimated saving range.
func (s Summary) SavingMin() int64 { return max(s.CurrentBytes-s.AfterMax, 0) }

// SavingMax is the upper end of the estimated saving.
func (s Summary) SavingMax() int64 { return max(s.CurrentBytes-s.AfterMin, 0) }

// DryRun builds the summary from the stored evaluations.
func (s *Service) DryRun(ctx context.Context) (Summary, error) {
	var sum Summary
	evs, err := s.store.Evaluations(ctx)
	if err != nil {
		return sum, err
	}
	totals, err := s.store.Totals(ctx)
	if err != nil {
		return sum, err
	}
	sum.Pending = totals.ByOutcome["pending"]
	groups := map[string]*Line{}
	add := func(kind, text string, size int64) {
		key := kind + "|" + text
		if groups[key] == nil {
			groups[key] = &Line{Kind: kind, Text: text}
		}
		groups[key].Count++
		groups[key].Bytes += size
	}
	for _, e := range evs {
		sum.Evaluated++
		sum.CurrentBytes += e.SourceSize
		after := e.SourceSize
		switch plan.Outcome(e.Outcome) {
		case plan.Optimise:
			var p plan.Plan
			_ = json.Unmarshal([]byte(e.Plan), &p)
			kind, text := describePlan(p)
			add(kind, text, e.SourceSize)
			sum.Optimise++
			sum.AfterMin += deref(e.EstMin, after)
			sum.AfterMax += deref(e.EstMax, after)
			continue
		case plan.AlreadyOptimal:
			add("optimal", "already optimal", e.SourceSize)
		case plan.Protected:
			add("protected", "protected by a policy", e.SourceSize)
		case plan.NoPolicy:
			add("no_policy", "not matched by any enabled policy", e.SourceSize)
		case plan.Skipped:
			kind, text := describeSkip(e.Reasons)
			add(kind, text, e.SourceSize)
		}
		sum.AfterMin += after
		sum.AfterMax += after
	}
	sum.Lines = sortLines(groups)
	return sum, nil
}

func deref(p *int64, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	return *p
}

func describePlan(p plan.Plan) (kind, text string) {
	src := strings.Fields(p.SourceLabel) // "2160p H.264"
	dst := strings.Fields(p.TargetLabel)
	if len(src) == 2 && len(dst) == 2 && p.Scale {
		from := src[0]
		if from == "2160p" {
			from = "4K"
		}
		return "downscale", "would be downscaled " + from + " → " + dst[0]
	}
	if len(src) == 2 && len(dst) == 2 {
		return "convert", "would be converted " + src[1] + " → " + dst[1]
	}
	return "convert", "would be converted"
}

func describeSkip(reasonsJSON string) (kind, text string) {
	var rs []plan.Reason
	_ = json.Unmarshal([]byte(reasonsJSON), &rs)
	for _, r := range rs {
		if strings.HasPrefix(r.Code, "hdr_") {
			return "skipped_hdr", "skipped (HDR or Dolby Vision)"
		}
	}
	if len(rs) > 0 {
		switch rs[0].Code {
		case "not_probed", "probe_failed":
			return "skipped", "skipped (could not be inspected)"
		case "no_encoder":
			return "skipped", "skipped (no working encoder)"
		case "sync":
			return "skipped", "skipped (not a single local video file)"
		}
	}
	return "skipped", "skipped for safety"
}

var kindOrder = map[string]int{"optimal": 0, "convert": 1, "downscale": 2, "skipped_hdr": 3, "skipped": 4, "protected": 5, "no_policy": 6}

func sortLines(groups map[string]*Line) []Line {
	out := make([]Line, 0, len(groups))
	for _, l := range groups {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if kindOrder[out[i].Kind] != kindOrder[out[j].Kind] {
			return kindOrder[out[i].Kind] < kindOrder[out[j].Kind]
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Text < out[j].Text
	})
	return out
}

// previewSampleSize is how many of the items a previewed policy would decide
// are planned in full. Counting matches reads only the probe summaries; a
// plan needs the parsed probe, so beyond this many items the outcome split
// and the sizes are estimated from an evenly spaced sample.
const previewSampleSize = 1000

// Preview is what a policy would do if it were enabled at its position.
// Matches and Wins are always exact counts.
type Preview struct {
	Matches  int // items whose scope and conditions pass
	Wins     int // of those, items this policy would decide (no higher policy wins first)
	Optimise int
	Optimal  int
	Skipped  int
	Current  int64 // current size of items it would optimise
	AfterMin int64
	AfterMax int64
	Samples  []PreviewSample
	// Sampled is true when Optimise, Optimal, Skipped and the sizes are
	// estimated from SampleSize of the Wins items, scaled up to Wins,
	// rather than counted. It is false when Wins is at most
	// previewSampleSize.
	Sampled    bool
	SampleSize int
}

// PreviewSample is one example item in a preview.
type PreviewSample struct {
	ItemID  string
	Name    string
	Outcome plan.Outcome
	Summary string
}

// Preview evaluates a policy as if enabled, among the other enabled
// policies, without saving anything.
func (s *Service) Preview(ctx context.Context, p policy.Policy) (Preview, error) {
	return s.preview(ctx, p, previewSampleSize)
}

// preview is Preview planning at most sampleSize items.
func (s *Service) preview(ctx context.Context, p policy.Policy, sampleSize int) (Preview, error) {
	var pv Preview
	b, err := s.loadBulk(ctx)
	if err != nil {
		return pv, err
	}
	p.Enabled = true
	if p.ID == 0 {
		p.ID = -1 // a new policy; any ID that cannot collide
	}
	ps := []policy.Policy{p}
	for _, o := range b.policies {
		if o.ID != p.ID {
			ps = append(ps, o)
		}
	}
	policy.Sort(ps)
	now := s.now()
	wins, err := s.previewWins(ctx, b, p, ps, now, &pv)
	if err != nil {
		return pv, err
	}
	pv.Wins = len(wins)
	sample := evenSample(wins, sampleSize)
	decided, err := s.previewDecide(ctx, b, sample, ps, now)
	if err != nil {
		return pv, err
	}
	for _, r := range decided {
		if r.ok {
			pv.count(r.c.Item, r.d)
			pv.SampleSize++
		}
	}
	if len(sample) < len(wins) {
		pv.scale(len(wins))
	}
	return pv, nil
}

// previewWins counts the items the policy matches and returns the IDs of
// those it would decide, in ID order. It reads the library a page at a time
// and only the probe summaries.
func (s *Service) previewWins(ctx context.Context, b *bulk, p policy.Policy, ps []policy.Policy, now time.Time,
	pv *Preview) ([]string, error) {
	var wins []string
	var matches atomic.Int64
	err := s.eachPage(ctx, func(pg page) error {
		won := make([]bool, len(pg.items))
		err := parallel(ctx, maxEvalWorkers, len(pg.items), func(i int) error {
			c, err := s.bulkCandidate(ctx, b, &pg, pg.items[i])
			if err != nil {
				return err
			}
			// Excluded, unmanageable and uninspected items are decided (or
			// left pending) before any policy, exactly as evaluation does.
			if _, g := gate(c, b.excluded); g != gateOpen {
				return nil
			}
			if !policy.Check(p, c.Policy, now).Matched {
				return nil
			}
			matches.Add(1)
			res := policy.Evaluate(ps, c.Policy, now)
			won[i] = res.Winner != nil && res.Winner.ID == p.ID
			return nil
		})
		for i, w := range won {
			if w {
				wins = append(wins, pg.items[i].ID)
			}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	pv.Matches = int(matches.Load())
	return wins, nil
}

// evenSample returns at most k of idx, evenly spaced and always the same for
// the same input, so a preview does not change between requests.
func evenSample[T any](idx []T, k int) []T {
	if len(idx) <= k {
		return idx
	}
	out := make([]T, k)
	for i := range out {
		out[i] = idx[i*len(idx)/k]
	}
	return out
}

// previewDecide plans the sampled items, parsing their probes where an
// optimise policy wins. They are decided in name order, so the examples a
// preview shows read alphabetically.
func (s *Service) previewDecide(ctx context.Context, b *bulk, sample []string, ps []policy.Policy,
	now time.Time) ([]decided, error) {
	found, err := s.store.ItemsByIDs(ctx, sample)
	if err != nil {
		return nil, err
	}
	items := make([]store.Item, 0, len(found))
	for _, id := range sample {
		if it, ok := found[id]; ok { // removed since: left out
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SortName != items[j].SortName {
			return items[i].SortName < items[j].SortName
		}
		return items[i].ID < items[j].ID
	})
	out := make([]decided, 0, len(items))
	for start := 0; start < len(items); start += s.pageSize {
		pg, err := s.loadPage(ctx, items[start:min(start+s.pageSize, len(items))])
		if err != nil {
			return nil, err
		}
		batch, err := s.decideItems(ctx, b, pg, ps, now)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (pv *Preview) count(it store.Item, d plan.Decision) {
	switch d.Outcome {
	case plan.Optimise:
		pv.Optimise++
		pv.Current += d.Plan.SourceSize
		pv.AfterMin += d.Plan.EstMin
		pv.AfterMax += d.Plan.EstMax
	case plan.AlreadyOptimal, plan.Protected:
		pv.Optimal++
	default:
		pv.Skipped++
	}
	if len(pv.Samples) < 12 {
		pv.Samples = append(pv.Samples, PreviewSample{ItemID: it.ID, Name: displayName(it.Name, it.SeriesName), Outcome: d.Outcome, Summary: d.Summary})
	}
}

// scale turns the counts from the sample into estimates for all wins items.
func (pv *Preview) scale(wins int) {
	pv.Sampled = true
	if pv.SampleSize == 0 {
		return
	}
	f := float64(wins) / float64(pv.SampleSize)
	up := func(n int64) int64 { return int64(math.Round(float64(n) * f)) }
	pv.Optimise = min(int(up(int64(pv.Optimise))), wins)
	pv.Optimal = min(int(up(int64(pv.Optimal))), wins-pv.Optimise)
	pv.Skipped = wins - pv.Optimise - pv.Optimal
	pv.Current, pv.AfterMin, pv.AfterMax = up(pv.Current), up(pv.AfterMin), up(pv.AfterMax)
}

func displayName(name, series string) string {
	if series == "" {
		return name
	}
	return series + " · " + name
}
