# JellyTrim

JellyTrim is a media lifecycle optimiser for Jellyfin. It reads what Jellyfin knows about each film and episode (whether it has been watched, when, whether it is a favourite, which library it is in) and uses simple policies to decide when a file can be made smaller. It then re-encodes the file with ffmpeg, checks the result, and swaps it in safely. For example: keep a new 4K film as it is, and convert it to 1080p HEVC once it has been watched and 90 days have passed.

> Keep media at the quality that makes sense now, and automatically reduce its storage cost when that quality is no longer useful.

JellyTrim is a single Go binary with a web interface. It runs in Docker next to Jellyfin.

## Status

JellyTrim has not reached version 1.0, but it is feature-complete for its MVP: connecting to Jellyfin, syncing and inspecting a library, policies with previews and explanations, safe encoding with x265 and Intel Quick Sync, a queue with history, scheduling, and the full web UI. `docs/MILESTONES.md` shows the detail. The one significant gap is that Intel Quick Sync has not yet been verified on real Intel hardware; the software encoder (x265) has. Expect breaking changes before 1.0.

**Dry Run is on by default.** Until you turn it off, JellyTrim only reports what it would do. It does not change any files. Keep your own backups of media you care about.

## Features

- **Policies that use Jellyfin data.** Watched state, last watched date, favourites, date added, library, series, collection, tag, resolution, codec and bitrate.
- **Households with several users.** Choose whose watch history counts and how many of them must have watched something (any one, a majority, everyone, or a percentage). One person's favourite is enough for the *Protect favourites* policy. Inactive accounts are left out.
- **Never swaps a file mid-film.** JellyTrim asks Jellyfin what is playing and waits until nobody is watching before it replaces a file.
- **A policy editor that reads like a sentence.** "In Movies, when watched and last watched more than 90 days ago, convert to 1080p HEVC at High quality."
- **Every decision explained.** Each item shows which policy matched and why, line by line.
- **A useful Dry Run.** Counts, planned changes and estimated savings for the whole library and for each policy, before anything changes.
- **Safe replacement.** New files are written alongside the original, checked, and swapped in only if every check passes. Originals are kept as backups for 7 days, with a Restore button.
- **Streams kept.** All audio tracks, subtitles, chapters, attachments and metadata are carried over.
- **HDR handled with care.** HDR10 and HLG keep their HDR information. HDR10+ and Dolby Vision profiles 7 and 8 are skipped unless a policy allows reducing them to HDR10. Dolby Vision profile 5 and anything unclear are always skipped, with the reason shown. JellyTrim never converts HDR to SDR.
- **Hardware detection by test encode.** JellyTrim checks which encoders actually work on your machine, not just which ones ffmpeg lists.
- **Queue, history and scheduling.** Progress, speed and time left; full history with technical details; a weekly processing schedule in hour blocks, so encoding only uses the GPU or CPU when you choose.
- **Simple to run.** One container for amd64 and arm64. SQLite database. No external services. No telemetry.

## How it keeps your media safe

- Dry Run is on until you turn it off.
- The original file is never opened for writing. The new file is written as a hidden file in the same folder.
- The new file is checked before it replaces anything: codec, resolution, every audio and subtitle stream, duration, HDR information, and a full decode.
- A file is replaced only if it saves at least 10% (you can change this).
- The original is kept as a hard-linked backup for 7 days, and can be restored from History.
- Every file operation is recorded before it happens, so JellyTrim can recover cleanly after a crash or power cut.
- When JellyTrim is unsure about a file, it skips it and says why. Symlinks, hard-linked files, interlaced video and unusual HDR are always skipped.

The details are in [docs/TRANSCODING.md](docs/TRANSCODING.md).

## Quick start

You need Docker with Compose, a running Jellyfin server (10.10 or later), and a Jellyfin API key (in Jellyfin: Dashboard, then API Keys).

Create a `docker-compose.yml`:

```yaml
services:
  jellytrim:
    image: ghcr.io/freakyturtle/jellytrim:latest
    container_name: jellytrim
    restart: unless-stopped
    # Run as the user that owns your media files, so JellyTrim can replace them.
    user: "1000:1000"
    ports:
      # This machine only. To reach it from your LAN, use "8080:8080" and
      # read SECURITY.md first: JellyTrim has no login.
      - "127.0.0.1:8080:8080"
    volumes:
      - ./config:/config
      # Your media, mounted read-write. Use the same path Jellyfin uses if you can.
      - /path/to/media:/media
    # Intel Quick Sync (QSV) only. Remove these lines for software encoding.
    devices:
      - /dev/dri:/dev/dri
    group_add:
      # The group ID that owns /dev/dri/renderD128 on the host.
      # Find it with: stat -c %g /dev/dri/renderD128
      - "993"
```

Start it:

```sh
docker compose up -d
```

Open `http://<your-server>:8080`. The setup wizard asks for your Jellyfin address (for example `http://jellyfin:8096`) and API key, lets you choose libraries and check path mappings, tests your hardware, and runs a first Dry Run scan.

If Jellyfin runs in another Compose project, put both containers on the same Docker network so JellyTrim can reach it by name.

