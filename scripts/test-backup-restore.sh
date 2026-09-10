#!/usr/bin/env bash
# CI-only: exercise the PostgreSQL 16 restore path under hardened storage limits.
set -euo pipefail
if [[ "${GITHUB_ACTIONS:-false}" != true ]]; then
  echo "This script is reserved for the disposable GitHub Actions PostgreSQL service." >&2
  exit 1
fi
test_dir=$(mktemp -d)
trap 'rm -f "$test_dir/backup.test"; rmdir "$test_dir"' EXIT
CGO_ENABLED=0 GOOS=linux go test -c -o "$test_dir/backup.test" ./internal/backup
# Refuse an existing database; never reuse or erase data.
docker run --rm --network host -e PGPASSWORD=parkrr postgres:16-alpine \
  createdb -h 127.0.0.1 -U parkrr parkrr_test_backup
docker run --rm --network host --memory 256m --tmpfs /tmp:rw,size=32m \
  --mount "type=bind,source=$test_dir,target=/audit,readonly" \
  -e GOMEMLIMIT=200MiB \
  -e 'PARKRR_BACKUP_TEST_DATABASE_URL=postgres://parkrr:parkrr@127.0.0.1:5432/parkrr_test_backup?sslmode=disable' \
  postgres:16-alpine /audit/backup.test \
  -test.run=TestRestoreLargeArchiveWithoutTemporaryStorage -test.v -test.timeout=90s
