# syntax=docker/dockerfile:1
# ============================================================================
# Stage 1: Build Go Binary (Supports native build + fast amd64 -> arm64 CGO cross-compile)
# ============================================================================
FROM --platform=$BUILDPLATFORM golang:bookworm AS builder

ENV GOTOOLCHAIN=auto
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG BUILDARCH=amd64

# Install arm64 C cross-compiler when building linux/arm64 on an amd64 runner (avoids slow QEMU)
RUN if [ "$TARGETARCH" = "arm64" ] && [ "$BUILDARCH" != "arm64" ]; then \
      apt-get update && apt-get install -y --no-install-recommends \
        gcc-aarch64-linux-gnu libc6-dev-arm64-cross && \
      rm -rf /var/lib/apt/lists/*; \
    fi

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build Linux binary with SQLite3 (CGO) + PostgreSQL (pgx)
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN set -eux; \
    if [ "$TARGETARCH" = "arm64" ] && [ "$BUILDARCH" != "arm64" ]; then \
      export CC=aarch64-linux-gnu-gcc; \
    fi; \
    CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -ldflags="-s -w" -o /out/folio ./cmd/folio

# ============================================================================
# Stage 2: Minimal Debian Bookworm Slim Runtime
# ============================================================================
FROM debian:bookworm-slim

LABEL org.opencontainers.image.title="Folio" \
      org.opencontainers.image.description="Self-hosted EPUB library manager, Web Reader, KOReader/CrossPoint progress sync, OPDS 1.2/2.0 catalog, Tailscale auth & CrossPoint Reader wireless companion" \
      org.opencontainers.image.licenses="MIT"

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/folio /app/folio
COPY web/ /app/web/

VOLUME ["/data"]

ENV PORT=8080 \
    DATA_DIR=/data \
    DB_DRIVER=sqlite \
    DB_DSN=/data/folio.db \
    TAILSCALE_AUTH_MODE=auto \
    TS_SOCKET=/var/run/tailscale/tailscaled.sock \
    TAILSCALE_TRUST_HEADERS=true \
    DEFAULT_SYNC_USER=reader \
    DEFAULT_SYNC_PASSWORD=folio123 \
    REGISTRATION_ENABLED=true \
    SEED_SAMPLE_BOOKS=true \
    ENABLE_MOCK_X3=false

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD curl -fsS http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/folio"]
