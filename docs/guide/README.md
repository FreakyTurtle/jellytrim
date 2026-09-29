# JellyTrim user guide

This guide is for people who run JellyTrim themselves, next to Jellyfin, with Docker. It assumes you know Docker Compose and Jellyfin, but not ffmpeg.

Read the pages in this order the first time:

1. [Install](install.md): set up the container, volumes, user and (optionally) Intel Quick Sync.
2. [First run](first-run.md): the setup wizard, reading your first Dry Run, and a cautious first rollout.
3. [Settings](settings.md): every setting on the Settings page, and the environment variables that can preset them.
4. [Policies](policies.md): common recipes for policies, and how precedence works.
5. [Reverse proxy](reverse-proxy.md): JellyTrim has no login of its own. Add authentication if you expose it beyond your LAN.
6. [Backups and restore](backups-and-restore.md): what JellyTrim keeps, how to restore a file, and what happens after a crash.
7. [Upgrading](upgrading.md): pulling new images, tags, and database migrations.
8. [Troubleshooting](troubleshooting.md): common problems and an FAQ.

For how JellyTrim works internally, see the main docs: [Product](../PRODUCT.md), [Architecture](../ARCHITECTURE.md), [Transcoding](../TRANSCODING.md), [Policies](../POLICIES.md), [UI](../UI.md).
