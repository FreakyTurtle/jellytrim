---
paths:
  - ".github/**"
  - "Dockerfile"
  - "docker-compose*.yml"
  - ".goreleaser.yaml"
  - "Taskfile.yml"
  - "scripts/**"
---

# Build, CI and release

- **Image:** `debian:trixie-slim` plus jellyfin-ffmpeg8 from a pinned `.deb` with sha256 per architecture. Go cross-compiles on `$BUILDPLATFORM` with `CGO_ENABLED=0`. Runs as a non-root UID (any UID works; `/config` must be writable). HEALTHCHECK on `/healthz`. Labels follow OCI (`org.opencontainers.image.*`).
- **Registry:** `ghcr.io/freakyturtle/jellytrim`. Tags `latest`, `X.Y.Z`, `X.Y` and (from 1.0) `X`, built from `v*` git tags only, via `docker/metadata-action`. Pull requests build without pushing.
- **Architectures:** `linux/amd64` and `linux/arm64`.
- **Binaries:** GoReleaser builds linux, darwin and windows for amd64 and arm64, with `checksums.txt`, attached to the GitHub release.
- **Workflows:** least-privilege `permissions:` per job. Actions pinned to a major version and kept current by Dependabot. No secrets other than the built-in `GITHUB_TOKEN`.
- **CI must match local:** CI runs the same `task` targets as `task check`. ffmpeg tests are required in CI (`JELLYTRIM_REQUIRE_FFMPEG=1`). CI installs the same jellyfin-ffmpeg version as the image (the Ubuntu noble package, pinned by sha256 in `ci.yml`), because the committed ffprobe fixtures depend on the ffmpeg version. Update the Dockerfile and `ci.yml` together.
- **Scripts** are `bash` with `set -euo pipefail`, pass `bash -n`, and never touch paths outside the repo (except the dev stack's Docker volumes).
- **No telemetry.** Nothing in the build or runtime calls home.
- Validate workflow changes with `actionlint`, Dockerfile changes with a local buildx build, GoReleaser changes with `goreleaser check`.
