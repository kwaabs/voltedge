# api/Dockerfile
# Multi-stage build: compile with the full Go toolchain, ship static
# binaries on a minimal base. One image carries the API server, the export
# worker (Phase 17), the MCP server (Phase 18), and the inbound-webhook and
# ETL-import workers; the compose service picks which to run via its command --
# the default here is the server.
#
# It also carries the two out-of-band admin tools -- authadmin (Phase 10: create
# or reset the break-glass admin) and resetdata (the break-glass factory reset).
# These have no long-running service; they are meant to be invoked ad hoc against
# a live deployment, where there is no Go toolchain / Taskfile / source tree to
# `go run` them from. Baking them into this image means they run with the api
# service's exact environment (DATABASE_URL + RUSTFS_*), e.g.:
#     docker compose exec -e AUTHADMIN_PASSWORD=... api authadmin create --email ...
#     docker compose exec -e RESETDATA_PASSWORD=... api resetdata --admin-email ... --yes
# They add only a couple of MB of static binary and never run unless invoked.

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server
RUN CGO_ENABLED=0 go build -o /out/exportworker ./cmd/exportworker
RUN CGO_ENABLED=0 go build -o /out/mcpserver ./cmd/mcpserver
RUN CGO_ENABLED=0 go build -o /out/webhookworker ./cmd/webhookworker
RUN CGO_ENABLED=0 go build -o /out/importworker ./cmd/importworker
RUN CGO_ENABLED=0 go build -o /out/validationworker ./cmd/validationworker
RUN CGO_ENABLED=0 go build -o /out/authadmin ./cmd/authadmin
RUN CGO_ENABLED=0 go build -o /out/resetdata ./cmd/resetdata

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/exportworker /usr/local/bin/exportworker
COPY --from=build /out/mcpserver /usr/local/bin/mcpserver
COPY --from=build /out/webhookworker /usr/local/bin/webhookworker
COPY --from=build /out/importworker /usr/local/bin/importworker
COPY --from=build /out/validationworker /usr/local/bin/validationworker
COPY --from=build /out/authadmin /usr/local/bin/authadmin
COPY --from=build /out/resetdata /usr/local/bin/resetdata
EXPOSE 8080
CMD ["server"]
