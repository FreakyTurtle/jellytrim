# Architecture

JellyTrim is one Go binary. It serves a server-rendered web UI, keeps its state in one SQLite file in `/config`, talks to Jellyfin over HTTP, and runs `ffprobe` and `ffmpeg` as child processes. There are no other services.

```
            ┌──────────────── JellyTrim (one process) ────────────────┐
 browser ──►│ web (templ + HTMX)                                      │
            │   │                                                     │
            │   ▼                                                     │
            │ services: library · policy/plan · queue · settings      │
            │   │            │               │                        │
            │   ▼            ▼               ▼                        │
            │ jellyfin    store (SQLite)   pipeline ─► encoder        │
            │   │                            │           │            │
            └───┼────────────────────────────┼───────────┼────────────┘
                ▼                            ▼           ▼
            Jellyfin API             media files     ffmpeg / ffprobe
```

## Principles

- **Pure domain core.** `media`, `pathmap`, `policy` and `plan` take data and return decisions. They do no I/O, so they are fast to test and deterministic.
- **One place for each side effect.** Processes: `internal/ffmpeg`. Database: `internal/store`. Jellyfin: `internal/jellyfin`. Media files: `internal/pipeline`.
- **Thin handlers.** `internal/web` parses requests, calls services and renders views.
- **Standard library first.** Dependencies: templ, `modernc.org/sqlite`. Nothing else at runtime.

## Packages

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/jellytrim` | Entry point: flags (`--version`, `--config-dir`, `--listen`), the `healthcheck` subcommand used by the image's HEALTHCHECK, signal handling | `app`, `config` |
| `internal/config` | Bootstrap settings from environment variables and `_FILE` secrets | stdlib |
| `internal/app` | Builds every service, starts workers and the HTTP server, shuts down in order | all |
| `internal/store` | SQLite access, embedded migrations, repositories | `modernc.org/sqlite` |
| `internal/jellyfin` | API client and types; `jellyfintest` fake server for tests | stdlib |
| `internal/pathmap` | Maps paths between Jellyfin and JellyTrim; root checks | stdlib (pure) |
| `internal/media` | Media model (file, streams, colour, HDR class) and ffprobe JSON parsing | stdlib (pure) |
| `internal/ffmpeg` | Runs ffprobe and ffmpeg; progress parsing; version detection | stdlib |
| `internal/encoder` | Encoder backends (x265, QSV), quality maps, argument building, hardware probe, Auto selection | `media`, `ffmpeg` |
| `internal/policy` | Policy model, scope and condition evaluation, explanations | `media` (pure) |
| `internal/plan` | Turns a matched policy and a media file into an encode plan or a skip with reasons; size estimates | `media`, `policy` (pure) |
| `internal/pipeline` | Safe execution: encode to a partial file, validate, back up, replace, notify; journal and recovery | `encoder`, `ffmpeg`, `media`, `store`, `jellyfin` |
| `internal/queue` | Job lifecycle and the worker pool | `pipeline`, `store` |
| `internal/library` | Sync from Jellyfin, probe caching, evaluation of every item, Dry Run summary | `jellyfin`, `pathmap`, `ffmpeg`, `media`, `policy`, `plan`, `store` |
| `internal/scheduler` | Periodic sync and re-evaluation; processing window; auto-enqueue | `library`, `queue` |
| `internal/web` | HTTP handlers, templ views, static files, image proxy | services |

`internal/testutil` holds shared test helpers (fixture loading, `RequireFFmpeg`).

## Data flow

### Sync (every 6 hours by default, or on demand)

1. `library.Sync` records the attempt in `sync_runs` first, so every attempt, including one that fails at once (Jellyfin down, nothing selected), is recorded and the scheduler backs off. It then lists the libraries and users from Jellyfin (`/Library/VirtualFolders`, `/Users`). An empty answer is refused while libraries or users are stored, so a faulty reply cannot cascade-delete the library.
2. For each managed library it pages through movies and episodes (`/Items`) without a user ID: the API key is an administrator's, so the list does not depend on one user's library access. A managed library that comes back empty while items are stored for it fails the sync, so nothing is swept.
3. For each selected user it pages through user data only. Collections are read as each selected user sees them and merged; a box set's members are stored as reported, which may be a series or season rather than episodes.
4. It maps each Jellyfin path to a local path (`pathmap`) and checks it is inside a configured root.
5. It writes items, user data, tags and collection membership to the store. Items not seen in a **complete** sync are removed afterwards (mark and sweep).
6. For each item whose file identity (device, inode, size, mtime) changed since the last probe, it runs ffprobe (two passes: streams and format, then the first frame's side data) and stores the raw JSON.
7. It evaluates every item (below) and stores the result.

### Evaluation (after each sync, and whenever a policy or setting changes)

1. Build an `policy.Item` snapshot from the store: Jellyfin metadata, user data, parsed `media.File`. An episode's collections are those holding the episode, its series or its season. One gate, shared by evaluation, the policy preview and the pre-job reassessment, decides first: excluded items are protected, items the sync marked unmanageable are skipped, uninspected items stay pending, and an item whose facts could not be checked (for example the optimised-file lookup) is skipped.
2. `policy.Evaluate(policies, item, now)` returns the winning policy, all matching policies, and an explanation for each.
3. `plan.Decide(item, action, settings, capabilities)` returns an `Optimise` plan with an estimated size range, or `AlreadyOptimal`, `Protected`, `Skipped{reasons}` or `NoPolicy`.
4. The result is stored in `evaluations` and drives the Library filters, the Dry Run summary and the dashboard.
5. If Dry Run is off, the policy is enabled and auto-processing is on, the scheduler enqueues `Optimise` results during the processing window.

### A job

```
Waiting ─► Analysing ─► Encoding ─► Validating ─► Replacing ─► Complete
   │           │            │            │             │
   └──────► Skipped      Failed       Failed        Failed (original restored)
