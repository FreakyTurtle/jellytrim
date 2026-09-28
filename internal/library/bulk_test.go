package library

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/testutil"
)

// TestSummaryGivesTheParsedFacts checks, for every committed probe fixture,
// that the summary columns saved with a probe read back as exactly the
// facts the parsed probe gives, so evaluating from summaries explains items
// the same way parsing every probe did.
func TestSummaryGivesTheParsedFacts(t *testing.T) {
	dir := filepath.Join(testutil.RepoRoot(t), "internal", "media", "testdata", "probe")
	entries, err := os.ReadDir(dir)
	must(t, err)
	checked := 0
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || strings.HasSuffix(name, ".frames") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			probe, frames := testutil.ProbeJSON(t, name)
			const size = 1_234_567_890
			f, err := media.Parse(probe, frames, size)
			if err != nil {
				t.Skipf("fixture does not parse: %v", err)
			}
			p := store.Probe{FileIdentity: store.FileIdentity{Size: size}}
			fillSummary(&p, f)
			sum := store.ProbeSummary{FileIdentity: p.FileIdentity, HasJSON: true, HDR: p.HDR}
			if !summaryComplete(sum) {
				t.Fatalf("summary %+v counts as incomplete", p)
			}
			if got, want := factsFromSummary(&p), policy.FactsFromFile(f); got != want {
				t.Fatalf("from summary %+v, parsed %+v", got, want)
			}
		})
		checked++
	}
	if checked < 10 {
		t.Fatalf("only %d fixtures checked", checked)
	}
}

// fileConditionPolicies adds policies whose conditions read every file fact,
// ahead of the starter policies, so the comparison below covers them.
func fileConditionPolicies(t *testing.T, e env) {
	t.Helper()
	ctx := context.Background()
	for i, p := range []policy.Policy{
		{Name: "Big HDR", Conditions: policy.Conditions{All: []policy.Condition{
			{Field: policy.FieldHDR, Op: policy.OpIsNot, List: []string{"sdr"}},
			{Field: policy.FieldSize, Op: policy.OpAbove, Number: 0.001}}},
			Action: policy.Action{Kind: policy.KindOptimise, MaxResolution: "1080p", Codec: "hevc", AllowHDRReduction: true}},
		{Name: "High bitrate", Conditions: policy.Conditions{All: []policy.Condition{
			{Field: policy.FieldBitrate, Op: policy.OpAbove, Number: 4},
			{Field: policy.FieldResolution, Op: policy.OpAtMost, Text: "1080p"}}},
			Action: policy.Action{Kind: policy.KindOptimise, Codec: "hevc"}},
		{Name: "Keep AV1", Conditions: policy.Conditions{All: []policy.Condition{
			{Field: policy.FieldCodec, Op: policy.OpIs, List: []string{"av1"}}}},
			Action: policy.Action{Kind: policy.KindProtect}},
	} {
		p.Enabled, p.Priority = true, i+1
		_, err := e.svc.SavePolicy(ctx, p)
		must(t, err)
	}
}

// referenceEvaluations decides every managed item the way evaluation did
// before summaries: every probe loaded and parsed in full.
func referenceEvaluations(t *testing.T, e env) map[string]store.Evaluation {
	t.Helper()
	ctx := context.Background()
	ec, err := e.svc.loadEvalContext(ctx)
	must(t, err)
	items, err := e.st.ManagedItems(ctx)
	must(t, err)
	excluded, err := e.st.Exclusions(ctx)
	must(t, err)
	out := map[string]store.Evaluation{}
	for _, it := range items {
		ud, err := e.st.ItemUserData(ctx, it.ID)
		must(t, err)
		var p *store.Probe
		if full, err := e.st.Probe(ctx, it.ID); err == nil {
			p = &full
		}
		c := ec.candidate(it, p, ud, func(id store.FileIdentity) (bool, error) { return e.st.IsOptimised(ctx, id) })
		d, g := gate(c, excluded)
		switch g {
		case gatePending:
			continue
		case gateDecided:
			out[it.ID] = toEvaluation(it.ID, policy.Result{}, d)
			continue
		}
		res := policy.Evaluate(ec.policies, c.Policy, e.svc.now())
		out[it.ID] = toEvaluation(it.ID, res, ec.decide(&c, res))
	}
	return out
}

