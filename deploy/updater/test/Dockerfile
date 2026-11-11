# SPDX-License-Identifier: AGPL-3.0-or-later
# A stand-in server for the updater test (test.sh): answers the healthcheck,
# or (BROKEN=1) exits at once, like a server whose migration failed.
FROM busybox:1.37
ARG VERSION=0.0.0
ARG BROKEN=0
LABEL org.opencontainers.image.version=$VERSION
RUN mkdir -p /www/api/v1 && echo '{"status":"ok"}' > /www/api/v1/health && echo "$BROKEN" > /broken
CMD ["sh", "-c", "[ \"$(cat /broken)\" = 1 ] && { echo 'migration 00042 failed'; exit 1; }; exec httpd -f -p 8080 -h /www"]
