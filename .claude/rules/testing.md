---
paths:
  - "**/*_test.go"
  - "**/testdata/**"
---

# Tests

- Table-driven, next to the code. Name cases by behaviour (`"never watched does not match last-watched condition"`).
- **Golden files** in `testdata/*.golden` for ffmpeg arguments, ffprobe parsing and explanations. Regenerate with `go test ./pkg/... -update`, then read the whole diff before committing.
- **Fixtures.** ffprobe JSON lives in `internal/media/testdata/probe/`. Media clips are generated (`task dev:fixtures`), never committed. Hand-written fixtures are listed in `internal/media/testdata/README.md`.
- **Time** is fixed in tests. Never depend on the wall clock.
- **Files** only inside `t.TempDir()`. Tests that change files hash the original before and after.
- **ffmpeg tests** call `testutil.RequireFFmpeg(t)`, which skips locally when ffmpeg is missing and fails in CI (`JELLYTRIM_REQUIRE_FFMPEG=1`). A skip is not a pass: report skips.
- **SQLite tests** use a real database in `t.TempDir()`.
- **Jellyfin tests** use `jellyfintest`, never a real server.
- **Fake tokens** in fixtures are `deadbeefdeadbeefdeadbeefdeadbeef` so the public check recognises them.
- `-race` is on in `task test` and CI.
