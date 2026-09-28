package library

import (
	"context"
	"errors"
	"fmt"

	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// ErrFileChanging is returned by Reassess when the file kept changing while
// it was being inspected.
var ErrFileChanging = errors.New("the file is changing; try again later")

// Assessment is a fresh decision for one item, made just before encoding.
type Assessment struct {
	Candidate Candidate
	Decision  plan.Decision
	Identity  fileid.Info
	// File is parsed from the probe Reassess itself ran, never from an
	// older stored probe.
	File     *media.File
	Roots    []string // resolved local roots
	Settings store.Settings
}

// Reassess probes one item's file again and re-evaluates it with the current
// policies, so a job never runs on stale information. It does not take the
// sync lock: it reads shared state and writes only this item's probe. It
// loads only this item, not the whole library.
func (s *Service) Reassess(ctx context.Context, itemID string) (Assessment, error) {
	var a Assessment
	it, err := s.store.Item(ctx, itemID)
	if err != nil {
		return a, err
	}
	excluded, err := s.store.Exclusions(ctx)
	if err != nil {
		return a, fmt.Errorf("reading exclusions: %w", err)
	}
	if _, ok := excluded[itemID]; ok {
		a.Decision, _ = gate(Candidate{Item: it}, excluded)
		return a, nil
	}
	if it.LocalPath == "" || it.SyncSkipReason != "" {
		return a, errors.New("the item has no local file JellyTrim can manage")
	}
	p, info, err := s.freshProbe(ctx, it)
	if err != nil {
		return a, err
	}
	c, ec, err := s.loadCandidate(ctx, it, &p)
	if err != nil {
		return a, err
	}
	a.Identity, a.Roots, a.Settings = info, ec.roots, ec.settings
	d, g := gate(c, excluded)
	switch g {
	case gateDecided:
		a.Candidate, a.File, a.Decision = c, c.File, d
		return a, nil
	case gatePending:
		return a, errors.New("the file could not be inspected")
	}
	res := policy.Evaluate(ec.policies, c.Policy, s.now())
	a.Decision = ec.decide(&c, res)
	a.Candidate, a.File = c, c.File
	return a, nil
}

// freshProbe probes the item's file now and returns that probe. It checks
// that the file did not change while ffprobe read it, and that the stored
// probe is the one just made: a background probe of an older version of the
// file could be saved over it. Either way it tries once more, then gives up
// with ErrFileChanging.
func (s *Service) freshProbe(ctx context.Context, it store.Item) (store.Probe, fileid.Info, error) {
	for range 2 {
		before, err := fileid.Stat(it.LocalPath)
		if err != nil {
			return store.Probe{}, before, fmt.Errorf("reading %s: %w", it.LocalPath, err)
		}
		p, ok := s.probeFile(ctx, probeTask{item: it, info: before})
		if !ok {
			return p, before, probeFailure(ctx, p)
		}
		after, err := fileid.Stat(it.LocalPath)
		if err != nil {
			return p, before, fmt.Errorf("reading %s: %w", it.LocalPath, err)
		}
		stored, err := s.store.Probe(ctx, it.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return p, before, err
		}
		if before.Same(after) && before.Nlink == after.Nlink && before.IsSymlink == after.IsSymlink &&
			err == nil && stored.FileIdentity == p.FileIdentity && stored.LocalPath == p.LocalPath && stored.Error == "" {
			return p, before, nil
		}
	}
	return store.Probe{}, fileid.Info{}, ErrFileChanging
}

func probeFailure(ctx context.Context, p store.Probe) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.Error == "" {
		return errors.New("inspecting the file: the result could not be saved")
	}
	return fmt.Errorf("inspecting the file: %s", p.Error)
}
