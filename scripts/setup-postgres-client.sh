#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_PATH:?GITHUB_PATH must be set}"

# Use the official signed PGDG source and versioned binaries on GitHub runners.
key=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc
sudo install -d /usr/share/postgresql-common/pgdg
sudo curl --fail --silent --show-error --location --retry 5 --retry-all-errors \
  https://www.postgresql.org/media/keys/ACCC4CF8.asc -o "$key"
source /etc/os-release
printf 'deb [signed-by=%s] https://apt.postgresql.org/pub/repos/apt %s-pgdg main\n' \
  "$key" "$VERSION_CODENAME" | sudo tee /etc/apt/sources.list.d/parkrr-pgdg.list >/dev/null
sudo apt-get -o Acquire::Retries=5 update
sudo apt-get -o Acquire::Retries=5 install --yes --no-install-recommends postgresql-client-18

pg_bin=/usr/lib/postgresql/18/bin
for tool in pg_dump pg_restore psql; do
  version=$("$pg_bin/$tool" --version)
  if [[ "$version" != *" 18."* ]]; then
    echo "$tool does not match PostgreSQL 18" >&2
    exit 1
  fi
done
echo "$pg_bin" >> "$GITHUB_PATH"
