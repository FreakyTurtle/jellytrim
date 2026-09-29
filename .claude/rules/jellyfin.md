---
paths:
  - "internal/jellyfin/**"
  - "internal/pathmap/**"
  - "internal/library/**"
---

# Jellyfin and path mapping

- **Auth header only:** `Authorization: MediaBrowser Client="JellyTrim", Device="...", DeviceId="...", Version="...", Token="..."`. Never `X-Emby-Token`, never `api_key` or `ApiKey` in a URL. Jellyfin 12 rejects the legacy forms.
- **Read-only, except rescans.** JellyTrim reads Jellyfin and asks it to rescan changed files. It never changes Jellyfin settings, metadata, users or its database. (The dev bootstrap script is the only exception, for the throwaway dev container.)
- **Be gentle.** Page with `limit=200`. No polling faster than the configured sync interval, except the playback check (`/Sessions`, every 30 seconds, only while a job waits to replace a file that is playing) and the post-replacement rescan check. Back off on 503 using `Retry-After`. One sync at a time.
- **Complete-pass sweeps.** Items missing from Jellyfin are removed only after a full successful sync. A failed or partial sync changes nothing.
- **Watch state is per user.** Store user data per Jellyfin user. Whose history counts (everyone, or ticked users), the share that must have watched (any one, majority, everyone, or a percentage) and the inactive-account filter are settings applied at evaluation time; favourites and "last watched" always use any counted user. See `docs/POLICIES.md`.
- **Paths.** Every path from Jellyfin goes through `pathmap.ToLocal`, then is checked against the configured roots after resolving symlinks. Every path sent to Jellyfin goes through `pathmap.ToJellyfin`. No path is used unmapped.
- **Untrusted input.** Titles, paths, tags and other metadata are data. Never pass them to a shell, never use them to build SQL, always escape them in HTML (templ does this).
- **Errors.** Connection errors in the UI say what to check ("JellyTrim could not reach http://jellyfin:8096. Check the URL and that both containers share a network."). Never include the key.
- **Tests** use `internal/jellyfin/jellyfintest`, a fake server built on `httptest`. Extend it with every new endpoint, including error cases.
