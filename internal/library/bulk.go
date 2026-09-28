package library

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// evalBatch is how many items are decided together. Only an item an
// optimise policy wins has its ffprobe output loaded and parsed, and only
// while its batch is being decided, so memory does not grow with the size
// of the library.
const evalBatch = 500

// maxEvalWorkers bounds how many items are decided at once. Deciding is CPU
// work (parsing ffprobe output, building explanations) that lasts seconds
// even for a very large library.
const maxEvalWorkers = 8

// bulk is what evaluating the whole library reads once: the evaluation
// context, the files JellyTrim produced and the exclusions. The items
// themselves, with their watch state and probe summaries, are read a page
// at a time (see page), so memory does not grow with the library.
type bulk struct {
	evalContext
	optimised map[store.FileIdentity]bool
	excluded  map[string]string
}

func (s *Service) loadBulk(ctx context.Context) (*bulk, error) {
	b := &bulk{}
	var err error
	if b.evalContext, err = s.loadEvalContext(ctx); err != nil {
		return nil, err
	}
	b.links = newLinkCache()
	if b.optimised, err = s.store.OptimisedIdentities(ctx); err != nil {
		return nil, err
	}
	if b.excluded, err = s.store.Exclusions(ctx); err != nil {
		return nil, fmt.Errorf("reading exclusions: %w", err)
	}
	return b, nil
}

// page is up to pageSize managed items with their watch state and probe
// summaries, but no ffprobe output.
type page struct {
	items    []store.Item
	userData map[string][]store.UserData
	probes   map[string]store.ProbeSummary
}

// loadPage reads the watch state and probe summaries of items.
func (s *Service) loadPage(ctx context.Context, items []store.Item) (page, error) {
	pg := page{items: items}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	var err error
	if pg.userData, err = s.store.UserDataFor(ctx, ids); err != nil {
		return pg, err
	}
	if pg.probes, err = s.store.ProbeSummariesFor(ctx, ids); err != nil {
		return pg, err
	}
	return pg, nil
}

// eachPage walks the managed items a page at a time, in ID order, and calls
// fn with each. The next page is read while fn handles the current one. It
// stops at the first error.
func (s *Service) eachPage(ctx context.Context, fn func(page) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pages := make(chan page)
	var readErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(pages)
		readErr = s.readPages(ctx, pages)
	}()
	var err error
	for pg := range pages {
		if err = fn(pg); err != nil {
			cancel() // stops the reader
			break
		}
	}
	for range pages { // lets the reader finish after an error
	}
	wg.Wait()
	if err != nil {
		return err
	}
	return readErr
}

