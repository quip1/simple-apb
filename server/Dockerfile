# SPDX-License-Identifier: AGPL-3.0-or-later
# Build context: repository root (docker build -f server/Dockerfile .)

# 1. Web app (player + admin panel), embedded into the Go binary.
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
COPY i18n/ /src/i18n/
COPY docs/PRIVACY-TEMPLATE.md /src/docs/PRIVACY-TEMPLATE.md
RUN npx vite build

# 2. Server.
FROM golang:1.27-alpine AS build
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/quip1/simple-apb/server/internal/version.Version=${VERSION}" \
    -o /out/simpleapb ./cmd/simpleapb

FROM alpine:3.22
# ffmpeg/ffprobe: probing and transcoding. postgresql17-client: pg_dump for
# backups when Postgres is used. tzdata/ca-certificates: TLS to SMTP/ntfy/providers.
RUN apk add --no-cache ffmpeg postgresql17-client ca-certificates tzdata \
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
# Docker sends SIGTERM on stop; the server drains requests and jobs, closes the
# DB and removes its lock file. A SIGKILL (or power cut) is detected on the
# next start and triggers the startup check.
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/simpleapb"]
