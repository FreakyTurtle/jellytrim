# 0009. Stored ffprobe output is compressed with a fixed dictionary

Date: 2026-09-28
Status: Accepted

## Context
JellyTrim stores the full ffprobe output for every file so parsing improvements apply without probing again. Real films produce 10 to 60 KB of JSON each, which made the database about 18 KB per item (1.8 GB at 100,000 items). Plain gzip made each row about 2.3 KB, but SQLite gives any row over half a 4 KB page a page of its own, so the database barely shrank.

## Decision
Probe JSON is compacted (white space removed) and compressed with zlib using a preset dictionary built from synthetic ffprobe output (`internal/store/probedict_v1.txt`). The zlib header names the dictionary. Rows written by older versions, stored as plain text, are still read as they are and are compressed in small batches while JellyTrim is idle.

## Consequences
- The database is about a third of its former size.
- The dictionary file must never change: a test pins its SHA-256. An improved dictionary would be added as a new version beside it, with the reader choosing by the header.
- `store.Probe` returns compact JSON with the same meaning as ffprobe's output, not its exact bytes. Only `media.Parse` reads it.
