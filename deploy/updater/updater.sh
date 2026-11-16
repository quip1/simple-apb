#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# The updater (docs/UPDATING.md, ADR-0049, ADR-0052): runs beside the server
# in its own small container and carries out the admin panel's "Update now".
# It needs no settings: it finds its compose project (folder, files, .env)
# from the labels docker compose puts on its own container.
#
# The server and this script share a folder (/updater here, config/updater
# for the server):
#   heartbeat     touched here every few seconds, so the server knows we run
#   request.json  written by the server: {"from": "...", "to": "X.Y.Z", ...}
#   running.json  the request while it is carried out
#   result.json   the outcome: succeeded, rolled_back or failed
#
# An update:
#   1. fetch the new release's deploy files (docker-compose.yml, this script),
#      keeping the old ones (not for a git checkout, which git updates);
#   2. pin SIMPLEAPB_VERSION=X.Y.Z in .env, pull, recreate the server, and
#      wait for its healthcheck;
#   3. healthy: bring the other services up to date, then restart this
#      updater on its new script. Not healthy (say, a database migration
#      failed): put the old files and version back and report "rolled_back".
# The server takes a database backup before it asks.
#
# Compose commands run in a short-lived helper container that mounts the
# project folder at its own path, so relative paths in the compose file mean
# the same host folders as when you run compose yourself.

set -u

