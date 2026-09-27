---
name: encoder-profile
description: Add a new encoder backend to JellyTrim or retune an existing one's quality profile, with golden argument tests, a hardware probe, and documentation of the chosen values.
argument-hint: "<encoder, e.g. nvenc-hevc, or 'retune x265'>"
---

Encoder work: **$ARGUMENTS**

1. Have the **media-specialist** review the current `internal/encoder` design and propose the native settings per quality tier, with reasons and sources.
2. Implement the backend behind the `encoder.Backend` interface:
   - `Name`, `Codec`, `Kind` (hardware or software).
   - Argument building for input options, filter chain (scale, pixel format) and encoder options.
   - The hardware probe: a real test encode of a `lavfi` source with the exact rate-control arguments used in production, and the HDR metadata passthrough test.
3. Golden tests in `internal/encoder/testdata/` for each tier, each resolution cap, SDR, HDR10 and 10-bit sources. Run `go test ./internal/encoder/... -update` only when the change is deliberate, then review the golden diff line by line.
4. If the hardware is available locally, run a real encode on fixtures and measure VMAF. If not, say so in `docs/TRANSCODING.md`.
5. Update the quality table and the backend's section in `docs/TRANSCODING.md`.
6. Run the **safety-reviewer** on the diff.
