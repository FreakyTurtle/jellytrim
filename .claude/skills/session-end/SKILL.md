---
name: session-end
description: Close a JellyTrim session by recording what was done, what was verified and what is next in docs/ongoing-work.md, so the next session can pick up cleanly.
---

Add a dated entry at the top of `docs/ongoing-work.md` (below the title), newest first:

```
## YYYY-MM-DD: <one-line summary>

- Done: ...
- Verified: commands run and results.
- Not verified: ... and why.
- Next: the first thing the next session should do.
- Open questions: for the maintainer, if any.
```

Keep it short. Remove entries older than the latest ten, since git history keeps them. Do not commit unless asked.
