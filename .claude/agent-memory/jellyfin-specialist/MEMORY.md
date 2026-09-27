# jellyfin-specialist memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-27 (Jellyfin 12.1.0): the bootstrap sequence works as written in scripts/dev-jellyfin-bootstrap.sh: GET then POST /Startup/User; POST /Auth/Keys?app=JellyTrim returns no body, read the key back from GET /Auth/Keys.
- 2026-09-27 (12.1.0): `EnableInternetProviders: false` alone does NOT stop online lookups; fixture films were renamed after real films. Pass `TypeOptions` with empty `MetadataFetchers` and `ImageFetchers` for Movie, Series, Season, Episode and BoxSet.
- 2026-09-27 (12.1.0): the first TV library scan can lag the movie scan; wait until the item count is stable across two polls. Without online metadata, item names are the file or folder names ("Alpha (2019)", "Mike Show - S01E01").
- 2026-09-27 (12.1.0): /UserPlayedItems/{id}?userId=&datePlayed= and /UserFavoriteItems/{id}?userId= work with a user access token.
