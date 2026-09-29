# Upgrading

## Pulling a new image

```sh
docker compose pull jellytrim
docker compose up -d jellytrim
```

Or with `docker run`, pull the new tag and recreate the container.

## Tags

Image tags have no `v` prefix, even though the git release tags they are built from do (for example git tag `v0.4.1` produces image tags `0.4.1` and `0.4`).

| Tag | Points to |
|---|---|
| `latest` | The newest tagged release that is not a pre-release (a version without a `-` suffix, for example `0.4.0`). |
| `X.Y.Z` | An exact release, for example `0.4.1`. Use this to pin a specific version. |
| `X.Y` | The newest patch release of that minor version, for example `0.4`. |
| `X` | The newest release of that major version. **Not published while JellyTrim is pre-1.0** (any `0.x` release): only `latest`, `X.Y.Z` and `X.Y` exist until the first `1.0.0`. |

Images are built for `linux/amd64` and `linux/arm64` from `ghcr.io/freakyturtle/jellytrim`.

Pin an exact `vX.Y.Z` tag if you want to control exactly when you upgrade; use `latest` if you are happy to follow releases automatically.

## Back up `/config` first

Before upgrading, back up the `/config` volume (see [Backups and restore](backups-and-restore.md#backing-up-config)). This is the one folder an upgrade cannot easily undo damage to, since it holds every setting, policy and job record.

## Database migrations

JellyTrim's SQLite schema is versioned with small, numbered migration files, embedded in the binary and applied automatically on start-up. You do not run anything by hand: the new container version checks the schema version stored in the database and applies any migrations newer than it, in order, each in its own transaction.

**Migrations are forward-only.** There is no built-in "undo" for a schema change. This is the main reason to back up `/config` before upgrading: if you need to go back to an older JellyTrim version after a migration has run, restore the backed-up `/config` from before the upgrade rather than expecting the old version to read the new schema.

## Rolling back

To roll back:

1. Stop the container.
2. Restore the `/config` backup taken before you upgraded.
3. Change the image tag back to the previous version in your compose file.
4. Start the container again.

Rolling back the image alone, without also restoring the pre-upgrade `/config`, is not supported once a migration has run: an older binary refuses to start against a schema newer than it knows, and tells you to restore the config backup made before the upgrade.
