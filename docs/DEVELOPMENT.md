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
task dev:bootstrap   # set up Jellyfin: users dev/dev, alex/alex, sam/sam, robin/robin with different
                     # watch states (it prints who watched what), libraries, API key
```

Jellyfin sees the media at `/media/movies` and `/media/tv`; JellyTrim sees it at `/mnt/media/movies` and `/mnt/media/tv`, so the path mapping step is exercised for real. The API key is written to `dev/jellyfin-api-key`. JellyTrim itself runs on `http://localhost:8097` in the dev stack (the same port as `task dev`; `:8080` is the default only inside the shipped image). Everything under `dev/` is gitignored.

To start again: `task dev:stack:reset`.

### Seeding a config without the wizard

`scripts/devseed` sets up a local config directory as if the setup wizard had been run by hand: it saves the Jellyfin connection, manages every library, selects the dev user, maps the Jellyfin paths to the fixture media, adds the starter policies, and runs a sync. Run it after `task dev:bootstrap`:

```
go run ./scripts/devseed -config tmp/config
```

Flags:

| Flag | Default | What it does |
|---|---|---|
| `-config` | `tmp/config` | The JellyTrim config directory to prepare |
| `-jellyfin` | `http://localhost:8096` | The dev Jellyfin's URL |
| `-key-file` | `dev/jellyfin-api-key` | Where to read the dev API key from |
| `-media` | `dev/media` | The local folder holding the fixture media |
| `-enable-all` | `false` | Enable every starter policy, not just Protect favourites |
| `-assume-x265` | `true` | Treat software x265 as tested, so plans can be made before the hardware test runs |
| `-live` | `false` | Turn Dry Run off, so JellyTrim will really optimise the fixture files |

Development only; it is not part of the shipped image.

### Never open the database from macOS while a container has it open

Do not open `tmp/config/jellytrim.db` (or any config directory a running container is using) with a SQLite client on macOS while that directory is bind-mounted into a container on Docker Desktop or OrbStack. SQLite's WAL mode needs shared memory between everything that has the file open, and that shared memory does not work across the file-sharing boundary between the macOS host and the Linux VM the container runs in. The usual symptom is corruption or "database disk image is malformed".

If you need to inspect the database while the dev stack is running, do it from inside a container (for example `docker compose exec jellytrim sh` and a SQLite client installed there), not from the host.

## Configuration

JellyTrim reads a few bootstrap settings from the environment; everything else is set in the UI and stored in SQLite. See `config.example.env`.

## Project layout

See `AGENTS.md` (Layout) and `docs/ARCHITECTURE.md`.

## Releasing

Releases are cut by tagging `vX.Y.Z` on `main`. The release workflow builds binaries with GoReleaser and multi-architecture images, and publishes them to GitHub Releases and `ghcr.io/freakyturtle/jellytrim`. See the `/release` skill for the checklist.

## Agent setup

The repository includes configuration for Claude Code and Codex. See `docs/agents/README.md`. None of it is needed to build or run JellyTrim.
