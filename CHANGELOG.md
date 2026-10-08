# Changelog

All notable changes to JellyTrim are recorded in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before version 1.0, a minor release may include breaking changes.

## [Unreleased]

### Fixed

- Every Intel Quick Sync encode failed with "ffmpeg reported errors while encoding". The VA-API library prints start-up messages ("libva info: ...") that JellyTrim counted as errors. It now tells the library to print errors only, and ignores those messages if they still appear. Real errors still reject the new file.

## [0.1.1] - 2026-10-07

### Added

- A quiet "Support JellyTrim" link at the bottom of the menu (and in the phone menu's More list) that opens Ko-fi in a new tab. It is a plain link: nothing is loaded from Ko-fi, and JellyTrim never asks for support anywhere else.

## [0.1.0] - 2026-10-07

The first public release.

### Added

- Application shell, design system and Docker image with a dev stack.
- Jellyfin connection, library sync (movies and episodes, watched state, favourites, tags, genres, collections), and path mapping with a live check.
- Media inspection with ffprobe, HDR classification (SDR, HDR10, HLG, HDR10+, Dolby Vision) and probe caching keyed on file identity.
- Library and item detail pages: filters, streams, and every decision explained line by line.
- A policy engine with scope, conditions and precedence, an editor, a live preview, and starter policies created by the setup wizard.
- Safe encoding with x265 and Intel Quick Sync: hardware probed by test encode, a journalled pipeline (encode to a partial file, validate, hard-link backup, atomic replace), and crash recovery.
- A queue with progress, pause, resume, cancel and retry, a scheduler for periodic sync and re-evaluation, and a weekly processing schedule in hour blocks (Monday to Sunday) that stops running encodes when an hour ends.
- History with technical details, and Restore while a backup exists.
- Watch history from several Jellyfin users: count everyone (including users added later) or chosen users, require any one, a majority, everyone or a set percentage to have watched, and leave accounts inactive for more than a set number of days (90 by default) out of that share. Everyone's favourites still count.
- A playback check: JellyTrim does not start encoding a file someone is playing, waits (up to 6 hours) before replacing a file that is playing, and Restore refuses while a file is playing.
- Settings for every configurable behaviour, Dry Run (on by default), and the setup wizard.
- Large libraries: tested with 100,000 items. A full re-evaluation takes about 3 seconds, policy previews under a second, and the queue orders jobs by the biggest estimated saving.
- A user guide for running JellyTrim with Docker (`docs/guide/`): install, setup wizard, settings, policy recipes, reverse proxy logins, backups and restore, upgrading and troubleshooting.
- `scripts/devseed` for seeding a local config against the dev Jellyfin without repeating the wizard by hand.
- A demo mode (`task demo`) that serves the real interface on an invented library, used for the screenshots.

### Security

- No built-in authentication; cross-origin protection on state-changing requests (see `SECURITY.md`).
- The Jellyfin API key is stored with file mode 0600, never rendered, logged or included in diagnostics; artwork is proxied through `/img/{id}` so the browser never sees it.
- ffmpeg and ffprobe run with explicit argument lists, never a shell; paths are `file:`-prefixed and confined to configured local roots.

[Unreleased]: https://github.com/freakyturtle/jellytrim/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/freakyturtle/jellytrim/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/freakyturtle/jellytrim/releases/tag/v0.1.0
