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

## Demo mode: an invented library for screenshots and UI work

```
task demo                     # or: go run ./scripts/demo
task demo -- -reset -running  # rebuild from scratch, and show a job encoding
```

The demo serves the real web UI on `http://127.0.0.1:8098` with an invented library that looks lived in. It needs no Docker, no Jellyfin and no ffmpeg. Every title, person and job in it is made up.

What it contains:

- 40 films and three series (88 items) in two libraries, Films and TV: 4K H.264 and HDR10 remuxes of 40 to 70 GB, 1080p H.264 and HEVC films, and episodes of 1 to 4 GB.
- Four people with two years of viewing, favourites, two collections and tags. One person (`jo`) has not signed in for over 140 days, so the default inactive filter leaves them out.
- Files that are skipped with a reason: a Dolby Vision profile 5 film, an interlaced episode and a hard-linked film.
- 14 finished jobs in History (complete with backups kept or expired, one restored, one skipped for a small saving, one failed, one cancelled) and 8 jobs waiting in the Queue.

How it works: an in-process fake Jellyfin (`jellyfintest`) serves the library; the media files are sparse (the right size, but no data and no disk space); a fake prober answers from the ffprobe fixtures in `internal/media/testdata/probe`, patched to match each file; and the hardware test results are fixed (software x265 works, there is no Intel GPU). The config is seeded the way the setup wizard and `scripts/devseed` do it, with three starter policies on and Efficient encoding off. Past jobs are written through the same store methods the queue uses.

Nothing is ever encoded. The demo never runs ffmpeg or ffprobe, never starts the queue's workers, and only writes inside its folder. It marks that folder with a `.jellytrim-demo` file and refuses to use a non-empty folder without it.

| Flag | Default | What it does |
|---|---|---|
| `-listen` | `127.0.0.1:8098` | Where to serve the web UI |
| `-dir` | `./tmp/demo` | The demo's folder: `config/` for the database and `media/` for the sparse files |
| `-reset` | `false` | Delete the demo's config and media and build them again |
| `-running` | `false` | Show one job encoding in the Queue (43%). This also turns Dry Run off and sets a schedule with every hour active except Monday to Friday, 08:00 to 17:00, so the page is consistent. Otherwise Dry Run is on and the schedule is nights and weekends. |
| `-v` | `false` | Log every request |

A second start reuses the folder, so changes you make in the UI are kept; only Dry Run and the processing schedule are set again from `-running`. Use `-reset` after changing the catalogue.

To change the data, edit the tables in `scripts/demo/catalogue.go`. The demo's tests (`go test ./scripts/demo/`) fail if a job in the History or Queue tables names an item that the policies do not choose.

Item pages, History, the ffmpeg commands in job details and the path mapping settings all show the local file path, which includes the full path of `-dir`. On a Mac, even `/tmp` resolves to a per-user folder. For screenshots, run the demo in a container so every path starts with `/data`:

```sh
docker run -d --name jellytrim-demo -p 127.0.0.1:8098:8098 \
  -v "$PWD":/src:ro -v jellytrim-demo-gomod:/go/pkg/mod -v jellytrim-demo-gocache:/root/.cache/go-build \
  -w /src -e GOFLAGS=-buildvcs=false -e TZ=Europe/London \
  golang:1.26-trixie go run ./scripts/demo -dir /data -listen 0.0.0.0:8098 -running
```

`docker restart jellytrim-demo` rebuilds it after a code change, and `docker rm -f jellytrim-demo` removes it. The README screenshots in `docs/images/` were taken this way.

## Configuration

JellyTrim reads a few bootstrap settings from the environment; everything else is set in the UI and stored in SQLite. See `config.example.env`.

## Project layout

See `AGENTS.md` (Layout) and `docs/ARCHITECTURE.md`.

## Releasing

Releases are cut by tagging `vX.Y.Z` on `main`. The release workflow builds binaries with GoReleaser and multi-architecture images, and publishes them to GitHub Releases and `ghcr.io/freakyturtle/jellytrim`. See the `/release` skill for the checklist.

## Agent setup

The repository includes configuration for Claude Code and Codex. See `docs/agents/README.md`. None of it is needed to build or run JellyTrim.
