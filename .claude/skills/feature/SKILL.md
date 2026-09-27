---
name: feature
description: "End-to-end workflow for a JellyTrim feature or significant change outside a milestone: align with the spec, plan with the architect, build in slices with tests, verify like CI, review, sync docs, commit. Use when asked to build, add or change product behaviour."
argument-hint: "<what to build>"
---

You are delivering: **$ARGUMENTS**

1. **Understand.** If the change is small and obvious (one or two files, no schema, no media handling), skip to step 4. Otherwise run the **spec-aligner** on the request and put any tension to the user.
2. **Plan.** Run the **architect**. For media work, have the **media-specialist** check the plan.
3. **Branch.** Once a remote exists, `git switch -c feat/<short-name>`. Until then, work on `main` and commit when verified.
4. **Build** slice by slice (yourself, **implementer**, or **ui-builder**). Tests first for decisions and file handling. Run the **verifier** after each slice.
5. **Check it works.** `/verify-ui` for UI changes; the dev stack for Jellyfin or encoding changes.
6. **Review.** `/review`. Fix blockers and should-fix items.
7. **Docs.** `/docs-sync`; `/adr-new` for non-obvious decisions; `CHANGELOG.md` Unreleased.
8. **Commit** with a conventional message. Report what changed, how it was verified, and anything not verified.
