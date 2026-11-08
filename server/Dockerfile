# SPDX-License-Identifier: AGPL-3.0-or-later
# Build context: repository root (docker build -f server/Dockerfile .)
# Phase 0 skeleton; the web SPA build stage is added in Phase 2.

FROM golang:1.27-alpine AS build
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/quip1/simple-apb/server/internal/version.Version=${VERSION}" \
    -o /out/simpleapb ./cmd/simpleapb

FROM alpine:3.22
RUN apk add --no-cache ffmpeg ca-certificates tzdata \
 && addgroup -S -g 10001 simpleapb \
 && adduser -S -D -H -u 10001 -G simpleapb simpleapb \
 && mkdir -p /config /library /cache /backups \
 && chown simpleapb:simpleapb /config /cache /backups
COPY --from=build /out/simpleapb /usr/local/bin/simpleapb
USER 10001:10001
VOLUME ["/config", "/library", "/cache", "/backups"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/v1/health || exit 1
ENTRYPOINT ["/usr/local/bin/simpleapb"]
