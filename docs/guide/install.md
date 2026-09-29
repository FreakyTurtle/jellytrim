# Install with Docker Compose

You need Docker with Compose, a running Jellyfin server (10.10 or later), and a Jellyfin API key. Create the key in Jellyfin under **Dashboard, then API Keys**; give it a name such as "JellyTrim" so you can revoke it separately from anything else.

## 1. Choose the container's user

JellyTrim replaces media files, so it must run as a user that can write to them. Do this before anything else.

On the machine that holds your media, find the UID and GID that owns it:

```sh
id <the user that owns your media>
```

This prints something like `uid=1000(myuser) gid=1000(myuser)`. If you are not sure which user owns the files, run `ls -ln /path/to/media` and read the numeric owner and group from the listing.

Use those numbers in `user: "UID:GID"` in your compose file. If you get this wrong, JellyTrim can read your media (to inspect it and offer previews) but fails to write, hard-link or rename when it tries to replace a file. The setup wizard's path check catches this early: it reports "cannot write here" for any folder the container's user does not own or have write permission on.

## 2. Jellyfin in the same compose file, or a separate project

**Same file** is simplest. Put both services in one `docker-compose.yml` and reach Jellyfin by its service name, for example `http://jellyfin:8096`.

**Separate compose projects** (for example Jellyfin already runs on its own) need a shared network so JellyTrim can reach Jellyfin by name. Create an external network once:

```sh
docker network create jellyfin-net
```

Add it to both projects. In Jellyfin's compose file:

```yaml
services:
  jellyfin:
    # ...
    networks:
      - jellyfin-net

networks:
  jellyfin-net:
    external: true
```

And in JellyTrim's:

```yaml
services:
  jellytrim:
    # ...
    networks:
      - jellyfin-net

networks:
  jellyfin-net:
    external: true
```

Bring up (or restart) both projects so they join the network, then use `http://jellyfin:8096` (Jellyfin's service name) as the Jellyfin URL in setup.

## 3. Mount your media

Mount media **read-write**. JellyTrim needs to write the new file, hard-link the backup and rename over the original.

Mount it at the **same path Jellyfin uses**, if you can. For example, if Jellyfin sees a film at `/media/movies/Film (2020)/Film.mkv`, mount your media at `/media/movies` in the JellyTrim container too. When the same local path already exists, the setup wizard pre-fills the mapping with equal Jellyfin and local prefixes, so no translation is needed.

If the paths cannot match (different container layouts, or Jellyfin runs outside Docker), mount media wherever is convenient and map the paths in the setup wizard. See [First run](first-run.md#4-path-mappings) for worked examples.

## 4. The config volume

Mount a local volume at `/config`:

```yaml
volumes:
  - ./jellytrim-config:/config
```

This holds:

- the SQLite database (`jellytrim.db`), with every policy, setting, sync result and history record;
- the Jellyfin API key, stored with file permissions `0600`, never shown in the UI;
- hardware test results.

**`/config` must be on local disk, not a network share** (NFS, SMB, or similar). JellyTrim's database runs SQLite in WAL (write-ahead log) mode, which relies on the filesystem's locking behaviour. Network filesystems commonly implement locking incorrectly or not at all, which can corrupt the database or make writes hang. Use a local bind mount or a local Docker volume.

Back up this folder like any other important data; see [Backups and restore](backups-and-restore.md).

## 5. Time zone

Set `TZ` so the processing schedule and daily sync time mean what you expect:

```yaml
environment:
  TZ: Europe/London
```

The default is UTC.

## 6. Ports: localhost or LAN

**JellyTrim has no login.** Anyone who can reach its web interface can change policies, turn off Dry Run and restore backups.

- Bind to this machine only (recommended unless you add authentication): `127.0.0.1:8080:8080`.
- Publish on your LAN: `8080:8080`. Only do this on a network you trust, or put a reverse proxy with authentication in front of it (see [Reverse proxy](reverse-proxy.md)).

## 7. Intel Quick Sync (QSV)

Only needed for hardware encoding. Pass through the render device and join the group that owns it:

```yaml
devices:
  - /dev/dri:/dev/dri
group_add:
  - "993"
```

Find the group ID that owns the render device on the host:

```sh
stat -c %g /dev/dri/renderD128
```

Use that number in `group_add`. Without it, the container can open the device node (world execute bit) but often cannot use it, and the hardware test fails.

After starting the container, check it worked in **Settings, Hardware**: it shows the same encoder table as setup, for example "Intel Quick Sync (HEVC) … ✓ Works". If QSV shows unavailable, see [Troubleshooting](troubleshooting.md#qsv-not-detected).

Remove the `devices` and `group_add` lines entirely if you only want software encoding (x265); JellyTrim then uses the CPU.

## 8. A `docker run` equivalent

For reference, `docker-compose.yml` (with the settings from the sections above) is the same as:

```sh
docker run -d \
  --name jellytrim \
  --restart unless-stopped \
  --user 1000:1000 \
  -p 127.0.0.1:8080:8080 \
  -e TZ=Europe/London \
  -v ./jellytrim-config:/config \
  -v /srv/media/movies:/mnt/media/movies \
  -v /srv/media/tv:/mnt/media/tv \
  --device /dev/dri:/dev/dri \
  --group-add 993 \
  --stop-timeout 30 \
  ghcr.io/freakyturtle/jellytrim:latest
```

## 9. The Jellyfin API key as a Docker secret

Rather than put the key in an environment variable, use the `_FILE` form with a Docker secret:

```yaml
services:
  jellytrim:
    # ...
    environment:
      JELLYTRIM_JELLYFIN_API_KEY_FILE: /run/secrets/jellyfin_api_key
    secrets:
      - jellyfin_api_key

secrets:
  jellyfin_api_key:
    file: ./secrets/jellyfin_api_key.txt
```

`./secrets/jellyfin_api_key.txt` holds only the key, with no trailing content beyond a newline (JellyTrim trims whitespace). Setting both `JELLYTRIM_JELLYFIN_API_KEY` and `JELLYTRIM_JELLYFIN_API_KEY_FILE` is an error and the container refuses to start.

## 10. Start it

```sh
docker compose up -d
```

Open `http://<your-server>:8080` (or `http://localhost:8080` if you bound to localhost). The setup wizard starts automatically; see [First run](first-run.md).

## Notes for common platforms

These are generic Docker behaviours; check your platform's own documentation for exact menu paths.

**Unraid.** Unraid runs containers as root by default unless the template sets a user; add `user: "UID:GID"` (or the equivalent field in the Unraid template editor) so JellyTrim runs as the user that owns your share, and confirm the media share is mounted read-write.

**Synology (Container Manager).** Container Manager exposes the same `user`, volume and device options as Compose, usually through a "Advanced Settings" or YAML editor when you create the project. Use the same UID and GID your DSM user or shared folder uses, found the same way with `id` from a shell (Synology supports SSH access).

**TrueNAS (Scale, apps).** TrueNAS's app system is built on Kubernetes or Docker Compose depending on version; where it exposes a raw compose or "Custom App" YAML editor, the same `user`, `volumes` and `devices` fields apply. Where it only exposes a UID/GID field in a form, set that field instead of `user:`.

**Portainer (stacks).** Paste the compose file as-is into a Portainer stack. Edit the `image`, volume paths, `user` and any `devices`/`group_add` lines to match your host before deploying, exactly as you would editing the file directly.
