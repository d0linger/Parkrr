# syntax=docker/dockerfile:1

# ---------- Build stage ----------
FROM golang:1.27-alpine AS build

WORKDIR /src

# Build tools for module resolution.
RUN apk add --no-cache git ca-certificates

# Resolve dependencies first for better layer caching. No `|| true`: a failed
# module download must fail the build, not silently proceed with an incomplete
# graph (finding CONT-02).
COPY go.mod ./
COPY go.sum* ./
RUN go mod download

# Copy the rest of the source. The build uses the committed go.mod/go.sum as-is —
# no `go mod tidy` here: it would mutate dependency resolution at build time and
# make the image non-reproducible. An inconsistent module graph should fail the
# build (and CI) rather than be silently rewritten (finding CONT-02).
COPY . .

# Build a static binary. Version can be injected at build time.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X github.com/preining/parkrr/internal/server.Version=${VERSION}" \
    -o /out/parkrr ./cmd/parkrr

# ---------- Runtime stage ----------
# Alpine (not distroless) so pg_dump/pg_restore are available for the encrypted
# backup feature; pinned to major 16 to match the postgres:16 server. Runs as a
# dedicated non-root user.
FROM alpine:3.24

# `apk upgrade` first: `apk add` never lifts an already-satisfied base-image
# package, so libcrypto3/libssl3, busybox etc. would otherwise stay on the
# base tag's (older) -rN even when the repo has a patched build. Upgrading all
# base packages before adding the extras keeps the runtime image patch-fresh.
RUN apk upgrade --no-cache \
    && apk add --no-cache postgresql16-client ca-certificates tzdata \
    && adduser -D -H -u 10001 parkrr \
    # Default backup directory, owned by the app user so a named volume mounted
    # here (docker-compose) inherits writable ownership for the non-root process.
    && mkdir -p /backups && chown parkrr /backups

WORKDIR /app
COPY --from=build /out/parkrr /app/parkrr

EXPOSE 8080
USER parkrr

# The binary probes itself (no curl needed).
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/app/parkrr", "healthcheck"]

ENTRYPOINT ["/app/parkrr"]
