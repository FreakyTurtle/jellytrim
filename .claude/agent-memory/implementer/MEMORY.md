# implementer memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.


- 2026-09-27: `go:embed` cannot reach a parent directory, so jellyfintest finds `internal/jellyfin/testdata` with `runtime.Caller(0)`. Read fixtures through `os.OpenRoot(dir).ReadFile(name)` to satisfy gosec G304 without a nolint.
- 2026-09-27: errcheck flags `defer resp.Body.Close()`; write `defer func() { _ = resp.Body.Close() }()`.
- 2026-09-27: revive var-naming wants `ProviderIDs` even where the JSON key is `ProviderIds`; keep the Go name idiomatic and put Jellyfin's spelling in the json tag.
