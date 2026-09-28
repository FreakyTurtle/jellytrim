package encoder

import (
	"context"
	"fmt"
	"sync"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
)

// Auto is the backend request that lets the registry choose.
const Auto = "auto"

// Registry holds the backends and what detection proved about them. It
// implements plan.Encoders. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	backends []Backend // hardware first
	// preferSoftware puts software backends first in Auto.
	preferSoftware bool
}

var _ plan.Encoders = (*Registry)(nil)

// DefaultBackends are JellyTrim's backends, hardware first. qsvDevice ""
// means DefaultQSVDevice.
func DefaultBackends(qsvDevice string) []Backend {
	return []Backend{QSV{Device: qsvDevice}, X265{}}
}

// NewRegistry returns a registry over backends (in Auto's hardware-first
// order). Nothing is available until capabilities are set or detected.
func NewRegistry(backends []Backend) *Registry {
	return &Registry{backends: backends}
}

// SetPreferSoftware makes Auto try software backends before hardware.
func (r *Registry) SetPreferSoftware(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preferSoftware = v
}

// SetCapabilities records detection results (for example loaded from the
// database). Results for unknown backends are ignored.
func (r *Registry) SetCapabilities(caps []Capability) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range caps {
		for i, b := range r.backends {
			if b.Name() == c.Backend {
				r.backends[i] = b.WithCapability(c)
			}
		}
	}
}

// Detect tests every backend and records the results.
func (r *Registry) Detect(ctx context.Context, runner *ffmpeg.Runner) []Capability {
	r.mu.RLock()
	backends := append([]Backend(nil), r.backends...)
	r.mu.RUnlock()
	caps := Detect(ctx, runner, backends)
	r.SetCapabilities(caps)
	return caps
}

// Capabilities returns every backend's capability, for the Settings page.
func (r *Registry) Capabilities() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	caps := make([]Capability, 0, len(r.backends))
	for _, b := range r.backends {
		c := b.Capability()
		if c.Backend == "" {
			c = capabilityFor(b)
			c.Error = "Not tested yet."
		}
		caps = append(caps, c)
	}
	return caps
}

// Backend returns the named backend with its capability.
func (r *Registry) Backend(name string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, b := range r.backends {
		if b.Name() == name {
			return b, true
		}
	}
	return nil, false
}

// Select implements plan.Encoders. requested is "auto" (or empty) or a
// backend name. hdr asks for a backend that proved it keeps HDR metadata.
func (r *Registry) Select(codec media.Codec, hdr bool, requested string) (string, bool, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if requested != "" && requested != Auto {
		return r.selectNamed(codec, hdr, requested)
	}
	var usable, any bool
	for _, b := range r.ordered() {
		if b.Codec() != codec {
			continue
		}
		any = true
		c := b.Capability()
		if !c.Available {
			continue
		}
		usable = true
		if !hdr || c.HDRPassthrough {
			return b.Name(), true, ""
		}
	}
	switch {
	case !any:
		return "", false, fmt.Sprintf("JellyTrim has no encoder for %s.", codec.Label())
	case !usable:
		return "", false, fmt.Sprintf("No %s encoder passed its test on this machine. See Settings for details.", codec.Label())
	}
	return "", false, fmt.Sprintf("No %s encoder on this machine proved that it keeps HDR metadata.", codec.Label())
}

func (r *Registry) selectNamed(codec media.Codec, hdr bool, name string) (string, bool, string) {
	var b Backend
	for _, candidate := range r.backends {
		if candidate.Name() == name {
			b = candidate
		}
	}
	switch {
	case b == nil:
		return "", false, fmt.Sprintf("The encoder %q was requested but JellyTrim does not know it.", name)
	case b.Codec() != codec:
		return "", false, fmt.Sprintf("%s was requested but it cannot produce %s.", shortLabel(b), codec.Label())
	case !b.Capability().Available:
		test := "its test"
		if b.Hardware() {
			test = "the hardware test"
		}
		return "", false, fmt.Sprintf("%s was requested but did not pass %s.", shortLabel(b), test)
	case hdr && !b.Capability().HDRPassthrough:
		return "", false, fmt.Sprintf("%s was requested but did not prove that it keeps HDR metadata.", shortLabel(b))
	}
	return b.Name(), true, ""
}

// ordered is the Auto preference order. The caller holds the lock.
func (r *Registry) ordered() []Backend {
	out := make([]Backend, 0, len(r.backends))
	first := !r.preferSoftware // hardware first unless software is preferred
	for _, pass := range []bool{first, !first} {
		for _, b := range r.backends {
			if b.Hardware() == pass {
				out = append(out, b)
			}
		}
	}
	return out
}

// shortLabel drops the codec from the label in sentences ("Intel Quick
// Sync was requested...").
func shortLabel(b Backend) string {
	switch b.Name() {
	case NameQSV:
		return "Intel Quick Sync"
	case NameX265:
		return "Software x265"
	}
	return b.Label()
}
