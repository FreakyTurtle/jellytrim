package library

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"

	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// probeWorkers bounds concurrent ffprobe runs: probing reads file headers,
// which is cheap, but network filesystems dislike many parallel readers.
const probeWorkers = 2

// Probe inspects changed files. It is exposed for "Inspect again" in the UI.
func (s *Service) Probe(ctx context.Context) (probed, failed int, err error) {
	if !s.begin() {
		return 0, 0, ErrBusy
	}
	probed, failed, err = s.probeChanged(ctx)
	if err == nil {
		_, err = s.evaluate(ctx)
	}
	s.end(err)
	return probed, failed, err
}

// ReprobeItem forgets an item's cached probe so the next run inspects it.
func (s *Service) ReprobeItem(ctx context.Context, itemID string) error {
	return s.store.DeleteProbe(ctx, itemID)
}

type probeTask struct {
	item store.Item
	info fileid.Info
	err  error
}

// probeChanged runs ffprobe on every managed item whose file identity
// changed since it was last probed, or that has never been probed.
func (s *Service) probeChanged(ctx context.Context) (probed, failed int, err error) {
	items, err := s.store.ManagedItems(ctx)
	if err != nil {
		return 0, 0, err
	}
	cached, err := s.store.ProbeIdentities(ctx)
	if err != nil {
		return 0, 0, err
	}
	var todo []probeTask
	for _, it := range items {
		if it.LocalPath == "" || it.SyncSkipReason != "" {
			continue
		}
		info, statErr := fileid.Stat(it.LocalPath)
		if p, ok := cached[it.ID]; ok && statErr == nil && unchanged(p, it, info) {
			continue
		}
		todo = append(todo, probeTask{item: it, info: info, err: statErr})
	}
	return s.runProbes(ctx, todo)
}

// unchanged reports whether a successful cached probe still describes the
// item's file.
func unchanged(p store.ProbeIdentity, it store.Item, info fileid.Info) bool {
	return p.Error == "" && p.LocalPath == it.LocalPath && p.Dev == info.Dev && p.Inode == info.Inode &&
		p.Size == info.Size && p.MtimeNs == info.MtimeNs && p.Nlink == info.Nlink && p.IsSymlink == info.IsSymlink
}

func (s *Service) runProbes(ctx context.Context, todo []probeTask) (probed, failed int, err error) {
	var mu sync.Mutex
	done := 0
	work := make(chan probeTask)
	var wg sync.WaitGroup
	for range probeWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				ok := s.probeOne(ctx, t)
				mu.Lock()
				done++
				if ok {
					probed++
				} else {
					failed++
				}
				s.setPhase("Inspecting files", done, len(todo))
				mu.Unlock()
			}
		}()
	}
	s.setPhase("Inspecting files", 0, len(todo))
	for _, t := range todo {
		if ctx.Err() != nil {
			break
		}
		work <- t
	}
	close(work)
	wg.Wait()
	return probed, failed, ctx.Err()
}

// probeOne probes a file and stores the result, including failures, so the
// UI can say exactly what went wrong.
func (s *Service) probeOne(ctx context.Context, t probeTask) bool {
	_, ok := s.probeFile(ctx, t)
	return ok
}

// probeFile is probeOne that also returns the probe it stored.
func (s *Service) probeFile(ctx context.Context, t probeTask) (store.Probe, bool) {
	p := store.Probe{ItemID: t.item.ID, LocalPath: t.item.LocalPath}
	if t.err != nil {
		p.Error = statError(t.item.LocalPath, t.err)
		return p, s.saveProbe(ctx, p)
	}
	p.FileIdentity = store.FileIdentity{Dev: t.info.Dev, Inode: t.info.Inode, Size: t.info.Size, MtimeNs: t.info.MtimeNs}
	p.Nlink, p.IsSymlink = t.info.Nlink, t.info.IsSymlink
	probeJSON, frameJSON, err := s.prober.Probe(ctx, t.item.LocalPath)
	if err != nil {
		if ctx.Err() != nil {
			return p, false
		}
		p.Error = err.Error()
		return p, s.saveProbe(ctx, p)
	}
	f, err := media.Parse(probeJSON, frameJSON, t.info.Size)
	if err != nil {
		p.Error = "ffprobe output could not be read: " + err.Error()
		return p, s.saveProbe(ctx, p)
	}
	p.ProbeJSON, p.FrameJSON = string(probeJSON), string(frameJSON)
	fillSummary(&p, f)
	return p, s.saveProbe(ctx, p)
}

func (s *Service) saveProbe(ctx context.Context, p store.Probe) bool {
	if err := s.store.SaveProbe(ctx, p); err != nil {
		s.log.Error("library: saving probe", "item", p.ItemID, "err", err)
		return false
	}
	return p.Error == ""
}

func fillSummary(p *store.Probe, f *media.File) {
	p.Container = string(f.Container)
	p.DurationMs = f.Duration.Milliseconds()
	p.HDR = string(f.HDR().Class)
	if bps, ok := f.VideoBitrate(); ok {
		p.VideoBitrate = bps
	}
	if v, ok := f.MainVideo(); ok {
		p.VideoCodec = string(v.Codec)
		p.Width, p.Height = v.Width, v.Height
		p.Resolution = int(v.Resolution())
	}
}

func statError(path string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "The file was not found at " + path + ". Check the path mapping for " + filepath.Dir(path) + "."
	case errors.Is(err, fs.ErrPermission):
		return "JellyTrim does not have permission to read " + path + "."
	}
	return "Could not read " + path + ": " + err.Error()
}
