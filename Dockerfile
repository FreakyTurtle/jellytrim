# syntax=docker/dockerfile:1

# ---- Build: runs on the build machine's architecture and cross-compiles ----
FROM --platform=$BUILDPLATFORM golang:1.26-trixie AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w \
        -X github.com/freakyturtle/jellytrim/internal/version.Version=${VERSION} \
        -X github.com/freakyturtle/jellytrim/internal/version.Commit=${COMMIT} \
        -X github.com/freakyturtle/jellytrim/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /out/jellytrim ./cmd/jellytrim

# ---- Runtime: Debian with jellyfin-ffmpeg (QSV, VAAPI, NVENC, x265, SVT-AV1) ----
FROM debian:trixie-slim
ARG TARGETARCH
# jellyfin-ffmpeg is pinned by version and checksum (ADR 0004). Update both
# architectures together from https://github.com/jellyfin/jellyfin-ffmpeg/releases,
# and the matching CI package in .github/workflows/ci.yml
ARG JFFMPEG_VERSION=8.1.3-1
ARG JFFMPEG_SHA256_AMD64=fbef9f81a53e175194e3f67832618a86111f7bd37197df21a5d748c34289f9c0
ARG JFFMPEG_SHA256_ARM64=4af743bd776eda082d40c851a4201e51a77a258145dd6ebe164dade39b35b2d0

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl; \
    case "$TARGETARCH" in \
      amd64) sha="$JFFMPEG_SHA256_AMD64" ;; \
      arm64) sha="$JFFMPEG_SHA256_ARM64" ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    deb="jellyfin-ffmpeg8_${JFFMPEG_VERSION}-trixie_${TARGETARCH}.deb"; \
    curl -fsSL -o "/tmp/$deb" "https://github.com/jellyfin/jellyfin-ffmpeg/releases/download/v${JFFMPEG_VERSION}/$deb"; \
    echo "$sha  /tmp/$deb" | sha256sum -c -; \
    apt-get install -y --no-install-recommends "/tmp/$deb"; \
    rm -f "/tmp/$deb"; \
    apt-get purge -y --auto-remove curl; \
    rm -rf /var/lib/apt/lists/*; \
    /usr/lib/jellyfin-ffmpeg/ffmpeg -hide_banner -version | head -1; \
    mkdir -p /config && chmod 0777 /config

COPY --from=build /out/jellytrim /usr/local/bin/jellytrim

ENV JELLYTRIM_LISTEN=:8080 \
    JELLYTRIM_CONFIG_DIR=/config \
    JELLYTRIM_FFMPEG=/usr/lib/jellyfin-ffmpeg/ffmpeg \
    JELLYTRIM_FFPROBE=/usr/lib/jellyfin-ffmpeg/ffprobe

LABEL org.opencontainers.image.title="JellyTrim" \
      org.opencontainers.image.description="Jellyfin-aware media lifecycle optimiser" \
      org.opencontainers.image.source="https://github.com/freakyturtle/jellytrim" \
      org.opencontainers.image.licenses="MIT"

# Runs as an unprivileged user by default. Override with `user: "UID:GID"` in
# compose so JellyTrim can write to your media; /config must be writable by it.
USER 1000:1000
VOLUME ["/config"]
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["jellytrim", "healthcheck"]
ENTRYPOINT ["jellytrim"]
