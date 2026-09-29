# Settings reference

Most of JellyTrim's configuration lives on the **Settings** page and is stored in the SQLite database in `/config`, not in environment variables. This page lists every setting, grouped as Settings groups them, with its default, its allowed range and what it does.

A Jellyfin connection preset by `JELLYTRIM_JELLYFIN_URL` and `JELLYTRIM_JELLYFIN_API_KEY` (or `_FILE`) only fills in setup, and only while nothing is saved yet. The fields on this page are always editable: once you save a connection here, this page is the source of truth, and changing the environment variable afterwards has no effect.

![Settings page](../images/settings.webp)

## Jellyfin

| Setting | Default | What it does |
|---|---|---|
| Address | none | Jellyfin's URL as seen from inside the JellyTrim container, for example `http://jellyfin:8096`. Preset by `JELLYTRIM_JELLYFIN_URL`. |
| API key | none | Tested when saved. Never shown once saved, only whether one is set. Preset by `JELLYTRIM_JELLYFIN_API_KEY` or `JELLYTRIM_JELLYFIN_API_KEY_FILE`. |

## Libraries

Which Jellyfin libraries JellyTrim may manage. Only Movies and TV Shows libraries are offered. A library must be ticked here before any policy can apply to it.

## Watched state

These three settings apply to every policy that checks watched state or favourites.

| Setting | Default | Range | What it does |
|---|---|---|---|
| Everyone, including people added later / Only these people: | Everyone, including people added later | one of the two | The first counts every enabled Jellyfin user, including ones added later. The second counts only the ones ticked. |
| A file counts as watched when | Any one | Any one, A majority, Everyone, or Custom (1 to 100%) | The share of counted users that must have played an item for it to count as watched. |
| Ignore accounts not used for more than N days | 90 days | 0 to 3650 (0 counts every account) | A user with no Jellyfin activity for more than this many days is left out of the watched share. Their favourites and last-watched date still count. If this would leave nobody counted, the filter is not applied. |

Favourites and last-watched date always use the most cautious reading: any counted or inactive user's favourite protects the item, and the most recent play by any of them resets the last-watched clock, whatever the share rule is set to. Full worked examples are in [docs/POLICIES.md](../POLICIES.md#whose-watch-state-counts).

## Paths

