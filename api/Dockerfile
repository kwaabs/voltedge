# api/Dockerfile
# Multi-stage build: compile with the full Go toolchain, ship static
# binaries on a minimal base. One image carries the API server, the export
# worker (Phase 17), and the MCP server (Phase 18); the compose service picks
# which to run via its command -- the default here is the server.

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server
RUN CGO_ENABLED=0 go build -o /out/exportworker ./cmd/exportworker
RUN CGO_ENABLED=0 go build -o /out/mcpserver ./cmd/mcpserver

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/exportworker /usr/local/bin/exportworker
COPY --from=build /out/mcpserver /usr/local/bin/mcpserver
EXPOSE 8080
CMD ["server"]
