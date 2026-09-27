# 0002. Policies are evaluated in order and the first match wins

Date: 2026-09-27
Status: Accepted

## Context
Several policies can match the same item and ask for different things (for example 1080p HEVC High and 720p HEVC Balanced). The brief requires deterministic, visible behaviour and no silent conflicting actions.

## Decision
Policies have a priority (their position in the list). For each item, JellyTrim checks enabled policies from the top, and the first whose scope and conditions all pass decides the action. Actions are never merged. Protect is an action, so a Protect policy near the top shields items from everything below it. Every other matching policy is listed in the explanation as "also matched, not applied".

## Consequences
- Behaviour is easy to predict and explain, like firewall rules.
- Users must order policies deliberately; the UI makes order visible and shows other matches.
- Combining two partial intents (one policy for resolution, another for codec) is not possible; each policy states a complete action.
