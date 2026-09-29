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

Jellyfin keeps watched state and favourites per user. Three settings decide whose watch state JellyTrim counts and when a file counts as watched. They are in Settings and in the setup wizard, and apply to every policy.

**Whose history counts** (`watch_users`):

- **Everyone** (default): every enabled Jellyfin user. Users added in Jellyfin later are picked up at the next sync and count from then on, with no change in JellyTrim.
- **Selected users**: only the users ticked in Settings. A user added in Jellyfin later does not count until ticked.

Disabled Jellyfin users never count. Users hidden from Jellyfin's sign-in screen count like any other user; the hidden flag is only shown.

**Share that must have watched** (`watch_percent`, 0 to 100):

| Choice | Value | Watched when |
|---|---|---|
| Any one | 0 | at least one counted user played it |
| Majority | 51 | more than half of the counted users played it |
| Everyone | 100 | every counted user played it |
| Custom | 1 to 100 | at least that share of the counted users played it |

The number of users needed is the share of the counted users, rounded up, and always at least one. With N counted users, a rule of P% needs ceil(P × N / 100) of them. Majority is the exception: it always means more than half, N / 2 + 1 rounded down, so 26 of 51 rather than 27. A custom 51% is stored the same way as Majority and behaves the same.

| Counted users | Majority (51%) | Everyone | 60% | 67% |
|---|---|---|---|---|
| 1 | 1 | 1 | 1 | 1 |
| 2 | 2 | 2 | 2 | 2 |
| 3 | 2 | 3 | 2 | 3 |
| 4 | 3 | 4 | 3 | 3 |
| 5 | 3 | 5 | 3 | 4 |

A worked example with four counted users (dev, alex, sam and robin):

| Item | Played by | Any one | Majority | Everyone | 60% |
|---|---|---|---|---|---|
| Alpha | alex | watched | not watched | not watched | not watched |
| Golf | dev, sam (sam's favourite) | watched | not watched (2 of 4, 3 needed) | not watched | not watched (50%) |
| Charlie | dev, alex, sam | watched | watched (3 of 4) | not watched | watched (75%) |
| Echo | all four | watched | watched | watched | watched |
| Bravo | nobody | not watched | not watched | not watched | not watched |

(The dev bootstrap script sets up this spread on the dev Jellyfin; see `docs/DEVELOPMENT.md`.)

**Ignore inactive accounts** (`watch_inactive_days`, default 90, 0 turns it off): a user whose last activity in Jellyfin is more than this many days ago is left out of the share, so an old account nobody uses cannot stop items from counting as watched. Their favourites and plays still count (see below), so an absent person's favourites keep protecting files. A user who has never used Jellyfin counts as inactive while the filter is on. JellyTrim reads each user's last activity at every sync and applies the filter each time it evaluates, so changing the number takes effect without a sync.

The filter never leaves nobody. If every user who would otherwise count is inactive, the filter is not applied and all of them count; Settings says so ("None of them has been active in the last 90 days, so inactive users are counted").

Settings shows one line summing this up, for example "Counting 3 of 4 users (1 inactive). A file counts as watched when a majority of them have watched it.", and lists every Jellyfin user as counted or not counted with the reason: disabled in Jellyfin, not selected, inactive for N days, or never used Jellyfin. An item's page shows each user's watch state the same way.

**Favourites and last watched use any counted user, including inactive ones**, whatever the share. Two things are always the cautious choice:

- **Favourite** is true if any counted or inactive user favourited the item, so one person's favourite is enough for *Protect favourites* to keep it, even after that person has been away for months.
- **Last watched** is the most recent play by any counted or inactive user, so a viewing by anyone resets the clock. It is dated by that play even when the share rule says the item is not watched yet.

A user who is disabled or not selected affects neither. The item's page marks an inactive user "inactive for N days; favourites and plays still count".

**Explanations.** With the Any one rule, or when only one user counts, the watched line reads "watched" or "not watched". Otherwise it gives the count and what was needed:

- Majority: "watched by 2 of 3 users (majority needed)", "watched by 1 of 3 users (majority needed)"
- Everyone: "watched by every counted user", "watched by 1 of 2 users (everyone needed)"
- Custom: "watched by 2 of 3 users (67%; 60% needed)". A share short of the rule is rounded down, so 2 of 3 under a 67% rule reads "66%; 67% needed", never "67%".

The line does not name the users left out; the item's page lists counted and not counted users. When nobody counts (for example Selected users with nobody ticked), watched and favourite conditions never match, and the lines say "no Jellyfin users are counted for watch state" and "no Jellyfin users are counted for favourites".

**Upgrading from an older version.** Older versions had "any selected user" and "all selected users". On upgrade:

- "any" becomes Any one (0) and "all" becomes Everyone (100).
- If some users were ticked and at least one enabled user was not, the install keeps Selected users with the same ticks. Otherwise (every enabled user ticked, or none) it becomes Everyone.
- Ignore inactive accounts starts at 90 days. Users' last activity is read at the first sync after the upgrade. Until then no user has any, so the filter is not applied (see above). After that sync, with Everyone or Majority, an inactive user no longer holds back the share, so some items can start counting as watched. Their favourites keep counting.
- An install with every enabled user ticked becomes Everyone, so users added to Jellyfin later count from then on. An install with nobody ticked (where watched and favourite conditions never matched) also becomes Everyone, so those conditions start matching.

(Per-policy choice of users is planned.)

### Days

"More than 90 days ago" means the event happened before `now − 90 × 24 hours`. Exactly 90 days is not more than 90 days. All times are UTC.

### Restored items

When a user restores a job's original from History, JellyTrim records an exclusion for that item: it is left alone, whatever the policies say, until the user allows changes again from the item's page. This overrides every policy; it is checked before scope and conditions, alongside the other reasons an item cannot be evaluated (see `docs/ARCHITECTURE.md`, data model, table `exclusions`).

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

Match counts are exact. When a policy would decide more than 1,000 items, the split into optimise, already optimal and skipped, and the size estimates, are worked out from an evenly spaced sample of 1,000 of them and scaled up; the editor says "Estimated from 1,000 of N items". The Dry Run summary and the Library always use the full evaluation.

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
3. **Space-saving television.** Scope: libraries or series the user picks. Condition: resolution above 720p. Action: 720p, HEVC, Balanced. (Above *Efficient encoding*, so episodes are capped at 720p rather than only converted.)
4. **Efficient encoding.** Condition: codec is H.264. Action: keep resolution, HEVC, High.

## Planned

- Per-policy choice of which users' watch state counts.
- Audio rules (compress lossless audio, remove commentary), subtitle rules (keep chosen languages, keep forced), always off by default.
- Storage-pressure conditions ("when free space is below 10%").
