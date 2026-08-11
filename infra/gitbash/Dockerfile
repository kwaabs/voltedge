# infra/gitbash/Dockerfile
#
# An Alpine-based dev shell: git + bash + the Postgres client, mounted
# against the repo root. Stands in for "Git Bash" on machines that don't
# have it installed, and doubles as the runner for migrations/apply.sh
# (it needs psql + sha256sum either way, so one sidecar covers both jobs).
#
# Usage: docker compose exec gitbash bash

FROM alpine:3.20

# postgresql15-client matches the server major version (supabase/postgres
# is Postgres 15) -- Alpine only ships versioned client packages, no
# unversioned "postgresql-client" alias past 3.13.
RUN apk add --no-cache \
        bash \
        git \
        openssh-client \
        curl \
        coreutils \
        grep \
        postgresql15-client

WORKDIR /workspace

# Keeps the container alive so `docker compose exec gitbash bash` has
# something to attach to -- this image is a shell, not a service.
CMD ["sleep", "infinity"]
