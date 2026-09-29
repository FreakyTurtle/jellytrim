package library

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/pathmap"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// noEncoders is used until the hardware test has run.
type noEncoders struct{}

func (noEncoders) Select(media.Codec, bool, string) (string, bool, string) {
	return "", false, "The encoders have not been tested yet. Open Settings and run the hardware test."
}

// Candidate is everything known about one item, ready to evaluate.
type Candidate struct {
	Item   store.Item
	Probe  *store.Probe
	File   *media.File
	Facts  plan.FileFacts
	Policy policy.Item
	// Problem says why the item cannot be decided now, for example when
	// the check for a file JellyTrim already optimised failed. The item is
	// skipped rather than assumed safe.
	Problem string
}

// evalContext is what every candidate needs besides its own rows.
type evalContext struct {
	settings store.Settings
	policies []policy.Policy
	users    []store.JellyfinUser // the users whose favourites and plays count
	inactive map[string]bool      // users left out of the watched share
	watch    policy.WatchRule
	cols     map[string][]string // collection IDs by member ID
	roots    []string            // resolved local roots
	libs     map[string]store.Library
	env      plan.Env
	// links, when set, remembers resolved folders across one evaluation
	// run (see linkCache). Nil resolves every path afresh.
	links *linkCache
}

func (s *Service) loadEvalContext(ctx context.Context) (evalContext, error) {
	var ec evalContext
	var err error
	var w watchers
	if w, ec.settings, err = s.loadWatchers(ctx); err != nil {
		return ec, err
	}
	ec.users, ec.watch = w.history, w.rule
	ec.inactive = make(map[string]bool)
	for _, u := range w.users {
		if u.Inactive {
			ec.inactive[u.User.ID] = true
		}
	}
	if ec.policies, err = s.Policies(ctx); err != nil {
		return ec, err
	}
	if ec.cols, err = s.store.ItemCollections(ctx); err != nil {
		return ec, err
	}
	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return ec, err
	}
	ec.libs = make(map[string]store.Library, len(libs))
	for _, l := range libs {
		ec.libs[l.ID] = l
	}
	// Without mappings there are no roots, so nothing is "inside" them and
	// every optimise decision is skipped: the safe default.
	if m, err := s.Mapper(ctx); err == nil {
		ec.roots = resolveRoots(m.Roots())
	}
	var enc plan.Encoders = noEncoders{}
	if s.encoders != nil {
		enc = s.encoders
	}
	ec.env = plan.Env{MinSavingPercent: ec.settings.MinSavingPercent, MatchBitDepth: ec.settings.MatchBitDepth,
		DefaultQuality: ec.settings.DefaultQuality, Encoders: enc}
	return ec, nil
}

// loadCandidate builds the candidate for one item from probe p, without
// loading the whole library.
func (s *Service) loadCandidate(ctx context.Context, it store.Item, p *store.Probe) (Candidate, evalContext, error) {
	ec, err := s.loadEvalContext(ctx)
	if err != nil {
		return Candidate{}, ec, err
	}
	if lib, ok := ec.libs[it.LibraryID]; !ok || !lib.Managed {
		return Candidate{}, ec, errors.New("the item is no longer in a managed library")
	}
	ud, err := s.store.ItemUserData(ctx, it.ID)
	if err != nil {
		return Candidate{}, ec, err
	}
	isOptimised := func(id store.FileIdentity) (bool, error) { return s.store.IsOptimised(ctx, id) }
	return ec.candidate(it, p, ud, isOptimised), ec, nil
}

// candidate assembles one item from its full probe p, parsing the ffprobe
// output.
func (ec *evalContext) candidate(it store.Item, p *store.Probe, ud []store.UserData,
	isOptimised func(store.FileIdentity) (bool, error)) Candidate {
	c := ec.baseCandidate(it, ud)
	c.attachProbe(p, isOptimised)
	return c
}

// baseCandidate is the item with its watch state, before any probe.
func (ec *evalContext) baseCandidate(it store.Item, ud []store.UserData) Candidate {
	c := Candidate{Item: it}
	c.Policy = policy.Item{
		ID: it.ID, Name: it.Name, Type: it.Type, LibraryID: it.LibraryID, LibraryName: ec.libs[it.LibraryID].Name,
		SeriesID: it.SeriesID, SeriesName: it.SeriesName, SeasonNumber: it.SeasonNumber, Collections: ec.collectionsFor(it),
		Tags: it.Tags, Genres: it.Genres, DateAdded: it.DateAdded, Watch: ec.watch,
		Size: it.JellyfinSize,
	}
	if len(ec.users) > 0 {
		c.Policy.Users = make([]policy.UserState, 0, len(ec.users))
	}
	for _, u := range ec.users {
		var d store.UserData
		for _, x := range ud {
			if x.UserID == u.ID {
				d = x
				break
			}
		}
		c.Policy.Users = append(c.Policy.Users, policy.UserState{UserID: u.ID, Name: u.Name, Played: d.Played,
			Favourite: d.Favourite, LastPlayed: d.LastPlayedAt, Inactive: ec.inactive[u.ID]})
	}
	return c
}

