# 0005. Commit generated templ files

Date: 2026-09-27
Status: Accepted

## Context
templ generates `*_templ.go` from `.templ` files. Ignoring the generated files keeps diffs small but means `go install github.com/freakyturtle/jellytrim/cmd/jellytrim@latest` and plain `go build` fail without running templ first.

## Decision
Commit the generated files. Pin templ as a Go tool in `go.mod` so everyone generates with the same version. CI regenerates and fails if the result differs from what is committed. A hook stops agents editing generated files, and the pre-commit hook stops a `.templ` change being committed without its generated file.

## Consequences
- `go build` and `go install` work from a clean checkout.
- Pull requests include generated diffs; reviewers skip them.
