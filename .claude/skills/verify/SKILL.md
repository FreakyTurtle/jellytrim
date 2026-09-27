---
name: verify
description: Run JellyTrim's CI checks locally through the verifier agent and report pass or fail. Skipped tests are not passing tests.
---

Run the **verifier** agent and relay its report as is. If anything failed, propose the fix. If tests were skipped, say which and why they would run in CI (for example, ffmpeg missing locally).
