# 0003. Keep the source container and path, and let the modification time change

Date: 2026-09-27
Status: Accepted

## Context
Jellyfin derives an item's ID from its path. Changing a file's path or extension creates a new item and loses watched state, favourites and play history. Jellyfin also re-reads a file's media information only when its modification time changes.

## Decision
The optimised file replaces the original at the same path, in the same container format (Matroska stays Matroska, MP4 stays MP4). Files in other containers, or with streams the container cannot hold, are skipped with a reason. The new file keeps its new modification time. After replacing, JellyTrim notifies Jellyfin with `POST /Library/Media/Updated` for that path and checks that Jellyfin picked up the change.

## Consequences
- Watched state, favourites and history are preserved.
- Some files cannot be optimised (AVI, TS, MP4 with PGS subtitles) until a later version adds opt-in container changes.
- Tools that track files by modification time will see the change; that is intended.
