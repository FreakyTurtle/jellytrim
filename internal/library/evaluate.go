package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	users    []store.JellyfinUser
	cols     map[string][]string // collection IDs by member ID
	roots    []string            // resolved local roots
	libs     map[string]store.Library
	env      plan.Env
}

// snapshot is every managed item with its watch state, collections and
// parsed probe.
type snapshot struct {
	evalContext
	candidates []Candidate
}

func (s *Service) loadEvalContext(ctx context.Context) (evalContext, error) {
	var ec evalContext
	var err error
	if ec.settings, err = s.store.Settings(ctx); err != nil {
		return ec, err
	}
	if ec.policies, err = s.Policies(ctx); err != nil {
		return ec, err
	}
	if ec.users, err = s.store.SelectedUsers(ctx); err != nil {
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

func (s *Service) loadSnapshot(ctx context.Context) (snapshot, error) {
	var snap snapshot
	var err error
	if snap.evalContext, err = s.loadEvalContext(ctx); err != nil {
		return snap, err
	}
	items, err := s.store.ManagedItems(ctx)
	if err != nil {
		return snap, err
	}
	userData, err := s.store.AllUserData(ctx)
	if err != nil {
		return snap, err
	}
	probes, err := s.store.Probes(ctx)
	if err != nil {
		return snap, err
	}
	optimised, err := s.store.OptimisedIdentities(ctx)
	if err != nil {
		return snap, err
	}
	isOptimised := func(id store.FileIdentity) (bool, error) { return optimised[id], nil }
	snap.candidates = make([]Candidate, 0, len(items))
	for _, it := range items {
		var p *store.Probe
		if pr, ok := probes[it.ID]; ok {
			p = &pr
		}
		snap.candidates = append(snap.candidates, snap.candidate(it, p, userData[it.ID], isOptimised))
	}
	return snap, nil
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

// candidate assembles one item. A probe of a different path than the item's
// current one describes another file, so the item counts as not inspected.
func (ec *evalContext) candidate(it store.Item, p *store.Probe, ud []store.UserData,
	isOptimised func(store.FileIdentity) (bool, error)) Candidate {
	c := Candidate{Item: it}
	c.Policy = policy.Item{
		ID: it.ID, Name: it.Name, Type: it.Type, LibraryID: it.LibraryID, LibraryName: ec.libs[it.LibraryID].Name,
		SeriesID: it.SeriesID, SeriesName: it.SeriesName, SeasonNumber: it.SeasonNumber, Collections: ec.collectionsFor(it),
		Tags: it.Tags, Genres: it.Genres, DateAdded: it.DateAdded, WatchMode: policy.WatchMode(ec.settings.WatchMode),
		Size: it.JellyfinSize,
	}
	byUser := map[string]store.UserData{}
	for _, d := range ud {
		byUser[d.UserID] = d
	}
	for _, u := range ec.users {
		d := byUser[u.ID]
		c.Policy.Users = append(c.Policy.Users, policy.UserState{UserID: u.ID, Name: u.Name, Played: d.Played,
			Favourite: d.Favourite, LastPlayed: d.LastPlayedAt})
	}
	if p == nil || p.LocalPath != it.LocalPath {
		return c
	}
	c.Probe = p
	c.Facts = plan.FileFacts{Probed: p.Error == "" && p.ProbeJSON != "", ProbeError: p.Error, IsSymlink: p.IsSymlink, Nlink: p.Nlink}
	if !c.Facts.Probed {
		return c
	}
	f, err := media.Parse([]byte(p.ProbeJSON), []byte(p.FrameJSON), p.Size)
	if err != nil {
		c.Facts.Probed, c.Facts.ProbeError = false, err.Error()
		return c
	}
	c.File, c.Policy.File = f, f
	opt, err := isOptimised(p.FileIdentity)
	if err != nil {
		c.Problem = "JellyTrim could not check whether it already optimised this file: " + err.Error()
	}
	c.Facts.Optimised = opt
	return c
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
		c.Facts.InRoots = inRoots(ec.roots, c.Item.LocalPath)
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
func inRoots(roots []string, p string) bool {
	if p == "" {
		return false
	}
	resolved, err := filepath.EvalSymlinks(p)
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

func (s *Service) evaluate(ctx context.Context) (int, error) {
	snap, err := s.loadSnapshot(ctx)
	if err != nil {
		return 0, err
	}
	excluded, err := s.store.Exclusions(ctx)
	if err != nil {
		return 0, err
	}
	now := s.now()
	evs := make([]store.Evaluation, 0, len(snap.candidates))
	for i := range snap.candidates {
		c := &snap.candidates[i]
		d, g := gate(*c, excluded)
		switch g {
		case gatePending:
			continue // shown as "pending"
		case gateDecided:
			evs = append(evs, toEvaluation(c.Item.ID, policy.Result{}, d))
			continue
		}
		res := policy.Evaluate(snap.policies, c.Policy, now)
		evs = append(evs, toEvaluation(c.Item.ID, res, snap.decide(c, res)))
	}
	if err := s.store.ReplaceEvaluations(ctx, evs); err != nil {
		return 0, fmt.Errorf("saving evaluations: %w", err)
	}
	return len(evs), nil
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
