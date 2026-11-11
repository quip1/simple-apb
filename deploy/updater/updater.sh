#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# The updater (docs/UPDATING.md, ADR-0049): runs beside the server in its own
# small container (docker compose --profile updater) and carries out the
# admin panel's "Update now".
#
# The server and this script share a folder (/updater here, config/updater
# for the server):
#   heartbeat     touched here every few seconds, so the server knows we run
#   request.json  written by the server: {"from": "...", "to": "X.Y.Z", ...}
#   running.json  the request while it is carried out
#   result.json   the outcome: succeeded, rolled_back or failed
#
# An update: pin SIMPLEAPB_VERSION=X.Y.Z in .env, pull, recreate only the
# server, and wait for its healthcheck. If it doesn't become healthy (say,
# the new version's database migration failed), go back to the version that
# was running and report "rolled_back". The server takes a database backup
# before it asks.

set -u

DIR=/updater
PROJECT=${SIMPLEAPB_DEPLOY_DIR:-}
SERVICE=simpleapb
TIMEOUT=${SIMPLEAPB_UPDATE_TIMEOUT:-300}

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

if [ -z "$PROJECT" ] || [ ! -f "$PROJECT/docker-compose.yml" ] || [ ! -f "$PROJECT/.env" ]; then
  log "SIMPLEAPB_DEPLOY_DIR must be the absolute path of the folder with docker-compose.yml and .env (mounted at the same path)."
  exit 1
fi

compose() { docker compose --project-directory "$PROJECT" -f "$PROJECT/docker-compose.yml" --env-file "$PROJECT/.env" "$@"; }

# JSON-safe one-line text for result.json.
clean() { printf '%s' "$1" | tr '\n\r\t' '   ' | sed 's/[\\"]/ /g' | cut -c1-600; }

result() { # status message from to
  printf '{"from":"%s","to":"%s","status":"%s","message":"%s","at":"%s"}\n' \
    "$(clean "$3")" "$(clean "$4")" "$1" "$(clean "$2")" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$DIR/.result.tmp"
  mv "$DIR/.result.tmp" "$DIR/result.json"
  log "update $3 -> $4: $1 $2"
}

set_version() {
  f="$PROJECT/.env"
  if grep -q '^SIMPLEAPB_VERSION=' "$f"; then
    sed -i "s/^SIMPLEAPB_VERSION=.*/SIMPLEAPB_VERSION=$1/" "$f"
  else
    printf '\nSIMPLEAPB_VERSION=%s\n' "$1" >>"$f"
  fi
}

container() { compose ps -q "$SERVICE" 2>/dev/null | head -n 1; }

wait_healthy() {
  waited=0
  while [ "$waited" -lt "$TIMEOUT" ]; do
    id=$(container)
    if [ -n "$id" ]; then
      case "$(docker inspect -f '{{.State.Health.Status}}' "$id" 2>/dev/null)" in
        healthy) return 0 ;;
      esac
    fi
    sleep 5
    waited=$((waited + 5))
  done
  return 1
}

run_update() {
  req="$DIR/running.json"
  to=$(sed -n 's/.*"to":"\([^"]*\)".*/\1/p' "$req")
  from=$(sed -n 's/.*"from":"\([^"]*\)".*/\1/p' "$req")
  if ! printf '%s' "$to" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
    result failed "not a version: $to" "$from" "$to"
    return
  fi
  id=$(container)
  if [ -z "$id" ]; then
    result failed "the server container isn't running" "$from" "$to"
    return
  fi
  image=$(docker inspect -f '{{.Config.Image}}' "$id")
  repo=${image%:*}
  # Where to go back to: the version the running image says it is (release
  # images carry it), else a local tag of the exact image that runs now.
  prev=$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$id" 2>/dev/null)
  if ! printf '%s' "$prev" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+'; then
    prev="rollback"
    docker tag "$(docker inspect -f '{{.Image}}' "$id")" "$repo:rollback"
  fi
  [ -n "$from" ] || from=$prev

  log "updating $from -> $to"
  set_version "$to"
  if ! out=$(compose pull "$SERVICE" 2>&1); then
    set_version "$prev"
    result failed "couldn't download $to: $(printf '%s' "$out" | tail -n 3)" "$from" "$to"
    return
  fi
  if out=$(compose up -d --no-deps "$SERVICE" 2>&1) && wait_healthy; then
    result succeeded "" "$from" "$to"
    return
  fi

  # Not healthy: keep its last words, then go back.
  logs=$(docker logs --tail 15 "$(container)" 2>&1)
  log "$to didn't become healthy; going back to $prev"
  set_version "$prev"
  if compose up -d --no-deps "$SERVICE" >/dev/null 2>&1 && wait_healthy; then
    result rolled_back "$to didn't start: $logs" "$from" "$to"
  else
    result failed "$to didn't start, and going back to $prev didn't either: $logs" "$from" "$to"
  fi
}

mkdir -p "$DIR"
# The server (uid 10001 in its image) writes requests here.
chown "${SIMPLEAPB_SERVER_UID:-10001}:${SIMPLEAPB_SERVER_UID:-10001}" "$DIR"
chmod 0770 "$DIR"
# A request interrupted by a restart of this container is reported, not retried.
if [ -f "$DIR/running.json" ]; then
  result failed "the updater stopped during the update" "" "$(sed -n 's/.*"to":"\([^"]*\)".*/\1/p' "$DIR/running.json")"
  rm -f "$DIR/running.json"
fi
log "updater ready for $PROJECT"
while true; do
  touch "$DIR/heartbeat"
  if [ -f "$DIR/request.json" ]; then
    mv "$DIR/request.json" "$DIR/running.json"
    run_update
    rm -f "$DIR/running.json"
  fi
  sleep 5
done
