# Changelog

All notable changes to JellyTrim are recorded in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before version 1.0, a minor release may include breaking changes.

## [Unreleased]

### Added

- Application shell, design system and Docker image with a dev stack.
- Jellyfin connection, library sync (movies and episodes, watched state, favourites, tags, genres, collections), and path mapping with a live check.
- Media inspection with ffprobe, HDR classification (SDR, HDR10, HLG, HDR10+, Dolby Vision) and probe caching keyed on file identity.
- Library and item detail pages: filters, streams, and every decision explained line by line.
- A policy engine with scope, conditions and precedence, an editor, a live preview, and starter policies created by the setup wizard.
- Safe encoding with x265 and Intel Quick Sync: hardware probed by test encode, a journalled pipeline (encode to a partial file, validate, hard-link backup, atomic replace), and crash recovery.
- A queue with progress, pause, resume, cancel and retry, a scheduler for periodic sync and re-evaluation, and a weekly processing schedule in hour blocks (Monday to Sunday) that stops running encodes when an hour ends.
- History with technical details, and Restore while a backup exists.
- Settings for every configurable behaviour, Dry Run (on by default), and the setup wizard.
- `scripts/devseed` for seeding a local config against the dev Jellyfin without repeating the wizard by hand.

### Security

- No built-in authentication; cross-origin protection on state-changing requests (see `SECURITY.md`).
- The Jellyfin API key is stored with file mode 0600, never rendered, logged or included in diagnostics; artwork is proxied through `/img/{id}` so the browser never sees it.
- ffmpeg and ffprobe run with explicit argument lists, never a shell; paths are `file:`-prefixed and confined to configured local roots.

[Unreleased]: https://github.com/freakyturtle/jellytrim/commits/main
