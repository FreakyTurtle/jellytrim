---
name: fix
description: Fix a JellyTrim bug properly: reproduce, find the root cause, add a failing test, fix, verify, review. Use when something is broken.
argument-hint: "<bug description or failing test>"
---

Bug: **$ARGUMENTS**

1. If the cause is not obvious, run the **debugger** agent with the description and any evidence. Otherwise reproduce it yourself.
2. Write a failing test that captures the bug. For media decisions, add a fixture if none covers it.
3. Fix at the root. Keep the change small.
4. Run the **verifier**.
5. If the fix touches file handling, the pipeline or encoder arguments, run the **safety-reviewer** on the diff.
6. Commit as `fix(<scope>): <what was wrong>`. Add a line to `CHANGELOG.md` Unreleased if users could have seen the bug.
