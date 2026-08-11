# api/Dockerfile
# Multi-stage build: compile with the full Go toolchain, ship a static
# binary on a minimal base. Nothing here is metadata-aware yet -- that
# starts in Phase 2.

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/server /usr/local/bin/server
EXPOSE 8080
CMD ["server"]
