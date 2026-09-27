---
name: release
description: Prepare and tag a JellyTrim release: pick the semantic version, finalise the changelog, check everything, create an annotated tag. Pushing the tag (which publishes images and binaries) needs the user's explicit go-ahead.
argument-hint: "<version, e.g. v0.2.0>"
disable-model-invocation: true
---

Release **$ARGUMENTS**.

1. Check the version follows semantic versioning against the last tag (`git describe --tags --abbrev=0`). Before 1.0, breaking changes bump the minor version.
2. Run `task check` and `/public-check`. Both must pass.
3. In `CHANGELOG.md`, move Unreleased to `## [X.Y.Z] - YYYY-MM-DD`, and update the comparison links.
4. `go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean` must succeed.
5. Commit `chore(release): vX.Y.Z` and create an annotated tag: `git tag -a vX.Y.Z -m "JellyTrim vX.Y.Z"`.
6. Stop. Tell the user that `git push origin main vX.Y.Z` will start the release workflow, which publishes `ghcr.io/freakyturtle/jellytrim` (tags `vX.Y.Z`, `vX.Y`, `vX`, `latest`) and a GitHub release with binaries and checksums. Push only if they say so.
