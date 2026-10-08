# Troubleshooting

## Getting logs

```sh
docker logs jellytrim
docker logs -f jellytrim        # follow
```

For more detail, set `JELLYTRIM_LOG_LEVEL=debug` in your compose file's `environment:` and restart the container. `JELLYTRIM_LOG_FORMAT=json` gives structured lines if you feed logs into another tool. The Jellyfin API key is never written to logs at any level.

## Reporting a bug

A new release may already fix your problem, so check the release notes and [upgrade](upgrading.md) first. If it is still there, use the [bug report form](https://github.com/freakyturtle/jellytrim/issues/new/choose). Every report is read, but there is no promise of a reply or a fix. Issues are for bugs only: there is no support channel, so this page and the rest of the guide are the help that exists.

Do not report security problems in a public issue. Report them privately, as [SECURITY.md](../../SECURITY.md) describes. In any report, include your JellyTrim version (`docker exec jellytrim jellytrim -version`, or the start-up line in `docker logs jellytrim`), and remove real hostnames, paths and API keys from anything you paste.

## Cannot reach Jellyfin

- Use the address JellyTrim can reach **from inside its own container**: a Docker service name (`http://jellyfin:8096`), not `localhost` and not your browser's address for Jellyfin. See [First run](first-run.md#2-connect-jellyfin).
- If Jellyfin is in a different Compose project, both containers must share a Docker network; see [Install](install.md#2-jellyfin-in-the-same-compose-file-or-a-separate-project).
- Check Jellyfin is actually listening on the port you used, and that nothing (a firewall, an internal-only bind address) blocks container-to-container traffic.

## API key rejected

- Recreate the key in Jellyfin (**Dashboard, then API Keys**) and paste it again; a key can be revoked or expire.
- Check for accidental leading or trailing whitespace if you set it through an environment variable or a secrets file (JellyTrim trims it, but confirm the file has no extra content).
- Confirm you did not set both `JELLYTRIM_JELLYFIN_API_KEY` and `JELLYTRIM_JELLYFIN_API_KEY_FILE`; the container refuses to start if both are set.

## Path mapping finds 0 files

"Found 0 of N files" during the path check means JellyTrim cannot see the files at the local path you gave, at all. Check:

1. The folder is actually mounted into the JellyTrim container (`docker exec jellytrim ls /your/local/path`).
2. The Jellyfin-side prefix in the mapping matches exactly what Jellyfin reports for that library's folders (case and trailing slashes matter).
3. You have not swapped the two sides of the mapping (Jellyfin path and local path).

See the worked examples in [First run](first-run.md#4-path-mappings).

## Permission denied on replace

JellyTrim can read a file (to inspect and preview it) but fails when it tries to write, hard-link or rename. This means the container's user does not own, or lacks write permission on, that folder.

- Set `user: "UID:GID"` in your compose file to the UID and GID that owns the media, found with `id <user>` on the host. See [Install](install.md#1-choose-the-containers-user).
- Confirm the volume is mounted read-write, not `:ro`.
- Run the path check again in Settings; it reports exactly which folder failed.

## Hard-link test fails (for example SMB or FUSE)

Some filesystems, including many SMB/CIFS mounts and some FUSE-based mounts, do not support hard links. The path check shows a warning that hard links are not supported and backups will use rename instead. This is expected and JellyTrim still works: instead of a hard-linked backup, it renames the original to the hidden backup name. The rename briefly moves the original file away from its usual path before the new file is renamed into place, rather than adding a second directory entry the way a hard link does, so it is not entirely without downside; JellyTrim still journals every step so a crash can be recovered.

## QSV not detected

- Confirm `/dev/dri` exists on the host: `ls /dev/dri`.
- Add `devices: ["/dev/dri:/dev/dri"]` and `group_add` with the render group's GID (`stat -c %g /dev/dri/renderD128`) to the compose file. See [Install](install.md#7-intel-quick-sync-qsv).
- Use **Test again** on the Hardware section of Settings after fixing the compose file and recreating the container.
- Check the result in **Settings, Hardware**: it tests each encoder (not each individual capability) and shows a table of Encoder, Codec, Device, Result and Notes, with an error detail in Notes on failure.
- Quick Sync HEVC encoding is built and covered by tests but has not yet been verified on real Intel hardware; x265 (software) is always available as a fallback and is the one that has been verified.

## Why was my file skipped

Open the item's page; the explanation names the exact reason. Common ones:

| Reason | Meaning |
|---|---|
| Symbolic link | JellyTrim only replaces real files. |
| Hard-linked file (more than one link) | Replacing it would use more space, not less (for example a seeding copy). |
| Outside the configured path mappings | The file is not inside a folder JellyTrim is allowed to change. |
| Unsupported container | Only Matroska (MKV) and MP4 are handled. |
| More than one video stream, interlaced, or rotated | Not supported yet; JellyTrim will not risk getting these wrong. |
| A stream the target container cannot hold | For example DTS or PGS subtitles cannot go safely into MP4; the whole file is skipped rather than dropping the stream. See the stream handling table in [docs/TRANSCODING.md](../TRANSCODING.md#9-stream-handling-reference). |
| Dolby Vision without a usable HDR10 base, or unclear HDR | JellyTrim never guesses at HDR metadata it cannot verify, and never tone-maps HDR to SDR. |
| HDR10+ or Dolby Vision (with a usable base) | Skipped unless the policy explicitly allows reducing it to HDR10. |
| No available encoder can do this safely | For example a policy names a hardware encoder for a file that needs HDR reduction, which only x265 can do. |
| Already optimal | The file is already efficiently encoded at this resolution, or the estimated saving is below the minimum saving setting. |
| Excluded (restored) | You restored this item from History; it is left alone until you allow changes again from the item's page. |

## Not enough free space

The job is skipped with a message naming the space needed and the space free. Free up space in the folder the file lives in (JellyTrim needs headroom for the new file alongside the original while both exist). Raising the minimum saving setting makes this less likely by attempting fewer, higher-value conversions; lowering it has the opposite effect, attempting more conversions for a smaller guaranteed saving.

## The queue does nothing

Check, in order:

1. **Dry Run.** While it is on, nothing is ever encoded, only reported. See [Settings](settings.md#dry-run).
2. **Process automatically.** If this is off in Settings, only jobs added manually (**Optimise now**) run.
3. **Queue paused.** Check the Queue page for a paused state and resume it.
4. **Processing schedule.** If the current hour is not ticked in the weekly schedule, encoding waits for the next active hour; Dry Run evaluation and previews are unaffected.
5. **Playback waits.** A job whose file is being watched in Jellyfin is held back and retried later, with a note explaining who is watching and on what device; this is expected behaviour, not a fault. See "Files being played" in [docs/TRANSCODING.md](../TRANSCODING.md#files-being-played).

## Jellyfin still shows the old codec

After replacing a file, JellyTrim asks Jellyfin to rescan it and polls for up to about 90 seconds for Jellyfin to report the new file size. If it has not by then, JellyTrim asks Jellyfin for a full item refresh and polls for the new size for up to another 90 seconds. If Jellyfin still shows the old information after that:

- Trigger a manual library scan in Jellyfin for that item.
- Check Jellyfin can actually reach the file at its usual path (nothing changed there: JellyTrim replaces the file in place, keeping the same path, so Jellyfin should keep the same item, watched state and favourites).

## FAQ

**Will it touch files while I am watching?**
Not normally. Before starting an encode, JellyTrim checks with Jellyfin whether the file is playing and holds the job back for 30 minutes if so. Before replacing the original with the finished file, it checks again, and keeps checking every 30 seconds for up to 6 hours while someone is watching, backing off and trying later rather than replacing a file mid-playback. If Jellyfin cannot be reached to answer that check, JellyTrim allows the job to go ahead once it has been unreachable for 10 minutes, rather than waiting indefinitely. See "Files being played" in [docs/TRANSCODING.md](../TRANSCODING.md#files-being-played).

**Does it keep subtitles and audio tracks?**
Yes, by default every audio track, subtitle track, chapter, attachment and piece of metadata is copied across unchanged. The only exception is when the target container cannot hold a particular stream at all (for example DTS or PGS subtitles into MP4): rather than silently dropping that stream, JellyTrim skips the whole file and says why. MKV output has no such restriction for the stream types JellyTrim handles.

**Does it work with Emby or Plex?**
Not yet. JellyTrim currently talks only to Jellyfin's API. Emby and Plex adapters are listed as a possible future addition, not built.

**Does it convert HDR to SDR?**
No, never. HDR10 and HLG are transcoded with their HDR metadata kept. HDR10+ and Dolby Vision are either skipped, or (only if a policy opts in) reduced to a plain HDR10 signal; JellyTrim does not tone-map HDR down to SDR under any setting.

**How much space will I save?**
It depends entirely on your media and policies; there is no fixed number. Run a Dry Run (on by default) and read the estimated saving on the Dashboard, and preview an individual policy before enabling it to see its own estimate. All estimates are labelled as estimates, since the true size is only known after encoding.
