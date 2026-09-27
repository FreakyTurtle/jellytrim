# Development

## Prerequisites

- Go 1.26 or later (an older Go downloads 1.26 automatically through `GOTOOLCHAIN=auto`).
- [Task](https://taskfile.dev).
- ffmpeg and ffprobe 7 or later, built with libx265 (Homebrew and most Linux distributions include it).
- Docker with Compose and Buildx, for the dev stack and image builds.
- golangci-lint v2.
- Optional: `jq` (used by the agent hooks and the bootstrap script).

templ does not need installing: it is pinned in `go.mod` as a tool and run with `go tool templ`.

## First time

```
task setup      # installs the git pre-commit hook, downloads modules
task check      # everything CI runs
```

## Everyday loop

```
task dev        # runs JellyTrim on http://localhost:8097 with ./tmp/config, rebuilding on change
task test       # go test -race ./...
task lint
task generate   # after editing .templ files (the dev task does this for you)
```

Generated `*_templ.go` files are committed. The pre-commit hook stops you committing a `.templ` change without its generated file.

## Dev stack: a throwaway Jellyfin with synthetic media

```
task dev:fixtures    # generate small test clips into dev/media (never real media)
task dev:stack       # Jellyfin on :8096, JellyTrim (built from the Dockerfile) on :8097
task dev:bootstrap   # set up Jellyfin: user dev/dev, libraries, some items watched, API key
```

Jellyfin sees the media at `/media/movies` and `/media/tv`; JellyTrim sees it at `/mnt/media/movies` and `/mnt/media/tv`, so the path mapping step is exercised for real. The API key is written to `dev/jellyfin-api-key`. Everything under `dev/` is gitignored.

To start again: `task dev:stack:reset`.

## Configuration

JellyTrim reads a few bootstrap settings from the environment; everything else is set in the UI and stored in SQLite. See `config.example.env`.

## Project layout

See `AGENTS.md` (Layout) and `docs/ARCHITECTURE.md`.

## Releasing

Releases are cut by tagging `vX.Y.Z` on `main`. The release workflow builds binaries with GoReleaser and multi-architecture images, and publishes them to GitHub Releases and `ghcr.io/freakyturtle/jellytrim`. See the `/release` skill for the checklist.

## Agent setup

The repository includes configuration for Claude Code and Codex. See `docs/agents/README.md`. None of it is needed to build or run JellyTrim.
