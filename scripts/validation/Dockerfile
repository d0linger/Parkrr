# Isolated Linux validation toolchain, never the production runtime image.
# PostgreSQL clients match the application's supported PostgreSQL 16 server.
FROM golang:1.26.10-alpine3.24@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca
RUN apk add --no-cache build-base git ca-certificates postgresql16-client tzdata
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
ENV GOTOOLCHAIN=local
# Mount only the repository read-only; build/test artifacts stay in the container.
CMD ["go", "test", "-p", "1", "-race", "-tags=integration", "./..."]