## Configuration

Most settings are made in the web interface and stored in the SQLite database in `/config`. These environment variables are read at start-up:

| Variable | Default | What it does |
|---|---|---|
| `JELLYTRIM_LISTEN` | `:8080` | Address and port to listen on |
| `JELLYTRIM_CONFIG_DIR` | `/config` | Folder for the database and state |
| `JELLYTRIM_JELLYFIN_URL` | (none) | Optional. Sets the Jellyfin address and skips that step of setup |
| `JELLYTRIM_JELLYFIN_API_KEY` | (none) | Optional. Sets the Jellyfin API key |
| `JELLYTRIM_JELLYFIN_API_KEY_FILE` | (none) | Optional. Reads the API key from a file, for Docker secrets |
| `JELLYTRIM_FFMPEG` | set in the image | Path to `ffmpeg` |
| `JELLYTRIM_FFPROBE` | set in the image | Path to `ffprobe` |
| `JELLYTRIM_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `JELLYTRIM_LOG_FORMAT` | `text` | `text` or `json` |
| `TZ` | `UTC` | Time zone for the processing schedule and daily sync time, for example `Europe/London` |

Values set by environment variables are shown as read-only in Settings.

## Path mapping

Jellyfin and JellyTrim may see the same files under different paths. For example, a film might be at `/media/movies/Example (2019)/Example.mkv` in the Jellyfin container and at `/mnt/media/movies/Example (2019)/Example.mkv` in the JellyTrim container.

A path mapping tells JellyTrim how to translate: Jellyfin path `/media/movies` is JellyTrim path `/mnt/media/movies`. You can add several mappings. The longest matching prefix wins.

The setup wizard fills in each library's folders from Jellyfin, then checks as you type: how many files it can find, and whether it can write, hard-link and rename in that folder. The simplest set-up is to mount your media at the same path in both containers, so no translation is needed.

## Security

JellyTrim has **no built-in login**. Anyone who can reach its web interface can change policies and turn off Dry Run. Run it on a private network, or put it behind a reverse proxy that adds authentication.

- JellyTrim rejects cross-origin requests that change state, so another website cannot act through your browser.
- The Jellyfin API key is stored in `/config`, is never shown in the interface and is never logged. Artwork is fetched by JellyTrim, so your browser never sees the key.
- Use a dedicated Jellyfin API key for JellyTrim.

See [SECURITY.md](SECURITY.md) for the threat model and how to report a vulnerability.

## Hardware support

| Encoder | Platforms | Status |
|---|---|---|
| x265 (software HEVC) | amd64, arm64 | Supported and verified on real hardware |
| Intel Quick Sync (QSV) HEVC | amd64 with `/dev/dri` passed through | Built and covered by golden argument tests; not yet verified on real Intel hardware |
| NVIDIA NVENC | | Planned |
| VAAPI (AMD and Intel) | | Planned |
| AV1 (SVT-AV1 and hardware) | | Planned |

The image includes jellyfin-ffmpeg, the same ffmpeg build Jellyfin uses. **Auto** picks the best working encoder for each job, and falls back to x265 when a hardware encoder cannot keep HDR information.

## Building from source

You need Go 1.26 or later, [Task](https://taskfile.dev), and ffmpeg with libx265 on your `PATH`.

```sh
git clone https://github.com/freakyturtle/jellytrim.git
cd jellytrim
task setup
task build        # writes bin/jellytrim
task check        # everything CI runs
```

Or install the binary directly:

```sh
go install github.com/freakyturtle/jellytrim/cmd/jellytrim@latest
```

[CONTRIBUTING.md](CONTRIBUTING.md) covers the development set-up, including a throwaway Jellyfin with generated test media.

## Documentation

- [Product specification](docs/PRODUCT.md): what JellyTrim is for and what it does.
- [Architecture](docs/ARCHITECTURE.md): how the code fits together.
- [Transcoding](docs/TRANSCODING.md): media decisions, encoder settings and validation.
- [Policies](docs/POLICIES.md): the policy model, conditions and precedence.
- [UI](docs/UI.md): the design system and page specifications.
- [Development](docs/DEVELOPMENT.md) and [testing](docs/TESTING.md).
- [Milestones](docs/MILESTONES.md): the plan and progress.
- [Decision records](docs/adr/).
- [AI agent set-up](docs/agents/README.md).

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first, and follow the [code of conduct](CODE_OF_CONDUCT.md). For anything large, open an issue to discuss it before you start.

## Licence

MIT. See [LICENSE](LICENSE).

## No telemetry

JellyTrim does not collect usage data, does not call home, and does not need an account. It talks only to your Jellyfin server.

## Acknowledgements

JellyTrim builds on:

- [Jellyfin](https://jellyfin.org), the free media server it works with.
- [FFmpeg](https://ffmpeg.org), which does the encoding and inspection.
- [jellyfin-ffmpeg](https://github.com/jellyfin/jellyfin-ffmpeg), the ffmpeg build in the image.
- [templ](https://templ.guide) and [htmx](https://htmx.org) for the web interface.
- [IBM Plex](https://github.com/IBM/plex), the typeface, under the SIL Open Font Licence.
