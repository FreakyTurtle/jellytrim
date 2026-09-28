package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/web"
)

// hardware owns the encoder registry: it loads the last test results at
// start, re-tests in the background, and stores the results.
type hardware struct {
	registry *encoder.Registry
	runner   *ffmpeg.Runner
	store    *store.Store
	library  *library.Service
	log      *slog.Logger
	mu       sync.Mutex // one test at a time
}

// load applies stored results so decisions work before the re-test ends.
func (h *hardware) load(ctx context.Context) {
	rows, err := h.store.Capabilities(ctx)
	if err != nil {
		return
	}
	var caps []encoder.Capability
	for _, r := range rows {
		var c encoder.Capability
		if json.Unmarshal([]byte(r.Detail), &c) == nil {
			caps = append(caps, c)
		}
	}
	h.registry.SetCapabilities(caps)
}

// Retest runs the hardware test, stores it and re-evaluates the library.
func (h *hardware) Retest(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	caps := h.registry.Detect(ctx, h.runner)
	rows := make([]store.CapabilityRow, 0, len(caps))
	for _, c := range caps {
		b, _ := json.Marshal(c)
		rows = append(rows, store.CapabilityRow{Backend: c.Backend, Available: c.Available, Detail: string(b), TestedAt: c.TestedAt})
		h.log.Info("hardware: tested encoder", "backend", c.Backend, "available", c.Available, "hdr", c.HDRPassthrough, "error", c.Error)
	}
	if err := h.store.SaveCapabilities(ctx, rows); err != nil {
		return err
	}
	h.library.EvaluateAsync()
	return nil
}

// Capabilities converts the registry's results for the web layer.
func (h *hardware) Capabilities() []web.Capability {
	var out []web.Capability
	for _, c := range h.registry.Capabilities() {
		out = append(out, web.Capability{
			Backend: c.Backend, Label: c.Label, Codec: c.Codec.Label(), Hardware: c.Hardware, Available: c.Available,
			HDR: c.HDRPassthrough, Device: c.Device, Detail: c.Detail, Error: c.Error, FFmpegVersion: c.FFmpegVersion,
			TestedAt: c.TestedAt, HardwareDecode: c.HWDecode,
		})
	}
	return out
}
