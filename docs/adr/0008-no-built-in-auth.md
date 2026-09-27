# 0008. No built-in authentication

Date: 2026-09-27
Status: Accepted

## Context
JellyTrim is a self-hosted container, usually on a home network next to Jellyfin. Self-hosters who expose services already use a reverse proxy with authentication (Authelia, Authentik, Caddy, Tailscale and similar). A built-in login adds setup friction and code to maintain, and duplicates what the proxy does better.

## Decision
JellyTrim has no login. Documentation explains that it should run on a trusted network or behind a reverse proxy that authenticates. JellyTrim still protects against cross-site request forgery with Go's `http.CrossOriginProtection`, never exposes the Jellyfin API key to the browser, and proxies artwork so the browser never talks to Jellyfin.

## Consequences
- Setup is faster and there is no password to lose.
- Anyone who can reach the port can change settings and start jobs. `SECURITY.md` says this plainly.
