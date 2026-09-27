# Architecture decision records

Short records of decisions that are not obvious from the code. Add one with `/adr-new`.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-pure-go-sqlite.md) | SQLite through the pure-Go modernc.org/sqlite driver | Accepted |
| [0002](0002-first-match-policies.md) | Policies are evaluated in order; the first match wins | Accepted |
| [0003](0003-keep-container-and-path.md) | Keep the source container and path; let the modification time change | Accepted |
| [0004](0004-jellyfin-ffmpeg-image.md) | Ship jellyfin-ffmpeg 8 in the official image | Accepted |
| [0005](0005-commit-templ-output.md) | Commit generated templ files | Accepted |
| [0006](0006-10-bit-hevc-output.md) | Encode HEVC in 10-bit by default | Accepted |
| [0007](0007-hdr-handling.md) | HDR10 and HLG transcoded; dynamic HDR and Dolby Vision opt-in or skipped | Accepted |
| [0008](0008-no-built-in-auth.md) | No built-in authentication | Accepted |
