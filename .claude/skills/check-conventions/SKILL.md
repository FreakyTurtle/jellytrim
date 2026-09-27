---
name: check-conventions
description: Check the current JellyTrim diff against the project rules, ADRs, writing style and open-source hygiene using the conventions-keeper agent.
argument-hint: "[diff range]"
---

Run the **conventions-keeper** agent on `$ARGUMENTS` (default: uncommitted changes). Relay its Blocking, Worth fixing and FYI groups. Offer to fix the Blocking items.