// attachProbe sets the candidate's probe, parsing its ffprobe output. A
// probe of a different path than the item's current one describes another
// file, so the item counts as not inspected.
func (c *Candidate) attachProbe(p *store.Probe, isOptimised func(store.FileIdentity) (bool, error)) {
	c.detachProbe()
	if p == nil || p.LocalPath != c.Item.LocalPath {
		return
	}
	c.Probe = p
	c.Facts = plan.FileFacts{Probed: p.Error == "" && p.ProbeJSON != "", ProbeError: p.Error, IsSymlink: p.IsSymlink, Nlink: p.Nlink}
	if !c.Facts.Probed {
		return
	}
	f, err := media.Parse([]byte(p.ProbeJSON), []byte(p.FrameJSON), p.Size)
	if err != nil {
		c.Facts.Probed, c.Facts.ProbeError = false, err.Error()
		return
	}
	c.File, c.Policy.File = f, f
	c.Policy.Facts = policy.FactsFromFile(f)
	opt, err := isOptimised(p.FileIdentity)
	if err != nil {
		c.Problem = "JellyTrim could not check whether it already optimised this file: " + err.Error()
	}
	c.Facts.Optimised = opt
}

// attachSummary sets the candidate's probe from its summary columns, without
// parsing the ffprobe output: enough for the policies, but not for a plan
// (see bulk.withFile).
func (c *Candidate) attachSummary(p *store.Probe, optimised map[store.FileIdentity]bool) {
	c.detachProbe()
	if p.LocalPath != c.Item.LocalPath {
		return
	}
	c.Probe = p
	c.Facts = plan.FileFacts{Probed: p.Error == "", ProbeError: p.Error, IsSymlink: p.IsSymlink, Nlink: p.Nlink}
	if !c.Facts.Probed {
		return
	}
	c.Policy.Facts = factsFromSummary(p)
	c.Facts.Optimised = optimised[p.FileIdentity]
}

// detachProbe forgets everything the candidate knew from a probe.
func (c *Candidate) detachProbe() {
	c.Probe, c.File, c.Facts, c.Problem = nil, nil, plan.FileFacts{}, ""
	c.Policy.File, c.Policy.Facts = nil, policy.Facts{}
}

// collectionsFor returns the collections holding the item itself, its
// series or its season: a box set may list a whole series or season.
func (ec *evalContext) collectionsFor(it store.Item) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range []string{it.ID, it.SeriesID, it.SeasonID} {
		if id == "" {
			continue
		}
		for _, c := range ec.cols[id] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// gateResult says whether an item reaches the policies.
type gateResult int

const (
	gateOpen    gateResult = iota // evaluate the policies
	gatePending                   // not inspected yet: no decision
	gateDecided                   // decided before the policies
)

// gate applies the per-item checks that come before any policy: exclusions,
// items Jellyfin sync marked as unmanageable, files not inspected yet, and
// facts that could not be checked. Evaluate, Preview and Reassess all use it
// so they cannot disagree.
func gate(c Candidate, excluded map[string]string) (plan.Decision, gateResult) {
	if reason, ok := excluded[c.Item.ID]; ok {
		return plan.Decision{Outcome: plan.Protected, Summary: reason,
			Reasons: []plan.Reason{{Code: "excluded", Text: reason}}}, gateDecided
	}
	if r := c.Item.SyncSkipReason; r != "" {
		return plan.Decision{Outcome: plan.Skipped, Summary: "Skipped: " + r,
			Reasons: []plan.Reason{{Code: "sync", Text: r}}}, gateDecided
	}
	if c.Probe == nil {
		return plan.Decision{}, gatePending
	}
	if c.Problem != "" {
		return plan.Decision{Outcome: plan.Skipped, Summary: "Skipped: " + c.Problem,
			Reasons: []plan.Reason{{Code: "check_failed", Text: c.Problem}}}, gateDecided
	}
	return plan.Decision{}, gateOpen
}

// decide turns a policy result into a decision. Whether the file is inside
// the roots is only worked out (resolving symbolic links on disk) when an
// optimise policy won, the one case that needs it.
func (ec *evalContext) decide(c *Candidate, res policy.Result) plan.Decision {
	if res.Winner != nil && res.Winner.Action.Kind != policy.KindProtect {
		resolve := filepath.EvalSymlinks
		if ec.links != nil {
			resolve = ec.links.evalSymlinks
		}
		c.Facts.InRoots = inRoots(ec.roots, c.Item.LocalPath, resolve)
	}
	return plan.Decide(c.Policy, res, c.Facts, ec.env)
}

// resolveRoots returns each mapped root with symbolic links resolved, so a
// mapped folder that is itself reached through a link still matches.
func resolveRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if res, err := filepath.EvalSymlinks(r); err == nil {
			out = append(out, filepath.ToSlash(res))
		} else {
			out = append(out, r)
		}
	}
	return out
}

// inRoots checks the file's resolved path against the resolved roots, so a
// symbolic link cannot lead JellyTrim outside them.
func inRoots(roots []string, p string, resolve func(string) (string, error)) bool {
	if p == "" {
		return false
	}
	resolved, err := resolve(p)
	if err != nil {
		return false
	}
	return pathmap.Within(roots, filepath.ToSlash(resolved))
}

// Evaluate re-evaluates every item and stores the results.
func (s *Service) Evaluate(ctx context.Context) (int, error) {
	if !s.begin() {
		return 0, ErrBusy
	}
	n, err := s.evaluate(ctx)
	s.end(err)
	return n, err
}

func toEvaluation(itemID string, res policy.Result, d plan.Decision) store.Evaluation {
	e := store.Evaluation{ItemID: itemID, Outcome: string(d.Outcome), Summary: d.Summary,
		Explanation: "[]", Reasons: mustJSON(d.Reasons)}
	if res.Matches != nil {
		e.Explanation = mustJSON(res.Matches)
	}
	if d.PolicyID != 0 {
		id := d.PolicyID
		e.PolicyID = &id
	}
	if d.Plan != nil {
		e.Plan = mustJSON(d.Plan)
		lo, hi := d.Plan.EstMin, d.Plan.EstMax
		e.EstMin, e.EstMax = &lo, &hi
	}
	return e
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