func storedEvaluations(t *testing.T, e env) map[string]store.Evaluation {
	t.Helper()
	ctx := context.Background()
	items, err := e.st.ManagedItems(ctx)
	must(t, err)
	out := map[string]store.Evaluation{}
	for _, it := range items {
		ev, err := e.st.Evaluation(ctx, it.ID)
		if err == nil {
			ev.EvaluatedAt = time.Time{}
			out[it.ID] = ev
		}
	}
	return out
}

func sameEvaluations(t *testing.T, got, want map[string]store.Evaluation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d evaluations, want %d", len(got), len(want))
	}
	for id, w := range want {
		if g := got[id]; !reflect.DeepEqual(g, w) {
			t.Errorf("item %s:\n got %+v\nwant %+v", id, g, w)
		}
	}
}

func TestEvaluationFromSummariesMatchesParsingEveryProbe(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	fileConditionPolicies(t, e)
	_, err := e.svc.Run(ctx)
	must(t, err)
	sameEvaluations(t, storedEvaluations(t, e), referenceEvaluations(t, e))
}

// A probe saved before the summary columns existed has them empty; it is
// parsed in full instead, with the same result.
func TestProbeWithoutSummaryIsParsed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	fileConditionPolicies(t, e)
	_, err := e.svc.Run(ctx)
	must(t, err)
	before := storedEvaluations(t, e)
	_, err = e.st.DB().ExecContext(ctx, `UPDATE probes SET video_codec = '', width = 0, height = 0, resolution = 0,
		hdr = '', video_bitrate = 0, duration_ms = 0, container = ''`)
	must(t, err)
	_, err = e.svc.Evaluate(ctx)
	must(t, err)
	sameEvaluations(t, storedEvaluations(t, e), before)
}

