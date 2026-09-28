# devops memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-28: never open the dev stack's SQLite database (dev/config/jellytrim.db) from macOS (sqlite3 CLI, go run on the host) while or before the container uses it through the bind mount. WAL shared memory does not work across the OrbStack/Docker Desktop file-sharing boundary; it produced "database disk image is malformed". Seed from a container on the dev network instead (see docs/DEVELOPMENT.md). On a Linux host with a normal bind mount this is not an issue.
- 2026-09-28: end-to-end check that works: seed with `docker run --network jellytrim-dev_default ... golang:1.26-trixie go run ./scripts/devseed -config dev/config -jellyfin http://jellyfin:8096 -media /mnt/media -enable-all -live`, start the jellytrim container, POST /sync, watch logs for "job finished" and "Jellyfin picked up the new file", then query Jellyfin /Items for codec and UserData.