The path mappings between Jellyfin's view of your files and JellyTrim's. See [First run](first-run.md#4-path-mappings) for worked examples, and use **Check paths** to test a mapping before saving it.

## Dry Run

| Setting | Default | What it does |
|---|---|---|
| Dry Run | On | While on, JellyTrim evaluates everything and reports what it would do, but changes no files. Turning it off needs a second confirmation. |

## Sync

| Setting | Default | Range | What it does |
|---|---|---|---|
| Every (hours) | 6 hours | 1 to 168 hours | How often JellyTrim asks Jellyfin for library changes, on top of syncing on demand. |
| Or daily at | none | a 24-hour time, for example `03:30` | If set, this replaces the interval: sync happens once at this time every day (in the `TZ` time zone), catching up on the most recent missed slot if JellyTrim was stopped. Leave empty to use the interval instead. |

A file is only re-probed with ffprobe when its size or modification time has changed since the last probe, so a sync itself is cheap even on a large library.

## Processing schedule

A weekly grid of hour blocks, Monday to Sunday, each on or off, in the `TZ` time zone. Encoding only runs in the hours you tick; the queue still accepts and orders jobs at any time, and Dry Run evaluation is not affected. Presets fill the grid in one click: **Any time**, **Nights (01:00 to 07:00)**, **Nights, and all weekend**, and **Outside 17:00 to 23:00**.

When an active hour ends, a running encode stops. The original file is never touched: JellyTrim deletes its partial file and the job goes back to Waiting until the next active hour. The default is every hour active. An empty grid means never: JellyTrim warns "No hours are active, so nothing will be encoded."

## Processing

| Setting | Default | Range | What it does |
|---|---|---|---|
| Queue matching items automatically | On | On or off | Whether the queue picks up new jobs on its own. Off pauses automatic processing; "Optimise now" on an item still queues it. |
| Check each new file with | Full decode check | Full decode check or Sampled (faster) | Full decodes the whole new file before it replaces the original, the safest choice. Sampled decodes several short parts of it, faster but less thorough. |
| Jobs at a time | 1 | 1 to 4 | How many encodes can run at once. Raise this only if your CPU or GPU has spare capacity; each concurrent software encode competes for the same cores. |
| Encoder | Auto | Auto or Software only | Whether JellyTrim uses a hardware encoder that passed the test (falling back to software otherwise), or software (x265) only. |

## Saving and backups

| Setting | Default | Range | What it does |
|---|---|---|---|
| Minimum saving | 10% | 0 to 90 | A file is only replaced if the encode saves at least this share of its size. Below this, the result is "already optimal" and nothing changes. |
| Keep originals for (days) | 7 | 0 to 90 | How long the original is kept as a hard-linked backup after replacement, with a Restore button in History. 0 has the backup deleted at the next hourly clean-up, which runs whether or not Jellyfin has picked up the change. See [Backups and restore](backups-and-restore.md). |

## Advanced

| Setting | Default | Range | What it does |
|---|---|---|---|
| x265 speed preset | slow | medium, slow, slower | ffmpeg's speed/efficiency trade-off for software encoding. Slower presets take longer but produce a smaller file at the same quality. |
| Output bit depth | 10-bit | 10-bit, or match source | JellyTrim outputs 10-bit HEVC by default (always 10-bit for HDR); "match source" keeps 8-bit sources at 8-bit. |
| Quality overrides | none (uses the built-in defaults) | 1 to 51, per backend and tier, or empty for the default | Overrides the CRF (x265) or ICQ `global_quality` (Intel Quick Sync) number JellyTrim uses for a quality tier. Leave a box empty to use the built-in default shown next to it. |

The built-in quality defaults, before any override:

| Quality | x265 CRF | QSV HEVC `global_quality` (ICQ) |
|---|---|---|
| Maximum | 18 | 19 |
| High | 21 | 22 |
| Balanced | 24 | 25 |
| Space Saver | 27 | 28 |

Lower x265 CRF means higher quality and a larger file; QSV's ICQ scale behaves differently, so its numbers are not directly comparable to x265's. See [docs/TRANSCODING.md](../TRANSCODING.md#quality-maps) for how these are used.

## Environment variables

These are read once, at start-up. Most take effect only through the config directory and listen address; `JELLYTRIM_JELLYFIN_URL` and `JELLYTRIM_JELLYFIN_API_KEY` (or `_FILE`) additionally preset the Jellyfin connection in Settings, but only while nothing has been saved there yet, and only when both the address and the key are set does setup skip that step. Everything else is configured only in the web UI.

| Variable | Default | What it does |
|---|---|---|
| `JELLYTRIM_LISTEN` | `:8080` | Address and port to listen on. |
| `JELLYTRIM_CONFIG_DIR` | `/config` | Folder for the database and state. Must be on local disk (see [Install](install.md#4-the-config-volume)). |
| `JELLYTRIM_JELLYFIN_URL` | none | Presets the Jellyfin address in Settings, while nothing is saved there yet. Setup skips the connect step only when the API key is also set this way. |
| `JELLYTRIM_JELLYFIN_API_KEY` | none | Presets the Jellyfin API key, while nothing is saved yet. Setting this and `_FILE` together is an error. |
| `JELLYTRIM_JELLYFIN_API_KEY_FILE` | none | Reads the API key from a file, for a Docker secret. |
| `JELLYTRIM_FFMPEG` | set in the image | Path to the `ffmpeg` binary. |
| `JELLYTRIM_FFPROBE` | set in the image | Path to the `ffprobe` binary. |
| `JELLYTRIM_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `JELLYTRIM_LOG_FORMAT` | `text` | `text` or `json`. |
| `TZ` | `UTC` | Time zone for the processing schedule and the daily sync time. |

`config.example.env` has a copy-pasteable version of this table for your compose file.