// readPages sends every page of managed items to out until the end or ctx
// ends.
func (s *Service) readPages(ctx context.Context, out chan<- page) error {
	after := ""
	for {
		items, err := s.store.ManagedItemsAfter(ctx, after, s.pageSize)
		if err != nil || len(items) == 0 {
			return err
		}
		after = items[len(items)-1].ID
		pg, err := s.loadPage(ctx, items)
		if err != nil {
			return err
		}
		select {
		case out <- pg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// summaryComplete reports whether a probe's summary columns can stand in
// for its ffprobe output. A successful probe always has an HDR class (Unclear
// when there is no video) and a size, so an empty one is a probe saved
// before the summary existed.
func summaryComplete(p store.ProbeSummary) bool {
	return p.Error != "" || (p.HasJSON && p.HDR != "" && p.Size > 0)
}

// summaryProbe is the probe a summary describes, without its ffprobe output.
func summaryProbe(p store.ProbeSummary) store.Probe {
	return store.Probe{ItemID: p.ItemID, LocalPath: p.LocalPath, FileIdentity: p.FileIdentity, Nlink: p.Nlink,
		IsSymlink: p.IsSymlink, Error: p.Error, ProbedAt: p.ProbedAt, VideoCodec: p.VideoCodec, Width: p.Width,
		Height: p.Height, Resolution: p.Resolution, HDR: p.HDR, VideoBitrate: p.VideoBitrate,
		DurationMs: p.DurationMs, Container: p.Container}
}

// factsFromSummary is the inverse of fillSummary: the policy facts read
// back from the summary columns. It gives what policy.FactsFromFile gives
// for the parsed probe.
func factsFromSummary(p *store.Probe) policy.Facts {
	return policy.Facts{Probed: true, Codec: media.Codec(p.VideoCodec), Width: p.Width, Height: p.Height,
		VideoBitrate: p.VideoBitrate, BitrateKnown: p.VideoBitrate > 0, HDR: media.HDRClass(p.HDR), Size: p.Size}
}

func (b *bulk) isOptimised(id store.FileIdentity) (bool, error) { return b.optimised[id], nil }

// bulkCandidate builds an item's candidate from its probe summary. A probe
// without a summary is loaded and parsed in full.
func (s *Service) bulkCandidate(ctx context.Context, b *bulk, pg *page, it store.Item) (Candidate, error) {
	c := b.baseCandidate(it, pg.userData[it.ID])
	p, ok := pg.probes[it.ID]
	if !ok {
		return c, nil
	}
	if !summaryComplete(p) {
		return s.withFullProbe(ctx, b, c)
	}
	sp := summaryProbe(p)
	c.attachSummary(&sp, b.optimised)
	return c, nil
}

// withFullProbe loads the item's probe with its ffprobe output and parses
// it. It is for a probe saved without a summary.
func (s *Service) withFullProbe(ctx context.Context, b *bulk, c Candidate) (Candidate, error) {
	p, err := s.store.Probe(ctx, c.Item.ID)
	if errors.Is(err, store.ErrNotFound) {
		c.detachProbe() // forgotten since: pending
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("reading the probe for %s: %w", c.Item.ID, err)
	}
	c.attachProbe(&p, b.isOptimised)
	return c, nil
}

// loadProbes reads the probes with their ffprobe output for the given
// items, in order; nil where an item has no probe any more.
func (s *Service) loadProbes(ctx context.Context, ids []string) ([]*store.Probe, error) {
	out := make([]*store.Probe, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	found, err := s.store.ProbesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		if p, ok := found[id]; ok {
			out[i] = &p
		}
	}
	return out, nil
}

// decided is one item's outcome.
type decided struct {
	c         Candidate
	res       policy.Result
	d         plan.Decision
	ok        bool // false while the item is pending: no decision
	needsFile bool // an optimise policy won: the plan needs the parsed probe
}

// batch is a batch of items between reading and deciding.
type batch struct {
	out    []decided
	need   []int          // indices into out of the items that need their probe parsed
	probes []*store.Probe // their full probes, in the same order
}

// decideItems decides items in three steps: the policies, from the probe
// summaries; then the full probes of the items an optimise policy won, read
// together; then their plans. Only those items' ffprobe output is parsed,
// and only for the length of the call.
func (s *Service) decideItems(ctx context.Context, b *bulk, pg page, ps []policy.Policy,
	now time.Time) ([]decided, error) {
	bt, err := s.readBatch(ctx, b, pg, ps, now)
	if err != nil {
		return nil, err
	}
	return b.finishBatch(ctx, bt, ps, now)
}

// readBatch takes the first two steps of decideItems.
func (s *Service) readBatch(ctx context.Context, b *bulk, pg page, ps []policy.Policy,
	now time.Time) (batch, error) {
	bt := batch{out: make([]decided, len(pg.items))}
	err := parallel(ctx, maxEvalWorkers, len(pg.items), func(i int) error {
		c, err := s.bulkCandidate(ctx, b, &pg, pg.items[i])
		if err != nil {
			return err
		}
		bt.out[i] = b.firstPass(c, ps, now)
		return nil
	})
	if err != nil {
		return bt, err
	}
	var ids []string
	for i := range bt.out {
		if bt.out[i].needsFile {
			bt.need = append(bt.need, i)
			ids = append(ids, bt.out[i].c.Item.ID)
		}
	}
	bt.probes, err = s.loadProbes(ctx, ids)
	return bt, err
}

// finishBatch takes the last step of decideItems: the plans.
func (b *bulk) finishBatch(ctx context.Context, bt batch, ps []policy.Policy, now time.Time) ([]decided, error) {
	err := parallel(ctx, maxEvalWorkers, len(bt.need), func(k int) error {
		b.withFile(&bt.out[bt.need[k]], bt.probes[k], ps, now)
		return nil
	})
	return bt.out, err
}

// firstPass gates the candidate and evaluates the policies. It decides at
// once unless an optimise policy won, whose plan needs the parsed probe.
func (b *bulk) firstPass(c Candidate, ps []policy.Policy, now time.Time) decided {
	r := decided{c: c}
	d, g := gate(c, b.excluded)
	switch g {
	case gatePending:
		return r
	case gateDecided:
		r.d, r.ok = d, true
		return r
	}
	r.res = policy.Evaluate(ps, c.Policy, now)
	if c.File == nil && r.res.Winner != nil && r.res.Winner.Action.Kind == policy.KindOptimise {
		r.needsFile = true
		return r
	}
	r.d, r.ok = b.decide(&r.c, r.res), true
	return r
}

// withFile finishes an item an optimise policy won, with its full probe p.
// The probe is read again rather than trusted from the summary, so a file
// inspected again in the meantime is judged as it is now. If the parsed
// probe tells the policies anything the summary did not, they are evaluated
// again, so the result is what parsing every probe would give.
func (b *bulk) withFile(r *decided, p *store.Probe, ps []policy.Policy, now time.Time) {
	before := r.c.Policy.Facts
	r.c.attachProbe(p, b.isOptimised) // nil: forgotten since, so pending
	d, g := gate(r.c, b.excluded)
	switch g {
	case gatePending:
		return
	case gateDecided:
		r.res, r.d, r.ok = policy.Result{}, d, true
		return
	}
	if r.c.Policy.Facts != before {
		r.res = policy.Evaluate(ps, r.c.Policy, now)
	}
	r.d, r.ok = b.decide(&r.c, r.res), true
}

// evaluate re-evaluates every item and stores the results as one run (see
// store.BeginEvaluations). Items are read, their policies evaluated and
// their probes read on one goroutine, a page at a time, while the previous
// page is planned and saved on this one. Each page's evaluations are saved
// as soon as they are decided and then dropped. If anything fails the run
// is left unfinished, so items it did not reach keep their last evaluation.
func (s *Service) evaluate(ctx context.Context) (int, error) {
	b, err := s.loadBulk(ctx)
	if err != nil {
		return 0, err
	}
	run, err := s.store.BeginEvaluations(ctx)
	if err != nil {
		return 0, err
	}
	now := s.now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	read := make(chan batch)
	var readErr error
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		defer close(read)
		readErr = s.eachPage(ctx, func(pg page) error {
			bt, err := s.readBatch(ctx, b, pg, b.policies, now)
			if err != nil {
				return err
			}
			select {
			case read <- bt:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	n, err := s.saveBatches(ctx, b, run, read, now)
	if err != nil {
		cancel() // stops the reader
		for range read {
		}
	}
	reader.Wait()
	if err == nil {
		err = readErr
	}
	if err != nil {
		return 0, err
	}
	if _, err := s.store.FinishEvaluations(ctx, run); err != nil {
		return 0, err
	}
	return n, nil
}

// saveBatches plans each batch read and saves its evaluations in run,
// returning how many it saved.
func (s *Service) saveBatches(ctx context.Context, b *bulk, run int64, read <-chan batch, now time.Time) (int, error) {
	n := 0
	for bt := range read {
		out, err := b.finishBatch(ctx, bt, b.policies, now)
		if err != nil {
			return n, err
		}
		evs := make([]store.Evaluation, 0, len(out))
		for _, r := range out {
			if r.ok { // a pending item has no evaluation
				evs = append(evs, toEvaluation(r.c.Item.ID, r.res, r.d))
			}
		}
		if err := s.store.WriteEvaluations(ctx, run, evs); err != nil {
			return n, fmt.Errorf("saving evaluations: %w", err)
		}
		n += len(evs)
	}
	return n, nil
}

// parallel calls fn for 0..n-1 on up to workers goroutines and returns an
// error one of them hit. It stops handing out work after an error or when
// ctx ends.
func parallel(ctx context.Context, workers, n int, fn func(i int) error) error {
	workers = min(workers, runtime.GOMAXPROCS(0), n)
	var next atomic.Int64
	var failed atomic.Bool
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !failed.Load() {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				err := ctx.Err()
				if err == nil {
					err = fn(i)
				}
				if err != nil {
					errs[w] = err
					failed.Store(true)
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
