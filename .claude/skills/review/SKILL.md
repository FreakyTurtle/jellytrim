---
name: review
description: Review the current JellyTrim changes with the code-reviewer and conventions-keeper in parallel, plus the safety-reviewer when media handling changed, and merge the findings into one ranked list.
argument-hint: "[diff range, default: uncommitted changes]"
---

1. Work out the diff: `$ARGUMENTS` if given, else uncommitted changes (`git diff HEAD`), else the last commit.
2. Run in parallel, in one message:
   - **code-reviewer** on the diff.
   - **conventions-keeper** on the diff.
   - **safety-reviewer** if the diff touches `internal/{pipeline,queue,encoder,ffmpeg,plan,media}`, file I/O, or anything that handles the Jellyfin key.
3. Merge the findings into one list, most severe first, removing duplicates. Keep each finding's `path:line`, scenario and fix.
4. Say which findings you will fix now and which you propose to leave, with a reason.
