package library

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
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

// Preview is what a policy would do if it were enabled at its position.
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
	var pv Preview
	snap, err := s.loadSnapshot(ctx)
	if err != nil {
		return pv, err
	}
	excluded, err := s.store.Exclusions(ctx)
	if err != nil {
		return pv, err
	}
	p.Enabled = true
	if p.ID == 0 {
		p.ID = -1 // a new policy; any ID that cannot collide
	}
	ps := []policy.Policy{p}
	for _, o := range snap.policies {
		if o.ID != p.ID {
			ps = append(ps, o)
		}
	}
	policy.Sort(ps)
	now := s.now()
	for i := range snap.candidates {
		c := &snap.candidates[i]
		// Excluded, unmanageable and uninspected items are decided (or
		// left pending) before any policy, exactly as evaluation does.
		if _, g := gate(*c, excluded); g != gateOpen {
			continue
		}
		if !policy.Check(p, c.Policy, now).Matched {
			continue
		}
		pv.Matches++
		res := policy.Evaluate(ps, c.Policy, now)
		if res.Winner == nil || res.Winner.ID != p.ID {
			continue
		}
		pv.Wins++
		pv.count(*c, snap.decide(c, res))
	}
	return pv, nil
}

func (pv *Preview) count(c Candidate, d plan.Decision) {
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
		pv.Samples = append(pv.Samples, PreviewSample{ItemID: c.Item.ID, Name: displayName(c.Item.Name, c.Item.SeriesName), Outcome: d.Outcome, Summary: d.Summary})
	}
}

func displayName(name, series string) string {
	if series == "" {
		return name
	}
	return series + " · " + name
}
