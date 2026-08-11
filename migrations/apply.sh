#!/usr/bin/env bash
#
# migrations/apply.sh
#
# Manual migration runner: applies every .sql file in this directory, in
# filename order, that isn't already recorded in ops.migration_log. Meant
# to run from the gitbash sidecar, which has psql + sha256sum installed.
#
# Usage (from the repo root, inside the gitbash container):
#   DATABASE_URL=postgres://postgres:voltedge_dev@postgres:5432/postgres \
#     ./migrations/apply.sh
#
# Or via docker compose directly from the host:
#   docker compose exec -e DATABASE_URL=postgres://postgres:voltedge_dev@postgres:5432/postgres \
#     gitbash bash /workspace/migrations/apply.sh

set -euo pipefail

MIGRATIONS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${DATABASE_URL:?DATABASE_URL is not set}"

# ops.migration_log is itself created by 0002_create_migration_log.sql, so
# every check below has to tolerate that table not existing yet.
log_table_exists() {
    psql "$DATABASE_URL" -tAc "SELECT to_regclass('ops.migration_log') IS NOT NULL;"
}

already_applied() {
    local filename="$1"
    psql "$DATABASE_URL" -tAc \
        "SELECT EXISTS (SELECT 1 FROM ops.migration_log WHERE filename = '${filename}');"
}

for file in "$MIGRATIONS_DIR"/*.sql; do
    filename="$(basename "$file")"

    if [ "$(log_table_exists)" = "t" ] && [ "$(already_applied "$filename")" = "t" ]; then
        echo "skip   $filename (already applied)"
        continue
    fi

    description="$(sed -n 's/^-- Purpose: //p' "$file" | head -n1)"
    [ -n "$description" ] || description="(no description found)"
    checksum="$(sha256sum "$file" | cut -d' ' -f1)"

    echo "apply  $filename"
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$file"

    if [ "$(log_table_exists)" = "t" ]; then
        escaped_description="${description//\'/\'\'}"
        psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c \
            "INSERT INTO ops.migration_log (filename, description, checksum_sha256) VALUES ('${filename}', '${escaped_description}', '${checksum}');"
    fi
done

echo "done."
