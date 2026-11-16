#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Installs a Simple APB server with Docker in one go (docs/UPDATING.md):
#
#   curl -fsSL https://raw.githubusercontent.com/quip1/simple-apb/main/deploy/install.sh | sh
#
# It downloads the latest release's deploy files into ./simple-apb (or $SIMPLEAPB_DIR),
# asks for your audiobook folder and how phones reach ntfy, writes .env and starts
# everything. After that, updates are one click in the admin panel (Admin → Updates).
# Run it again in a folder that already has an install and it only starts it.
#
# Settings it would ask for can be given instead: SIMPLEAPB_LIBRARY, NTFY_PUBLIC_URL,
# SIMPLEAPB_VERSION (a release such as 0.6.0; default: the latest). For a mirror or
# fork: SIMPLEAPB_REPO, SIMPLEAPB_IMAGE, SIMPLEAPB_RELEASE_FILES.

set -eu

REPO=${SIMPLEAPB_REPO:-quip1/simple-apb}
FILES=${SIMPLEAPB_RELEASE_FILES:-https://raw.githubusercontent.com/$REPO}
DIR=${SIMPLEAPB_DIR:-./simple-apb}

say() { printf '%s\n' "$*"; }
die() { say "error: $*" >&2; exit 1; }

# Asks on the terminal even when this script comes through a pipe.
ask() { # prompt default
  if [ -r /dev/tty ]; then
    printf '%s' "$1" >/dev/tty
    [ -n "$2" ] && printf ' [%s]' "$2" >/dev/tty
    printf ': ' >/dev/tty
    read -r answer </dev/tty || answer=""
  else
    answer=""
  fi
  [ -n "$answer" ] || answer=$2
  printf '%s' "$answer"
}

fetch() { # url file
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    die "needs curl or wget"
  fi
}

command -v docker >/dev/null 2>&1 || die "Docker isn't installed. Install it first: https://docs.docker.com/engine/install/"
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is missing (the 'docker compose' command)."
docker info >/dev/null 2>&1 || die "can't talk to Docker. Is it running, and may this user use it? (Try with sudo.)"

if [ -f "$DIR/.env" ] && [ -f "$DIR/docker-compose.yml" ]; then
  say "Simple APB is already installed in $DIR; starting it."
  say "To update, use the admin panel: Admin → Updates."
  cd "$DIR" && docker compose up -d
  exit 0
fi

# The release to install.
version=${SIMPLEAPB_VERSION:-}
if [ -z "$version" ]; then
  tmp=$(mktemp)
  fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp" || die "couldn't ask GitHub for the latest release"
  version=$(sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' "$tmp" | head -n 1)
  rm -f "$tmp"
fi
printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || die "not a release version: '$version'"
say "Installing Simple APB $version into $DIR"

library=${SIMPLEAPB_LIBRARY:-$(ask "Your audiobook folder" "/srv/audiobooks")}
[ -d "$library" ] || die "$library isn't a folder"
ntfy=${NTFY_PUBLIC_URL:-$(ask "How phones reach ntfy (push notifications), e.g. https://push.example.com" "http://$(hostname 2>/dev/null || echo localhost):8081")}

mkdir -p "$DIR/updater" "$DIR/data/config/updater"
fetch "$FILES/v$version/deploy/docker-compose.yml" "$DIR/docker-compose.yml" || die "couldn't download the release's docker-compose.yml"
fetch "$FILES/v$version/deploy/updater/updater.sh" "$DIR/updater/updater.sh" || die "couldn't download the release's updater"
fetch "$FILES/v$version/deploy/.env.example" "$DIR/.env.example" || true

umask 077
cat >"$DIR/.env.new" <<ENV
# Written by install.sh. Edit and run \`docker compose up -d\` to apply.

# Your audiobook folder on the host (mounted at /library).
SIMPLEAPB_LIBRARY=$library

# How phones reach ntfy through your reverse proxy (TLS).
NTFY_PUBLIC_URL=$ntfy

# The reverse proxy's IP or subnet (comma-separated), so real client IPs are used.
SIMPLEAPB_TRUSTED_PROXIES=

# The server version. The updater (Admin → Updates) changes it for you.
SIMPLEAPB_VERSION=$version
ENV
# A mirror or fork, if one was given: the image, and where the updater gets release files.
[ -n "${SIMPLEAPB_IMAGE:-}" ] && printf 'SIMPLEAPB_IMAGE=%s\n' "$SIMPLEAPB_IMAGE" >>"$DIR/.env.new"
[ -n "${SIMPLEAPB_RELEASE_FILES:-}" ] && printf 'SIMPLEAPB_RELEASE_FILES=%s\n' "$SIMPLEAPB_RELEASE_FILES" >>"$DIR/.env.new"
mv "$DIR/.env.new" "$DIR/.env"

cd "$DIR"
docker compose up -d
port=$(sed -n 's/^SIMPLEAPB_PORT=//p' .env)
say ""
say "Simple APB $version is starting. Open http://$(hostname 2>/dev/null || echo localhost):${port:-8080} to create the admin account."
say "Its files are in $(pwd). Updates: Admin → Updates → Update now."