```

1. **Analysing:** re-probe the source, check identity against the job, re-run the plan (the file may have changed), check free space, take the path lock.
2. **Encoding:** `encoder` builds arguments from the plan; `ffmpeg` runs them, writing `.<name>.jellytrim-<job>.partial` in the source directory. Progress, speed and ETA are stored. Early abort if the projected size is too large.
3. **Validating:** probe the output and compare it with the plan and the source; decode check; size rule.
4. **Replacing:** set mode and owner, fsync, re-check source identity, hard-link a backup, rename the partial over the original, fsync the directory.
5. **Complete:** record the saving and the `optimised` identity, notify Jellyfin, poll until it reflects the change.

See `docs/TRANSCODING.md` for the rules at each step.

## Data model (SQLite)

Times are Unix seconds (`INTEGER`, UTC) unless named `_ns`. JSON columns are `TEXT`.

| Table | Purpose | Key columns |
|---|---|---|
| `schema_migrations` | Applied migrations | `version`, `applied_at` |
| `settings` | Key/value settings, including the Jellyfin URL and API key | `key`, `value` |
| `path_mappings` | Jellyfin prefix to local prefix | `id`, `jellyfin_prefix`, `local_prefix`, `position` |
| `libraries` | Jellyfin libraries and whether JellyTrim manages them | `id` (Jellyfin ItemId), `name`, `collection_type`, `locations` (JSON), `managed` |
| `jellyfin_users` | Users and whether their watch state counts | `id`, `name`, `disabled`, `selected` |
| `items` | Movies and episodes from Jellyfin | `id`, `library_id`, `type`, `name`, `series_id`, `series_name`, `season_id`, `season_name`, `season_number`, `episode_number`, `year`, `jellyfin_path`, `local_path`, `date_added`, `runtime_ticks`, `tags`, `genres`, `sync_skip_reason`, `seen_sync_id` |
| `item_user_data` | Watch state per user | `item_id`, `user_id`, `played`, `play_count`, `favorite`, `last_played_at` |
| `collections`, `collection_items` | Jellyfin collections and membership. `collection_items.item_id` is a movie, episode, series or season ID as Jellyfin reports it, so it has no foreign key to `items` | `collection_id`, `item_id` |
| `probes` | Cached ffprobe output per item | `item_id`, `dev`, `inode`, `size`, `mtime_ns`, `nlink`, `is_symlink`, `probe_json`, `frame_json`, `error`, `probed_at` |
| `policies` | User policies | `id`, `name`, `enabled`, `priority`, `scope`, `conditions`, `action` (JSON) |
| `evaluations` | Latest decision per item | `item_id`, `outcome`, `policy_id`, `explanation`, `plan`, `reasons`, `est_min_bytes`, `est_max_bytes`, `evaluated_at` |
| `jobs` | Queue and history. At most one active (waiting to replacing) job per item, enforced by the partial unique index `jobs_one_active`. Status `attention` means the job stopped in a state the user must check: not active, but it blocks new jobs for its item and file | `id`, `item_id`, `policy_id`, `status`, `plan`, `source_identity`, `output_identity`, `progress`, `speed`, `eta_seconds`, `encoder`, `source_size`, `output_size`, `summary`, `diagnostics`, `backup_path`, `backup_expires_at`, `restored_at`, timestamps |
| `journal` | Intent journal of filesystem steps | `id`, `job_id`, `step`, `path`, `created_at`, `completed_at` |
| `optimised` | Files JellyTrim produced (loop guard) | `dev`, `inode`, `size`, `mtime_ns`, `item_id`, `job_id` |
| `capabilities` | Hardware probe results | `backend`, `available`, `detail` (JSON), `tested_at` |
| `sync_runs` | Sync history | `id`, `started_at`, `finished_at`, `status`, `items_seen`, `error` |

The database is `/config/jellytrim.db`, created with mode 0600. Migrations are in `internal/store/migrations/`, embedded with `embed.FS`, applied at start-up.

## Concurrency

- One HTTP server goroutine pool (stdlib).
- One sync at a time (a mutex in `library`).
- A worker pool in `queue` with configurable concurrency (default 1). A per-path lock stops two jobs, or a sync and a replace, from working on the same file.
- SQLite: one `*sql.DB`; writes serialise through SQLite's lock with a busy timeout. Transactions begin `IMMEDIATE` (`_txlock=immediate`), so a transaction that reads then writes waits for the lock instead of failing with `SQLITE_BUSY`. `synchronous=FULL`, so a journal row is on disk before the step it describes. Long operations (ffmpeg) never hold a transaction.
- A database written by a newer JellyTrim (a schema version above the newest embedded migration) is refused at start with a clear error.
- Every goroutine takes a `context.Context` from `app` and stops on shutdown.

## Shutdown

On SIGTERM or SIGINT, or when the HTTP server fails, `app` cancels the run context and then:
1. shuts down the HTTP server with a 10 s timeout, so no request starts new work;
2. waits for the scheduler and the start-up hardware test;
3. waits for the queue: running encodes are cancelled (ffmpeg receives SIGTERM, then SIGKILL after 10 s), the partial files recorded in the journal are removed and the jobs go back to Waiting;
4. waits for background library runs; `library` refuses to start new ones once the context is done;
5. closes the database.

A job interrupted during Replacing is never cancelled mid-step: the replace sequence is short and finishes first.

## Recovery on start

`pipeline.Recover` reads unfinished journal entries and acts only on the recorded paths:
- partial file recorded, not yet renamed: delete the partial; job back to Waiting;
- backup recorded, original missing: rename the backup back to the original; job Failed with "Interrupted during replacement; original restored";
- rename recorded as complete: finish the job (Complete) and notify Jellyfin.

## Security

- No built-in authentication (ADR 0008). `http.CrossOriginProtection` guards state-changing requests.
- The Jellyfin API key is stored in `settings`, never rendered, never logged. Artwork is proxied through `/img/{id}` so the browser never talks to Jellyfin.
- Metadata and file names are untrusted: HTML is escaped by templ, SQL uses placeholders, ffmpeg arguments are slices with `file:` paths.
- File operations are limited to the configured local roots after resolving symlinks.

## Designed for later

- **More encoders** (NVENC, VAAPI, SVT-AV1, VideoToolbox): add a `encoder.Backend`; selection and probing are generic.
- **VMAF-guided quality**: `encoder.Sample` already encodes short segments for size estimates. A later `plan` step can encode samples at several quality values, measure VMAF with ffmpeg's `libvmaf`, and choose the lowest size that meets a threshold.
- **Other media servers**: `library` depends on a small interface of what it needs from Jellyfin, so a Plex or Emby adapter could implement it.
- **Webhooks**: a Jellyfin webhook endpoint can trigger a targeted sync instead of polling.
