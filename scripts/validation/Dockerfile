# Isolated Linux validation toolchain, never the production runtime image.
# PostgreSQL clients match the application's supported PostgreSQL 16 server.
FROM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c
RUN apk add --no-cache build-base git ca-certificates postgresql16-client tzdata
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
ENV GOTOOLCHAIN=local
# Mount only the repository read-only; build/test artifacts stay in the container.
CMD ["go", "test", "-p", "1", "-race", "-tags=integration", "./..."]
