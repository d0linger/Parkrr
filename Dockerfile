# syntax=docker/dockerfile:1

# ---------- Build stage ----------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Build tools for module resolution.
RUN apk add --no-cache git ca-certificates

# Resolve dependencies first for better layer caching.
COPY go.mod ./
COPY go.sum* ./
RUN go mod download || true

# Copy the rest of the source and ensure the module graph is complete.
COPY . .
RUN go mod tidy

# Build a static binary. Version can be injected at build time.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X github.com/preining/parkrr/internal/server.Version=${VERSION}" \
    -o /out/parkrr ./cmd/parkrr

# ---------- Runtime stage ----------
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/parkrr /app/parkrr

EXPOSE 8080
USER nonroot:nonroot

# The distroless image has no shell/curl, so the binary probes itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/app/parkrr", "healthcheck"]

ENTRYPOINT ["/app/parkrr"]
