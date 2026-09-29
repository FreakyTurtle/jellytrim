# 0011. Watch history: everyone by default, a share that must have watched, inactive accounts ignored

Date: 2026-09-29
Status: Accepted

## Context
Jellyfin keeps watched state and favourites per user. The first version counted only users ticked in Settings and combined them as "any" or "all". Households add users over time, so a new person's favourites were ignored until someone ticked them. "All" could never be satisfied if one account never watched films, and there was no middle ground between one person and everyone.

## Decision
- **Whose history counts:** everyone, including users added later, by default. Choosing specific users remains an option.
- **The share that must have watched:** a percentage of counted users, rounded up to whole users, with at least one. The presets are any one (0), a majority (51%: 2 of 2, 2 of 3, 3 of 4) and everyone (100%), plus a custom percentage.
- **Inactive accounts:** a user who has not used Jellyfin for more than N days (default 90; 0 turns the filter off) is left out of the share. Their favourites and plays still count, so leaving an account unused never removes a file's protection. If the filter would leave no one in the share, it is ignored for that evaluation.
- **Majority** means more than half for any number of users (N / 2 + 1), not 51% rounded up, which would ask for 27 of 51.
- **Unchanged and deliberate:** a favourite of any counted user protects a file whatever the share, and "last watched" is the most recent play by any counted user, so a rewatch by anyone resets the clock.
- Existing installs are migrated: "all" becomes 100%, "any" becomes any one, and an install with a deliberate subset of ticked users keeps "selected". An install with every enabled user ticked, or nobody ticked, becomes "everyone". The inactive filter applies from the first sync after the upgrade. These change results on some installs; `docs/POLICIES.md` lists how.

## Consequences
- Watch state and collections are read for every candidate user at each sync (all enabled users by default): one light request pass per user for watch state, and one request per user per collection for collections.
- The share and the inactive filter are applied at evaluation time, so changing them needs no resync.
- Explanations state the share, for example "watched by 2 of 3 users (majority needed)".
