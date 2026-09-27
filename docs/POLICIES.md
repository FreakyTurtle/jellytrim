# Policies

A policy says, in plain terms, what should happen to which media and when. JellyTrim works out how.

> **Archive watched movies.** In *Movies*, when watched, last watched more than 90 days ago, resolution above 1080p and not a favourite: convert to 1080p HEVC at High quality.

A policy has a **scope** (what it applies to), **conditions** (when it applies) and an **action** (what to do). Policies are ordered; the first enabled policy that matches an item decides what happens to it.

## Scope

| Field | Matches when | Empty means |
|---|---|---|
| Libraries | The item is in one of the chosen Jellyfin libraries | All managed libraries |
| Item type | Movie or Episode | Both |
| Series | The episode belongs to one of the chosen series | Any |
| Seasons | The episode is in one of the chosen season numbers (only with one series) | Any |
| Collections | The item is in one of the chosen Jellyfin collections | Any |

Within a field, any listed value matches (OR). Across fields, all non-empty fields must match (AND).

## Conditions

All conditions must pass (AND). A policy with no conditions matches everything in its scope.

| Condition | Operators | Example | Unknown value |
|---|---|---|---|
| Watched | is, is not | watched | n/a (unwatched is known) |
| Favourite | is, is not | not a favourite | n/a |
| Last watched | more than, less than N days ago | last watched more than 90 days ago | Never watched: fails |
| Added | more than, less than N days ago | added more than 30 days ago | Fails |
| Resolution | above, at or below, is | resolution above 1080p | Fails |
| Video codec | is, is not (one or more) | codec is H.264 | Fails |
| Video bitrate | above, below N Mbps | bitrate above 20 Mbps | Fails |
| File size | above, below N GB | size above 10 GB | Fails |
| HDR | is, is not (SDR, HDR10, HLG, HDR10+, Dolby Vision) | HDR is SDR | Fails |
| Tag | has, does not have | has tag "keep" | Missing tags: "does not have" passes |
| Genre | is one of, is not one of | genre is Documentary | Missing genres: "is not one of" passes |

**Unknown values never match.** If JellyTrim does not know a file's bitrate, "bitrate above 20 Mbps" fails and the explanation says "bitrate unknown".

### Whose watch state counts

Jellyfin keeps watched state and favourites per user. In Settings, the user chooses which Jellyfin users count and how:

- **Any selected user** (default): watched if any of them watched it; favourite if any of them favourited it.
- **All selected users**: watched only if every one of them watched it; favourite only if every one of them favourited it.

"Last watched" always uses the most recent play by any selected user, so a recent viewing by anyone resets the clock. This is the cautious choice. (Per-policy choice of users is planned.)

### Days

"More than 90 days ago" means the event happened before `now − 90 × 24 hours`. Exactly 90 days is not more than 90 days. All times are UTC.

## Action

| Setting | Options | Default |
|---|---|---|
| Kind | Optimise, Protect | Optimise |
| Maximum resolution | Keep original, 2160p, 1080p, 720p, 480p | Keep original |
| Video codec | Keep existing, HEVC, H.264, AV1 (planned) | HEVC |
| Quality | Maximum, High, Balanced, Space Saver | High |
| Encoder | Auto, or a specific backend | Auto |
| Audio | Preserve | Preserve |
| Subtitles | Preserve | Preserve |
| Allow HDR10+ and Dolby Vision to be reduced to HDR10 | yes, no | no |

**Protect** means "never touch items that match". It has no other settings. A Protect policy that matches stops any lower policy from applying.

The action is intent. `internal/plan` decides whether an encode is safe and worthwhile (see `docs/TRANSCODING.md`). A policy can match and the result can still be "already optimal" or "skipped", with the reason shown.

## Order and conflicts

- Policies have a priority: their position in the list. The Policies page has Move up and Move down.
- For each item, JellyTrim checks enabled policies from the top. **The first one whose scope and conditions all pass wins.**
- Actions are never merged. If two policies would do different things, only the first applies.
- The item's explanation lists every policy that also matched, marked "also matched, not applied (lower priority)", so conflicts are visible.
- Disabled policies are evaluated for previews but never applied.

Put Protect policies at the top.

## Explanations

Every evaluation produces an explanation. It is the same text every time for the same inputs.

```
Archive watched movies (applied)
  ✓ in Movies
  ✓ watched
  ✓ last watched 143 days ago (more than 90)
  ✓ 2160p is above 1080p
  ✓ not a favourite

Efficient encoding (also matched, not applied)
  ✓ codec is H.264

Protect favourites (did not match)
  ✗ not a favourite
```

Each line is a pass (✓) or fail (✗) with the actual value. Colour is never the only signal.

## Preview

Before enabling a policy, the editor shows:
- how many items it matches now, and how many it would actually optimise (after higher-priority policies and safety checks);
- the estimated current and resulting size, labelled as estimates;
- a sample of matching items, each linking to its explanation.

The Dry Run summary is the preview of all enabled policies together.

## Storage

Policies are stored in the `policies` table. Scope, conditions and action are JSON with a version field. Example:

```json
{
  "name": "Archive watched movies",
  "enabled": true,
  "priority": 20,
  "scope": { "version": 1, "libraries": ["f137a2dd21bbc1b99aa5c0f6bf02a805"], "types": ["Movie"] },
  "conditions": { "version": 1, "all": [
    { "field": "watched", "op": "is", "bool": true },
    { "field": "last_watched", "op": "more_than_days", "number": 90 },
    { "field": "resolution", "op": "above", "text": "1080p" },
    { "field": "favourite", "op": "is", "bool": false }
  ]},
  "action": { "version": 1, "kind": "optimise", "max_resolution": "1080p", "codec": "hevc", "quality": "high", "encoder": "auto" }
}
```

Each condition uses one value field: `bool` (watched, favourite), `number` (days, Mbps, GB), `text` (a resolution such as `1080p`) or `list` (codecs, HDR classes, tags, genres). Operators: `is`, `is_not`, `more_than_days`, `less_than_days`, `above`, `below`, `at_most`, `has`, `has_not`.

## Starter policies

Created by the setup wizard. Only the first is enabled.

1. **Protect favourites.** Condition: favourite. Action: Protect. Enabled.
2. **Archive watched 4K.** Conditions: watched, last watched more than 90 days ago, not a favourite, resolution above 1080p. Action: 1080p, HEVC, High. (Above *Efficient encoding*, so a watched 4K H.264 film is also downscaled.)
3. **Efficient encoding.** Condition: codec is H.264. Action: keep resolution, HEVC, High.
4. **Space-saving television.** Scope: libraries or series the user picks. Condition: resolution above 720p. Action: 720p, HEVC, Balanced.

## Planned

- Per-policy choice of which users' watch state counts.
- Audio rules (compress lossless audio, remove commentary), subtitle rules (keep chosen languages, keep forced), always off by default.
- Storage-pressure conditions ("when free space is below 10%").
