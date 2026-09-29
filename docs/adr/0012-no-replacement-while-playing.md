# 0012. Never replace a file while someone is playing it

Date: 2026-09-29
Status: Accepted

## Context
Replacing a file is an atomic rename, and on Linux a player that already has the file open keeps reading the old copy. On network filesystems that is less certain, and a viewer who seeks or restarts could land in the new file. Encoding a file someone is watching also wastes work if it then cannot be swapped in. And someone watching the file during a long encode can change the facts the decision was made on: they may mark it a favourite, or watch it for the first time.

## Decision
- Before a job starts encoding, JellyTrim asks Jellyfin what is playing (`/Sessions`). If the item is playing (paused counts), the job is not started; the queue moves on and the item is held back for 30 minutes. If Jellyfin rejects the API key or is not set up, the job is held back the same way. The job then reads the item's watch state from Jellyfin again before re-checking the file; if Jellyfin cannot answer, it uses the last sync's.
- Before replacing, JellyTrim checks again and waits while the item is playing, asking every 30 seconds for up to 6 hours. The job keeps its worker slot and shows who is watching. The processing schedule does not interrupt this wait, because the encode is done and waiting uses no encoder. Once the item has been seen playing, two "not playing" answers in a row are needed.
- After 6 hours the new file is discarded, the job goes back to the queue, and the item is held back for 2 hours, so a client left paused cannot cause an endless loop of encodes. The third give-up ends the job as Skipped.
- If Jellyfin cannot be asked, the job keeps trying for 10 minutes, then replaces the file and records a warning. It never does this after seeing the item playing in the same wait, or when Jellyfin rejects the API key or is not set up: then it waits until the 6-hour limit and gives up.
- After the wait, and before anything is touched, the job re-reads Dry Run, re-reads the item's watch state (someone may have watched it during the encode; a failure counts as a give-up only if the item was seen playing), makes the item's decision again, and asks Jellyfin once more whether it is playing. It goes ahead only if Dry Run is off, the policies still choose the same plan and nobody has started watching; otherwise the new file is discarded (Dry Run or a new viewer: back to the queue; no longer chosen: Skipped). From then on Cancel is refused.
- If the new file cannot be deleted after a give-up or discard, the job fails and names the file rather than going back to the queue.
- Restore refuses while Jellyfin reports the item playing. When Jellyfin cannot be asked it refuses too, but History offers "Restore anyway", a confirmed second step that skips only that check, so a Jellyfin that stays down cannot stop a restore until the backup expires.

## Consequences
- With one worker, a job waiting for a viewer blocks other jobs for up to 6 hours; the queue shows why.
- Hold-backs and give-up counts are kept in memory; after a restart the check before encoding catches files that are still playing.
- Polling `/Sessions` every 30 seconds happens only while a job waits for a viewer. Reading one item's watch state costs one request per counted user, at job start and again before each replacement.
- Every replacement re-probes the original once more before the rename, which costs a second or two.
- A wrong or missing API key holds a waiting job for the full 6 hours before it gives up.
