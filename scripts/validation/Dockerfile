# Isolated Linux validation toolchain, never the production runtime image.
# PostgreSQL clients match the application's supported PostgreSQL 18 server.
FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280
RUN apk add --no-cache build-base git ca-certificates postgresql18-client tzdata
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
ENV GOTOOLCHAIN=local
# Mount only the repository read-only; build/test artifacts stay in the container.
CMD ["go", "test", "-p", "1", "-race", "-tags=integration", "./..."]
