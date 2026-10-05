# Contributing

**JellyTrim is open source, but it is not open to contributions.** It is maintained by one person in their spare time, as a tool they use themselves. To keep it that way:

- **Pull requests are not accepted.** The repository only lets its owner open them, and any that get through are closed automatically with a short note. This is not a judgement of the change.
- **There is no issue tracker, discussion forum or support channel.** The [user guide](docs/guide/README.md) and its [troubleshooting page](docs/guide/troubleshooting.md) are the help that exists.
- **Security problems are the exception.** Please report them privately, as [SECURITY.md](SECURITY.md) describes.

You are welcome to fork it. The [MIT licence](LICENSE) lets you use, change and share your own version, as long as you keep the copyright and licence notice. The rest of this page is for anyone working on the code, including in a fork.

JellyTrim changes people's media files. The most important rule is the first product principle: never destroy a user's media because JellyTrim was unsure. Read [docs/PRODUCT.md](docs/PRODUCT.md) for what JellyTrim is for, and what is deliberately out of scope.

## Prerequisites

- **Go 1.26 or later.**
- **[Task](https://taskfile.dev)** to run the project commands.
- **ffmpeg and ffprobe 7 or later, built with libx265.** Needed for tests and fixtures. Tests that need ffmpeg are skipped without it; CI runs them all.
- **Docker with Compose**, for the dev stack (a throwaway Jellyfin).
- **golangci-lint**, for `task lint`.
- **Python 3**, for `task ai:check`.

templ is pinned in `go.mod` as a Go tool, so you do not need to install it.

## Set up

```sh
git clone https://github.com/freakyturtle/jellytrim.git
cd jellytrim
task setup
```

`task setup` downloads the Go modules and turns on the committed pre-commit hook (`.githooks/pre-commit`), which runs `scripts/check-public.sh`.

## Development loop

| Command | What it does |
|---|---|
| `task dev` | Runs JellyTrim locally with templ in watch mode. State goes in `./tmp/config`. |
| `task dev:fixtures` | Generates synthetic test media into `dev/media` and ffprobe JSON into testdata. |
| `task dev:stack` | Starts a Jellyfin 12 container and a JellyTrim container on the fixtures. |
| `task dev:bootstrap` | Sets up the dev Jellyfin: users `dev`, `alex`, `sam` and `robin` (each password is the name) with different watched and favourite states, libraries, and an API key written to `dev/jellyfin-api-key`. |

A typical first run:

```sh
task dev:fixtures
task dev:stack
task dev:bootstrap
```

The dev stack mounts the fixtures at `/media` in Jellyfin and at `/mnt/media` in JellyTrim, so path mapping is exercised for real. Everything under `dev/` is gitignored and can be deleted at any time.

Work only on the generated fixtures or on copies in a temporary directory. Never point a development build at a real media library.

## Checks

```sh
task check
```

This runs everything CI runs:

- `go tool templ generate`, then fails if the generated files changed
- `go vet`
- golangci-lint
- `go test -race ./...`, reporting how many tests were skipped
- `task ai:check` (the Codex copies of the agent files are up to date)
- `scripts/check-public.sh --all`

A skipped test is not a passing test. If your change adds tests that need ffmpeg, make sure they ran.

## Conventions

- **Commits** use [Conventional Commits](https://www.conventionalcommits.org): `feat(policy): add tag condition`, `fix(pipeline): ...`, `docs: ...`, `test: ...`, `chore: ...`, `ci: ...`.
- **Tests** come with every change. Bug fixes start with a failing test. Tests that touch files use `t.TempDir()`.
- **Standard library first.** Do not add a dependency without a clear reason, and say why in the pull request.
- **No Node.** No npm, no frontend build step, no CSS framework. The UI is templ, htmx and hand-written CSS.
- **Package boundaries.** The domain packages (`media`, `pathmap`, `policy`, `plan`) do no I/O. Only `internal/ffmpeg` runs processes. Handlers stay thin.
- **Writing style.** Plain British English in code comments, docs and UI text. Short sentences, no hype, no emojis, no em dashes.
- **Area rules.** `.claude/rules/` has short rules for each part of the code (Go, store, UI, media safety, Jellyfin, policies, tests, release, docs). They apply to people as well as AI agents.

### Media safety

Any change to `internal/ffmpeg`, `internal/encoder`, `internal/plan`, `internal/pipeline` or `internal/queue` must keep these true:

- The original file is never modified before the new file passes validation.
- Every audio, subtitle and attachment stream is kept, or its removal is recorded with a reason.
- Every filesystem step is recorded in the journal before it happens.
- ffmpeg arguments are a list of strings, never a shell command.
- Tests assert that the original file is unchanged (by hash) after every failure case.

The full rules are in `.claude/rules/media-safety.md` and [docs/TRANSCODING.md](docs/TRANSCODING.md).

### Generated files are committed

The `*_templ.go` files are generated by `task generate` and committed, so that `go install ...@latest` works without templ. After editing a `.templ` file, run `task generate` and commit both files. CI fails if they differ. Do not edit generated files by hand.

## Adding an encoder

The `/encoder-profile` skill walks through this. In short:

1. Implement the encoder backend interface in `internal/encoder`.
2. Map each quality tier (Maximum, High, Balanced, Space Saver) to the encoder's own setting, and document the values and how you chose them in `docs/TRANSCODING.md`.
3. Add a hardware probe that runs a short test encode with exactly the arguments the encoder will use, including an HDR metadata check.
4. Add golden tests for the generated ffmpeg arguments.
5. If you tested on real hardware, say which hardware and driver in the pull request. If you could not, say that too.
6. Update the hardware table in `README.md`.

## Never commit media or personal data

The repository is public. Do not commit:

- media files, databases or anything from `dev/`;
- real API keys or tokens (tests use `deadbeefdeadbeefdeadbeefdeadbeef`);
- personal paths, hostnames, IP addresses or email addresses;
- files over 1 MB, apart from the vendored fonts.

`scripts/check-public.sh` runs in the pre-commit hook and in CI. Run `scripts/check-public.sh --all` to check the whole tree. You can also list your own private strings (a server name, your username) in a gitignored `.public-denylist` file, one per line, and the check will block them.

Set your commit email to a noreply address if you do not want your personal email to be public.

## AI coding agents

JellyTrim has a set-up for Claude Code and Codex: `AGENTS.md` is the shared guide, and [docs/agents/README.md](docs/agents/README.md) explains the rest.
