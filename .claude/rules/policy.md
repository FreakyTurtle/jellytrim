---
paths:
  - "internal/policy/**"
---

# Policy engine

Read `docs/POLICIES.md`.

- **Pure.** No I/O. Input is an item snapshot (Jellyfin metadata, user data, media model) plus the time `now`. Output is a result with an explanation.
- **Deterministic.** Same inputs give the same result and the same explanation text, byte for byte. Policies are evaluated in priority order (lowest number first, then ID). The first enabled policy whose scope and conditions all match wins. Others that also matched are listed as "also matched, not applied".
- **Explain everything.** Each condition returns pass or fail with a plain-English line (`✓ last watched 143 days ago`, `✗ favourite`). A failed scope or condition is shown too, so users can see why a policy did not match.
- **Unknown is not a match.** A condition on a value that is missing (bitrate unknown, never watched) fails and says why.
- **Actions describe intent**, not ffmpeg settings: max resolution, codec, quality tier, audio and subtitle handling, HDR opt-ins. `internal/plan` turns them into an encode plan or a skip.
- **Protect** is an action. A matching Protect policy stops lower-priority policies from applying.
- Tests are table-driven with a fixed `now`. Cover every condition's boundary (exactly N days, equal resolution, unknown values) and precedence.
