---
name: dev-stack
description: Bring up JellyTrim's dev stack (a throwaway Jellyfin plus JellyTrim built from the Dockerfile, on synthetic fixtures), bootstrap Jellyfin, and report the URLs and API key location.
---

1. `task dev:fixtures` if `dev/media` is missing or stale.
2. `task dev:stack` starts `docker-compose.dev.yml`: Jellyfin on `http://localhost:8096` (media at `/media/...`) and JellyTrim on `http://localhost:8097` (media at `/mnt/media/...`, so path mapping is needed).
3. `task dev:bootstrap` runs `scripts/dev-jellyfin-bootstrap.sh`: completes the Jellyfin wizard, creates the users `dev`, `alex`, `sam` and `robin` (password = name), adds libraries, sets a fixed spread of played and favourite items per user (printed as a table, useful for checking the watch rules), and writes the API key to `dev/jellyfin-api-key` (gitignored). It is safe to run twice.
4. Report the URLs, the Jellyfin logins (`dev` / `dev`; also `alex`, `sam`, `robin` with the name as password), and that the key is in `dev/jellyfin-api-key`. Never print the key into a commit, doc or memory file.

To reset: `task dev:stack:reset` (asks first; it deletes `dev/config` and `dev/jellyfin`).
