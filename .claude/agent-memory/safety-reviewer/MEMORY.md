# safety-reviewer memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.


## Bug classes to check first

- 2026-09-27: Undo paths that can themselves fail (rename-based backup, then final rename fails, then undo rename fails) must not report "original unchanged"; check the job ends in a state startup recovery will revisit.
- 2026-09-27: Ambiguous rename errors on network filesystems (error returned but rename happened); undo logic must identify what is at the original path by inode, not assume.
- 2026-09-27: Restore or any user action that renames over the live path must verify the current file is JellyTrim's output (recorded identity) and that no job for the item is active.
- 2026-09-27: TOCTOU between the identity re-check, the backup link and the final rename, especially when EBUSY retries loop without re-checking identity.
- 2026-09-27: Journal durability: SQLite WAL with synchronous=NORMAL does not make "journalled before it happens" true across power loss.
- 2026-09-27: Source probe and source identity must come from the same moment; a probe read back from a shared cache table can be overwritten by a concurrent background probe.
- 2026-09-27: Validation that uses format duration alone misses a short or gappy video stream when audio is copied full length; encode stderr at error level ignored on exit 0.
- 2026-09-27: Path stored on the job row at enqueue time versus the path the pipeline used at start; recovery, restore and backup expiry must use the same path the journal used.
- 2026-09-27: Recovery path checks by prefix/substring ("same folder, starts with '.', contains '.jellytrim-'") instead of the exact PartialPath/BackupPath for that job and original.
- 2026-09-27: "Replaced" classification inferred from absence of the partial file only, without checking the inode at the original path.
- 2026-09-27: Validation checks listed in docs but missing in code (colour matrix, attachment mimetype, cross-type stream order).
- 2026-09-27: Safety tests inject one failure at a time; check for double-failure cases and for negative unit tests of every Validate check.
