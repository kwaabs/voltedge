# deploy/migrate/Dockerfile -- one-shot migration-runner image.
#
# On Docker Compose the migrations run from the gitbash sidecar with the repo
# bind-mounted. Kubernetes (and Coolify's build pipeline) can't bind-mount the
# repo, so this image BAKES the migration SQL + the apply.sh runner together
# with the psql client it needs. Run it as a Kubernetes Job (or an
# initContainer) with DATABASE_URL set; apply.sh is idempotent -- it skips any
# migration already recorded in ops.migration_log, so re-running is safe.
#
# Build from the REPO ROOT so the COPY sees ./migrations:
#   docker build -f deploy/migrate/Dockerfile -t <registry>/voltedge-migrate:TAG .
#
# The postgresql15-client package also carries pg_dump / pg_restore, so this
# same image doubles as the seed-restore tool (see the deployment guide).

FROM alpine:3.20
RUN apk add --no-cache bash coreutils postgresql15-client
WORKDIR /migrations
COPY migrations/ /migrations/
# apply.sh resolves its own directory, iterates *.sql in filename order, and
# records each in ops.migration_log. DATABASE_URL must be provided at runtime.
ENTRYPOINT ["bash", "/migrations/apply.sh"]
