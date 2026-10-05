# Security policy

## Supported versions

JellyTrim is before version 1.0. Security fixes go into the latest minor release only. Upgrade to the latest release before reporting a problem, if you can.

| Version | Supported |
|---|---|
| Latest minor release (for example 0.4.x when 0.4 is the newest) | Yes |
| Anything older | No |

## Reporting a vulnerability

Please report vulnerabilities privately. Do not open a public issue.

1. Go to the JellyTrim repository on GitHub.
2. Open the **Security** tab.
3. Choose **Report a vulnerability**.

This opens a private security advisory that only the maintainer can see. Include what you found, how to reproduce it, which version you used, and what an attacker could do with it. Remove real API keys, hostnames and personal paths from logs before you attach them.

JellyTrim is maintained by one person in their spare time, so there is no guaranteed response time. Security reports are read and taken seriously, and are the only kind of report JellyTrim accepts (see [CONTRIBUTING.md](CONTRIBUTING.md)). You will be credited in the advisory unless you ask not to be.

## Threat model

### Where JellyTrim is meant to run

JellyTrim is self-hosted software for a home or small-group server. It is designed to run on a **trusted private network**, next to Jellyfin, and to be used by the people who run that server.

### No built-in authentication

JellyTrim has no login, by design. Anyone who can reach its web interface can change policies, turn off Dry Run, start and cancel jobs, and restore backups. That is enough to cause a lot of re-encoding.

Do not expose JellyTrim directly to the internet. Choose one of these:

- Bind it to one machine or your LAN only (for example `127.0.0.1:8080:8080` in Docker Compose).
- Put it behind a reverse proxy that adds authentication, such as forward authentication with Authelia or Authentik, or Caddy's `basic_auth`.
- Reach it over a private network such as Tailscale or WireGuard.

### What JellyTrim does to protect you

- **Cross-origin protection.** JellyTrim rejects state-changing requests that come from another website (using Go's `http.CrossOriginProtection`). A page you visit elsewhere cannot use your browser to change JellyTrim's settings.
- **The Jellyfin API key stays on the server.** It is stored in the SQLite database in `/config`, with file permissions `0600`. It is never shown in the web interface (Settings shows only that a key is saved), never written to logs, and never included in error messages or job diagnostics. Artwork is fetched by JellyTrim through its own `/img/{id}` endpoint, so the browser never receives the key.
- **Environment and secret files.** The key can instead be supplied with `JELLYTRIM_JELLYFIN_API_KEY_FILE`, for example from a Docker secret.
- **No shell.** ffmpeg and ffprobe are run with an explicit list of arguments, never through a shell. File names, titles and other metadata from Jellyfin or from media files are never run as commands. Paths are passed to ffmpeg with a `file:` prefix so a file name cannot be read as an option or protocol.
- **Path boundary.** JellyTrim only reads and writes inside the local folders you configure in path mapping. It resolves symlinks and refuses anything outside those folders. Items are looked up by ID, never by a path sent from the browser.
- **No calls home.** JellyTrim has no telemetry, no update checks and no accounts. The only server it talks to is your Jellyfin.

### What JellyTrim needs

- **A Jellyfin API key.** Create a dedicated key for JellyTrim (in Jellyfin: Dashboard, then API Keys), so you can revoke it without affecting anything else. JellyTrim reads library data and asks Jellyfin to rescan changed files. It does not change Jellyfin's settings or metadata.
- **Write access to your media.** JellyTrim replaces files, so the container user must be able to write to the media folders. Give it access only to the folders it should manage.
- **Access to `/dev/dri`** if you use Intel Quick Sync. Pass through only the render device you need.

### Out of scope

- Attacks by someone who already has access to the JellyTrim web interface. Without a login, that access is full access by design. Protect it at the network or proxy level.
- Attacks by someone with shell access to the host or the container.
- Vulnerabilities in Jellyfin or ffmpeg themselves. Please report those to their projects. We do want to hear if JellyTrim uses them in an unsafe way.
