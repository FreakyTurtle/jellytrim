# Reverse proxy and authentication

**JellyTrim has no login of its own.** Anyone who can reach its web interface can change policies, turn off Dry Run, start jobs and restore backups. Do not expose it to the internet without authentication in front of it. See `SECURITY.md` for the full threat model.

The examples below use placeholder hostnames (`jellytrim.example.com`) and service names (`jellytrim:8080`). Replace them with your own.

## The Host header matters

JellyTrim rejects state-changing requests (anything other than a safe read) that do not look same-origin, using Go's `http.CrossOriginProtection`. It trusts the browser's `Sec-Fetch-Site` header first, when the browser sends one; only when that header is absent does it fall back to comparing the `Origin` header against the request's `Host`. Your reverse proxy must forward the **original Host header the browser used** unchanged to JellyTrim, so the two match on that fallback path. If the proxy rewrites the Host header to something else (for example the upstream's internal name), a browser that omits `Sec-Fetch-Site` will have every form submission and button in JellyTrim's UI rejected as cross-origin.

- **Caddy** forwards the Host header as the client sent it by default; no extra configuration is needed.
- **nginx** does not forward it by default: set `proxy_set_header Host $host;` explicitly. On a non-standard port, use `proxy_set_header Host $http_host;` instead, since `$host` drops the port and the comparison then fails.
- **Traefik** forwards the original Host header by default when using the Docker provider with a `Host()` router rule.

## Caddy, with forward auth to Authelia or Authentik

```
jellytrim.example.com {
	forward_auth authelia:9091 {
		uri /api/verify?rd=https://auth.example.com
		copy_headers Remote-User Remote-Groups Remote-Name Remote-Email
	}
	reverse_proxy jellytrim:8080
}
```

For Authentik's forward-auth endpoint, adjust the `forward_auth` block to Authentik's outpost, for example:

```
jellytrim.example.com {
	forward_auth authentik-outpost:9000 {
		uri /outpost.goauthentik.io/auth/caddy
		copy_headers X-Authentik-Username X-Authentik-Email
	}
	reverse_proxy jellytrim:8080
}
```

## nginx, with `auth_request`

```
server {
	listen 443 ssl;
	server_name jellytrim.example.com;

	location = /auth {
		internal;
		proxy_pass http://authelia:9091/api/verify;
		proxy_set_header X-Original-URL $scheme://$http_host$request_uri;
	}

	location / {
		auth_request /auth;
		proxy_pass http://jellytrim:8080;
		proxy_set_header Host $host;
	}
}
```

### A simpler option: HTTP basic authentication

If a full auth provider is more than you need, nginx's built-in basic auth is enough to keep casual access out:

```
location / {
	auth_basic "JellyTrim";
	auth_basic_user_file /etc/nginx/jellytrim.htpasswd;
	proxy_pass http://jellytrim:8080;
	proxy_set_header Host $host;
}
```

Create the password file with `htpasswd -c /etc/nginx/jellytrim.htpasswd <username>`. Basic auth sends credentials with every request in a way that is easy to relay; still put this behind HTTPS.

## Traefik, with a `forwardAuth` middleware

Docker Compose labels:

```yaml
services:
  jellytrim:
    image: ghcr.io/freakyturtle/jellytrim:latest
    labels:
      traefik.enable: "true"
      traefik.http.routers.jellytrim.rule: Host(`jellytrim.example.com`)
      traefik.http.routers.jellytrim.entrypoints: websecure
      traefik.http.routers.jellytrim.tls.certresolver: myresolver
      traefik.http.routers.jellytrim.middlewares: jellytrim-auth
      traefik.http.middlewares.jellytrim-auth.forwardauth.address: http://authelia:9091/api/verify?rd=https://auth.example.com
      traefik.http.middlewares.jellytrim-auth.forwardauth.trustForwardHeader: "true"
      traefik.http.middlewares.jellytrim-auth.forwardauth.authResponseHeaders: Remote-User,Remote-Groups,Remote-Name,Remote-Email
      traefik.http.services.jellytrim.loadbalancer.server.port: "8080"
```

## Checking it worked

After putting a proxy in front of JellyTrim:

1. Visit the public hostname in a browser; you should be prompted by the auth provider, not by JellyTrim.
2. Once signed in, try an action that changes something, for example toggling a policy. If it fails with an error about the request being rejected, check the Host header is being forwarded unchanged (see above).
3. Confirm the plain container port (`127.0.0.1:8080` if you bound it that way, or the LAN port if not) is not separately reachable in a way that bypasses the proxy.
