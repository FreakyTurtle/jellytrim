---
name: pr
description: Open a GitHub pull request for the current JellyTrim branch with gh, after checks pass. For use once the repository exists on GitHub.
disable-model-invocation: true
---

1. `gh` must be installed and authenticated (`gh auth status`). If not, stop and say so.
2. Run `task check` and `/public-check`. Both must pass.
3. Push the branch (this asks for confirmation): `git push -u origin HEAD`.
4. Write the PR body to the scratchpad, following `.github/PULL_REQUEST_TEMPLATE.md`: what and why, how it was verified, media-safety impact, screenshots for UI changes.
5. `gh pr create --title "<conventional title>" --body-file <scratchpad file>`.
6. Report the PR URL and the CI status (`gh pr checks`).
