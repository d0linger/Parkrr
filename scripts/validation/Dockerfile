# Isolated Linux validation toolchain, never the production runtime image.
# PostgreSQL clients match the application's supported PostgreSQL 16 server.
FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf
RUN apk add --no-cache build-base git ca-certificates postgresql16-client tzdata
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
ENV GOTOOLCHAIN=local
# Mount only the repository read-only; build/test artifacts stay in the container.
CMD ["go", "test", "-p", "1", "-race", "-tags=integration", "./..."]
