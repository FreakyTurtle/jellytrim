# Backups and restore

## What JellyTrim keeps

When JellyTrim replaces a file, it first makes a backup of the original **beside the new file, in the same folder**, using a hard link where the filesystem supports one (or a rename if it does not, in which case the "backup" is simply the original file under its hidden name; see [First run](first-run.md#4-path-mappings) for what the path check reports about this).

The backup's name is hidden (dotfile) so Jellyfin, which ignores dotfiles, never lists it as a separate item: `.<name>.jellytrim-bak-<job>`. The in-progress new file, before it replaces anything, is also hidden: `.<name>.jellytrim-<job>.partial`.

A hard-linked backup uses **no extra disk space of its own**: it is a second directory entry for the same data as the original, not a copy. Free space still has to cover the new file while it is being encoded, though: JellyTrim checks for the estimated output size plus 20% plus 64 MiB of headroom before it starts, and needs that free space to remain available until the replacement happens. Once the original becomes the backup, its data continues to occupy space for as long as the backup is kept, alongside the new file. Plan disk headroom for at least the largest file JellyTrim might process.

**Backups are kept for 7 days by default**, configurable in Settings from 0 to 90 days (0 deletes the backup as soon as Jellyfin has picked up the change, so there is no restore window at all). A daily task deletes backups past their retention period; every deletion is recorded in the journal first.

## Restoring from History

1. Open **History** and find the job.
2. While its backup still exists, a **Restore** button is shown.
3. Restore only goes ahead if all of the following are still true:
   - the file at the path is still the exact one JellyTrim produced (same device, inode and size as recorded when the job finished), so a file you have since replaced yourself is never overwritten;
   - no job is currently working on the item;
   - Jellyfin confirms nobody is playing the item.

If any of these is not true, Restore refuses and explains why, rather than risking data loss.

### "Restore anyway"

If Jellyfin cannot be reached to confirm nobody is playing the item, Restore refuses with a message saying so, and History then offers **Restore anyway** as a separate, confirmed step. This is for when Jellyfin is down and a backup is about to expire, and you are confident nobody is watching. It skips only the "is anyone watching" check: the other two checks (same file, no job in progress) still apply, and if Jellyfin does answer during the retry and reports playback, it still refuses.

Restoring renames the backup back over the optimised file, asks Jellyfin to rescan, and marks the item so JellyTrim leaves it alone until you allow changes to it again from the item's page.

## Backing up `/config`

`/config` holds JellyTrim's whole state: every policy, setting, sync result and job history, and the Jellyfin API key. It does not hold your media. Back it up like any other important application data, for example with a periodic copy of the volume while JellyTrim is stopped, or your usual host-level backup tool. Because it is a SQLite database in WAL mode, do not back it up by copying individual files from a live database with a plain `cp` while JellyTrim is running; stop the container first, or use a tool that understands SQLite's backup API or filesystem snapshots.

## What happens after a crash or power cut

Every filesystem step JellyTrim takes (writing the partial file, hard-linking the backup, renaming) is recorded in a journal **before** it happens. On the next start, JellyTrim's recovery reads the journal and finishes or reverses any step that did not complete, acting only on the exact partial and backup file names recorded for that job next to the original file it was working on; it never guesses from a folder listing or a glob pattern.

- A job that had not started replacing anything goes back to Waiting.
- A job caught mid-replacement is resolved from the journal: if the rename had already happened (even if JellyTrim never received confirmation of it), the job is completed; otherwise the original is left as it was, with its backup in place.
- If neither the rename nor a safe rollback can be confirmed, the job is marked **Needs attention** with the exact location of the original stated, and nothing is deleted automatically. This is rare, and is designed to fail safe rather than guess.

A graceful stop is handled the same way at the queue level: `docker compose stop` (or `docker stop`) sends a termination signal, and JellyTrim stops accepting new HTTP requests (with a 10-second timeout to finish in-flight ones), stops the scheduler, and stops the queue: any job that is actively encoding has its partial file deleted and goes back to Waiting, with the original file untouched throughout. Only once these have stopped does JellyTrim close its database. Docker's own default stop period is 10 seconds before it sends a hard kill signal; the example compose file raises this to `stop_grace_period: 30s` so the sequence above has time to complete rather than being cut short.

## Finding and removing leftover hidden files, if ever needed

You should not normally need to do this: recovery on start-up handles the ordinary cases. If you are decommissioning a folder, or investigating something unusual, JellyTrim's own files in a media folder always match one of these patterns, next to the real file they belong to:

- `.<name>.jellytrim-<job-id>.partial`: an in-progress encode. Safe to delete if JellyTrim is stopped and you are sure no job is using it (check History and Queue first).
- `.<name>.jellytrim-bak-<job-id>`: a backup of an original file, kept for the retention period. Deleting this early removes your ability to restore that job from History; only do it if you are confident you no longer need the backup.

Find them with, for example:

```sh
find /path/to/media -name '.*.jellytrim-*'
```

Do not add a `-maxdepth` limit: TV episodes commonly sit several folders deeper than films, and a shallow depth would miss them.

Never delete a plain (non-hidden) media file this way; JellyTrim's own working files are always the hidden, prefixed ones above.
