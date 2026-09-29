# Policies: common recipes

A policy says what should happen to which media, and when. This page gives task-focused recipes for the situations most people want. For the full model (scope, conditions, actions, the whose-watch-state-counts settings, and how policies are stored), see [docs/POLICIES.md](../POLICIES.md).

![Policy editor](../images/policy-editor.webp)

## How precedence works, briefly

Policies are ordered. For each item, JellyTrim checks enabled policies from the top and **the first one whose scope and conditions all match wins**. Lower policies are not merged in, only recorded as "also matched, not applied" in the item's explanation. Put anything you want to always protect at the top of the list.

## Recipe: archive watched 4K films

Shrink a film once it has been watched and is unlikely to be watched again soon.

- **Scope:** all managed libraries (leave empty), or scope it to just your Movies library if you only want it to apply there.
- **Conditions:** watched; last watched more than 90 days ago; not a favourite; resolution above 1080p.
- **Action:** Maximum resolution 1080p, codec HEVC, quality High.

This is the starter policy **Archive watched 4K**, offered in setup. As a starter it covers every managed library, not only Movies, so anything above 1080p (including a 4K TV episode) can match it.

## Recipe: shrink old TV episodes

Cap television at a lower resolution straight away, since most shows are watched once at typical viewing distances where 720p is hard to tell apart from 1080p.

- **Scope:** your TV Shows library or libraries (or specific series).
- **Conditions:** resolution above 720p.
- **Action:** Maximum resolution 720p, codec HEVC, quality Balanced.

Put this above a general "convert to HEVC" policy, so an episode is capped and converted in one pass rather than converted twice.

## Recipe: protect favourites

Never touch anything anyone has favourited, regardless of any other policy.

- **Scope:** all managed libraries (leave empty).
- **Conditions:** favourite.
- **Action kind:** Protect.

Put this policy **first**. A Protect match stops every lower policy for that item; it has no other settings.

## Recipe: convert a whole library to HEVC

Bring an entire library onto a more efficient codec without changing resolution.

- **Scope:** the library.
- **Conditions:** video codec is H.264 (add "not a favourite" if you also run the favourites recipe below it).
- **Action:** Maximum resolution Keep original, codec HEVC, quality High.

This is the starter policy **Efficient encoding**. A file already in HEVC (or a more efficient codec) is not matched by this policy at all: its explanation shows "not matched by any enabled policy" under Efficient encoding, rather than "already optimal". See the worth-it checks in [docs/TRANSCODING.md](../TRANSCODING.md#3-decisions-internalplan).

## Previewing before you enable a policy

Open the policy editor and look at the preview before switching a policy on:

- how many items it matches now, and how many it would actually change once higher-priority policies and safety checks are applied;
- the estimated current and resulting size (labelled as an estimate);
- a sample of matching items, each linking to its full explanation.

For a policy that would decide more than 1,000 items, the preview is worked out from an evenly spaced sample and scaled up, and says so. The Dry Run summary on the Dashboard and the Library page always use the full, exact evaluation across every enabled policy.

## Explanations

Every item's page shows exactly why a policy did or did not match, line by line:

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

If a value JellyTrim does not know (for example an unclear bitrate) is part of a condition, that condition fails and the explanation says the value is unknown, rather than guessing.