DIR=/updater
SERVICE=simpleapb
TIMEOUT=${SIMPLEAPB_UPDATE_TIMEOUT:-300}
# Where release files come from: <base>/v<version>/deploy/<file>. Empty: don't fetch.
FILES=${SIMPLEAPB_RELEASE_FILES-https://raw.githubusercontent.com/quip1/simple-apb}

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

# ── who we are ──
SELF=$(cat /etc/hostname)
label() { docker inspect -f "{{index .Config.Labels \"$1\"}}" "$SELF" 2>/dev/null; }
PROJECT=$(label com.docker.compose.project)
WD=$(label com.docker.compose.project.working_dir)
FILES_LABEL=$(label com.docker.compose.project.config_files)
IMAGE=$(docker inspect -f '{{.Config.Image}}' "$SELF" 2>/dev/null)
if [ -z "$PROJECT" ] || [ -z "$WD" ] || [ -z "$FILES_LABEL" ] || [ -z "$IMAGE" ]; then
  log "This container wasn't started by docker compose, so it can't find its project. Start it with docker compose."
  exit 1
fi
ENVFILE="$WD/.env"
COMPOSE_ARGS="-p $PROJECT"
for f in $(printf '%s' "$FILES_LABEL" | tr ',' ' '); do COMPOSE_ARGS="$COMPOSE_ARGS -f $f"; done

# Runs a shell command in a helper container with the project folder mounted at its own path.
helper() {
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$WD:$WD" -w "$WD" \
    --add-host host.docker.internal:host-gateway --entrypoint /bin/sh "$IMAGE" -c "$1"
}
compose() { helper "docker compose $COMPOSE_ARGS --env-file '$ENVFILE' $*"; }

# JSON-safe one-line text for result.json.
clean() { printf '%s' "$1" | tr '\n\r\t' '   ' | sed 's/[\\"]/ /g' | cut -c1-600; }

result() { # status message from to
  printf '{"from":"%s","to":"%s","status":"%s","message":"%s","at":"%s"}\n' \
    "$(clean "$3")" "$(clean "$4")" "$1" "$(clean "$2")" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$DIR/.result.tmp"
  mv "$DIR/.result.tmp" "$DIR/result.json"
  log "update $3 -> $4: $1 $2"
}

# Pins the version in .env: a new file beside it, renamed over it, same owner and mode.
set_version() {
  helper "own=\$(stat -c %u:%g .env) && mode=\$(stat -c %a .env) &&
    { grep -v '^SIMPLEAPB_VERSION=' .env; echo 'SIMPLEAPB_VERSION=$1'; } >.env.new &&
    chown \$own .env.new && chmod \$mode .env.new && sync && mv .env.new .env && sync" >/dev/null 2>&1
}

container() { docker ps -aq --filter "label=com.docker.compose.project=$PROJECT" --filter "label=com.docker.compose.service=$SERVICE" | head -n 1; }

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

# A git checkout is updated with git; only installs from release files get new files here.
is_checkout() {
  parent=$(dirname "$WD")
  docker run --rm -v "$parent:$parent:ro" --entrypoint /bin/sh "$IMAGE" -c "test -e '$WD/.git' -o -e '$parent/.git'" >/dev/null 2>&1
}

FETCHED=""
# Downloads the release's docker-compose.yml and updater.sh beside the current ones
# (as *.new), checks the compose file, then swaps them in, keeping *.bak.
fetch_files() { # version
  FETCHED=""
  [ -n "$FILES" ] || return 0
  if is_checkout; then
    log "a git checkout: deploy files come from git, not from here"
    return 0
  fi
  base="$FILES/v$1/deploy"
  if ! helper "wget -q -T 30 -O docker-compose.yml.new '$base/docker-compose.yml' && mkdir -p updater && wget -q -T 30 -O updater/updater.sh.new '$base/updater/updater.sh'" >/dev/null 2>&1; then
    helper "rm -f docker-compose.yml.new updater/updater.sh.new" >/dev/null 2>&1
    log "couldn't fetch the deploy files of $1; updating the image only"
    return 0
  fi
  if ! helper "docker compose -p $PROJECT -f docker-compose.yml.new --env-file '$ENVFILE' config -q" >/dev/null 2>&1; then
    helper "rm -f docker-compose.yml.new updater/updater.sh.new" >/dev/null 2>&1
    log "the docker-compose.yml of $1 doesn't work with this .env; updating the image only"
    return 0
  fi
  # The new files take the owner of the ones they replace (the helper runs as root).
  helper "own=\$(stat -c %u:%g docker-compose.yml) && chown \$own docker-compose.yml.new updater/updater.sh.new && sync && \
    cp -p docker-compose.yml docker-compose.yml.bak && mv docker-compose.yml.new docker-compose.yml && \
    { [ -f updater/updater.sh ] && cp -p updater/updater.sh updater/updater.sh.bak || true; } && mv updater/updater.sh.new updater/updater.sh" >/dev/null 2>&1
  FETCHED=yes
}

restore_files() {
  [ -n "$FETCHED" ] || return 0
  helper "mv docker-compose.yml.bak docker-compose.yml; [ -f updater/updater.sh.bak ] && mv updater/updater.sh.bak updater/updater.sh" >/dev/null 2>&1
  FETCHED=""
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
  fetch_files "$to"
  set_version "$to"
  if ! out=$(compose pull "$SERVICE" 2>&1); then
    set_version "$prev"
    restore_files
    result failed "couldn't download $to: $(printf '%s' "$out" | tail -n 3)" "$from" "$to"
    return
  fi
  if out=$(compose up -d --no-deps "$SERVICE" 2>&1) && wait_healthy; then
    # The rest of the stack (ntfy…) follows the new compose file; this updater last.
    others=$(compose config --services 2>/dev/null | grep -v -x -e "$SERVICE" -e updater | tr '\n' ' ')
    [ -n "$others" ] && compose up -d --no-deps $others >/dev/null 2>&1
    result succeeded "" "$from" "$to"
    if [ -n "$FETCHED" ]; then
      # A new script for next time: recreate this container from a detached helper.
      docker run -d --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$WD:$WD" -w "$WD" --entrypoint /bin/sh "$IMAGE" \
        -c "sleep 3; docker compose $COMPOSE_ARGS --env-file '$ENVFILE' up -d --no-deps --force-recreate updater" >/dev/null 2>&1
    fi
    return
  fi

  # Not healthy: keep its last words, then go back.
  logs=$(docker logs --tail 15 "$(container)" 2>&1)
  log "$to didn't become healthy; going back to $prev"
  set_version "$prev"
  restore_files
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
log "updater ready for project $PROJECT in $WD"
while true; do
  touch "$DIR/heartbeat"
  if [ -f "$DIR/request.json" ]; then
    mv "$DIR/request.json" "$DIR/running.json"
    run_update
    rm -f "$DIR/running.json"
  fi
  sleep 5
done
