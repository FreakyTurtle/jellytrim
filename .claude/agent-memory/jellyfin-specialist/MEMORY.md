# jellyfin-specialist memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-27 (Jellyfin 12.1.0): the bootstrap sequence works as written in scripts/dev-jellyfin-bootstrap.sh: GET then POST /Startup/User; POST /Auth/Keys?app=JellyTrim returns no body, read the key back from GET /Auth/Keys.
- 2026-09-27 (12.1.0): `EnableInternetProviders: false` alone does NOT stop online lookups; fixture films were renamed after real films. Pass `TypeOptions` with empty `MetadataFetchers` and `ImageFetchers` for Movie, Series, Season, Episode and BoxSet.
- 2026-09-27 (12.1.0): the first TV library scan can lag the movie scan; wait until the item count is stable across two polls. Without online metadata, item names are the file or folder names ("Alpha (2019)", "Mike Show - S01E01").
- 2026-09-27 (12.1.0): /UserPlayedItems/{id}?userId=&datePlayed= and /UserFavoriteItems/{id}?userId= work with a user access token.
- 2026-09-27 (12.1.0): auth failures are an empty 401. `X-Emby-Token` and `?api_key=` get 401, but `?ApiKey=` was still accepted (200). JellyTrim never sends either; the fake rejects both.
- 2026-09-27 (12.1.0): the MediaBrowser header is parsed leniently: unquoted values and a header with only `Token="..."` both work. The fake is stricter on purpose (all five fields, quoted).
- 2026-09-27 (12.1.0): `GET /Items/{id}?userId=` works, and so does the legacy `/Users/{userId}/Items/{id}`. An unknown GUID gives a problem+json 404; a non-GUID ID gives a 400 validation problem.
- 2026-09-27 (12.1.0): `GET /Items/00000000000000000000000000000000?userId=` returns 200 with the user's root folder ("Media Folders"), not 404. The client checks the returned Id matches.
- 2026-09-27 (12.1.0): an unknown `userId` on `/Items` gives a plain-text 404 "Error processing request.".
- 2026-09-27 (12.1.0): a missing primary image is a 404 whose body is a JSON string ("<name> does not have an image of type Primary").
- 2026-09-27 (12.1.0): `POST /Library/Media/Updated` needs `Content-Type: application/json` (415 without). It returns 204 even for an empty list or a path outside every library, so 204 proves nothing; poll the item.
- 2026-09-27 (12.1.0): `POST /Items/{id}/Refresh?...` returns 204; unknown item 404.
- 2026-09-27 (12.1.0): without `fields`, items carry no Path, MediaSources, DateCreated, Tags, Genres, ParentId or SortName. Without `userId` there is no UserData. `LastPlayedDate` is omitted when never played. Dates have seven fractional digits and a Z.
- 2026-09-27 (12.1.0): a movie's `ParentId` is not the library's `ItemId` from /Library/VirtualFolders, but `parentId=<ItemId>&recursive=true` returns the library's items. Episodes carry SeriesId, SeasonId, and a localised SeasonName ("Series 1").
- 2026-09-27 (12.1.0): `/System/Info` reports `OperatingSystem: ""` in the official container.
- 2026-09-27: BoxSet queries (`includeItemTypes=BoxSet`, `parentId=<boxset>`) are not yet confirmed against a real server: the dev dataset has no collections, and creating one would add a Collections library to the dev server.
- 2026-09-29 (12.1.0): `GET /Sessions` with the API key lists every session; API-key requests do not create a session of their own. `activeWithinSeconds=960` is honoured (filters on LastActivityDate; the dev bootstrap's day-old session drops out). Idle sessions carry a `PlayState` and may carry a `NowPlayingQueue` but no `NowPlayingItem`; only `NowPlayingItem` means something is playing.
- 2026-09-29 (12.1.0): a playing session has `NowPlayingItem` (a full item: Id, Name, Type, Path, RunTimeTicks, MediaStreams...) plus `PlayState.{PositionTicks, IsPaused, MediaSourceId, PlayMethod}`, and `UserName`, `Client`, `DeviceName` at the top level. A paused session keeps `NowPlayingItem` with `IsPaused: true`; after `/Sessions/Playing/Stopped` it disappears at once.
- 2026-09-29 (12.1.0): to test playback on the dev server, authenticate a throwaway device with `POST /Users/AuthenticateByName`, then `POST /Sessions/Playing`, `/Sessions/Playing/Progress` (IsPaused) and `/Sessions/Playing/Stopped`, then `POST /Sessions/Logout`. Playback start bumps PlayCount and LastPlayedDate; put them back with `POST /UserItems/{id}/UserData?userId=` (200, API key works).
- 2026-09-29 (12.1.0): users created with POST /Users/New are hidden (`Policy.IsHidden: true`) and have no LastActivityDate until they sign in once; the dev bootstrap signs each new user in so the inactive-account filter sees them as active.