func TestEvenSample(t *testing.T) {
	idx := []int{3, 5, 8, 13, 21, 34, 55, 89, 144, 233}
	cases := []struct {
		name string
		k    int
		want []int
	}{
		{"fewer than k keeps all", 20, idx},
		{"exactly k keeps all", 10, idx},
		{"half takes every other", 5, []int{3, 8, 21, 55, 144}},
		{"three spread across", 3, []int{3, 13, 55}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evenSample(idx, tc.k); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPreviewScale(t *testing.T) {
	pv := Preview{Wins: 10, Optimise: 2, Optimal: 1, Skipped: 1, SampleSize: 4, Current: 400, AfterMin: 100, AfterMax: 200}
	pv.scale(10)
	want := Preview{Wins: 10, Optimise: 5, Optimal: 3, Skipped: 2, SampleSize: 4, Sampled: true,
		Current: 1000, AfterMin: 250, AfterMax: 500}
	if !reflect.DeepEqual(pv, want) {
		t.Fatalf("got %+v, want %+v", pv, want)
	}
	// Rounding never counts more items than the policy decides.
	pv = Preview{Optimise: 2, Optimal: 1, SampleSize: 3}
	pv.scale(4)
	if pv.Optimise+pv.Optimal+pv.Skipped != 4 || pv.Skipped < 0 {
		t.Fatalf("got %+v", pv)
	}
}

func TestPreviewSamplesLargeResults(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.Run(ctx) // only Protect favourites is enabled
	must(t, err)
	p := policy.Policy{Name: "Everything to HEVC 720p", Action: policy.Action{Kind: policy.KindOptimise,
		MaxResolution: "720p", Codec: "hevc"}, Priority: 100}
	exact, err := e.svc.preview(ctx, p, previewSampleSize)
	must(t, err)
	if exact.Sampled || exact.SampleSize != exact.Wins || exact.Wins < 8 {
		t.Fatalf("exact preview %+v", exact)
	}
	same, err := e.svc.preview(ctx, p, exact.Wins)
	must(t, err)
	if !reflect.DeepEqual(same, exact) {
		t.Fatalf("a sample as large as the wins is not exact:\n%+v\n%+v", same, exact)
	}
	est, err := e.svc.preview(ctx, p, 4)
	must(t, err)
	if !est.Sampled || est.SampleSize != 4 || est.Matches != exact.Matches || est.Wins != exact.Wins {
		t.Fatalf("sampled preview %+v, exact %+v", est, exact)
	}
	if est.Optimise+est.Optimal+est.Skipped != est.Wins || len(est.Samples) != 4 {
		t.Fatalf("sampled counts %+v", est)
	}
}

// Evaluating the library a page at a time decides every item as parsing
// every probe does, however small the pages.
func TestEvaluationInPagesMatchesParsingEveryProbe(t *testing.T) {
	for _, size := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("pages of %d", size), func(t *testing.T) {
			e := setup(t)
			e.svc.pageSize = size
			enableAll(t, e)
			fileConditionPolicies(t, e)
			_, err := e.svc.Run(context.Background())
			must(t, err)
			sameEvaluations(t, storedEvaluations(t, e), referenceEvaluations(t, e))
		})
	}
}

func TestPreviewInPagesMatchesOnePage(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	_, err := e.svc.Run(ctx)
	must(t, err)
	p := policy.Policy{Name: "Everything to HEVC 720p", Action: policy.Action{Kind: policy.KindOptimise,
		MaxResolution: "720p", Codec: "hevc"}, Priority: 100}
	whole, err := e.svc.preview(ctx, p, 5)
	must(t, err)
	e.svc.pageSize = 2
	paged, err := e.svc.preview(ctx, p, 5)
	must(t, err)
	if !reflect.DeepEqual(paged, whole) || whole.Wins == 0 {
		t.Fatalf("in pages %+v\nin one page %+v", paged, whole)
	}
}

// breakProbe makes an item's stored probe unreadable: no summary, so it is
// read in full, and compressed with a dictionary that does not exist.
func breakProbe(t *testing.T, e env, id string) {
	t.Helper()
	_, err := e.st.DB().ExecContext(context.Background(), `UPDATE probes SET hdr = '', video_codec = '',
		probe_json = X'78BB00000000FFFF' WHERE item_id = ?`, id)
	must(t, err)
}

// An evaluation run that fails part-way saves the pages it finished and
// leaves every other item's previous evaluation in place; the next complete
// run removes what is stale.
func TestEvaluationThatFailsKeepsEarlierEvaluations(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	_, err := e.svc.Run(ctx)
	must(t, err)
	before := storedEvaluations(t, e)
	e.svc.pageSize = 2
	// Every decision a policy makes changes: everything is protected.
	_, err = e.svc.SavePolicy(ctx, policy.Policy{Name: "Protect all", Enabled: true, Priority: 0,
		Action: policy.Action{Kind: policy.KindProtect}})
	must(t, err)
	after := referenceEvaluations(t, e)

	// Break the probe of the last probed item in ID order, so the run fails
	// on its page after saving the earlier ones.
	ids := slices.Sorted(maps.Keys(before))
	broken := ids[len(ids)-1]
	var pos int
	all, err := e.st.ManagedItems(ctx)
	must(t, err)
	order := make([]string, len(all))
	for i, it := range all {
		order[i] = it.ID
	}
	slices.Sort(order)
	pos = slices.Index(order, broken)
	firstUnsaved := order[pos-pos%e.svc.pageSize]
	breakProbe(t, e, broken)

	if _, err := e.svc.Evaluate(ctx); err == nil {
		t.Fatal("evaluation with an unreadable probe succeeded")
	}
	got := storedEvaluations(t, e)
	var saved, kept int
	for _, id := range ids {
		g, ok := got[id]
		if !ok {
			t.Fatalf("item %s lost its evaluation", id)
		}
		want, which := after[id], &saved
		if id >= firstUnsaved {
			want, which = before[id], &kept
		}
		if !reflect.DeepEqual(g, want) {
			t.Errorf("item %s:\n got %+v\nwant %+v", id, g, want)
		}
		if !reflect.DeepEqual(before[id], after[id]) {
			*which++ // the check above tells the runs apart
		}
	}
	if saved == 0 || kept == 0 {
		t.Fatalf("%d changed items saved and %d kept: the test does not tell the runs apart", saved, kept)
	}

	// Forgetting the broken probe leaves the item pending; the next run
	// completes and removes its old evaluation.
	must(t, e.svc.ReprobeItem(ctx, broken))
	_, err = e.svc.Evaluate(ctx)
	must(t, err)
	final := storedEvaluations(t, e)
	if _, ok := final[broken]; ok {
		t.Fatalf("pending item %s kept its evaluation", broken)
	}
	sameEvaluations(t, final, referenceEvaluations(t, e))
}
