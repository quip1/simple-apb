#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# End-to-end test of the updater (CI, needs Docker): a local registry with
# three stand-in server images, an install with deploy/install.sh from
# stand-in release files, and two updates: one that works and one that has to
# be rolled back.
set -Eeuo pipefail

here=$(cd "$(dirname "$0")" && pwd)
deploy=$(cd "$here/../.." && pwd)
work=$(mktemp -d)
reg=localhost:5000/sapb-test

# On failure, say where and show the containers' state as CI annotations.
report() {
  trap - ERR
  local line=$1
  # One annotation per piece; newlines become %0A (GitHub's escape for them).
  ann() { echo "::error title=$1::$(printf '%s' "$2" | tail -c 3500 | tr -d '\r' | awk 'BEGIN{ORS="%0A"} {gsub(/%/,"%25"); print}')"; }
  ann "updater test failed at line $line" "$(sed -n "${line}p" "$0")"
  if [ -d "$work/deploy" ]; then
    (cd "$work/deploy" && ann "compose ps" "$(docker compose ps -a 2>&1)")
    (cd "$work/deploy" && ann "updater log" "$(docker compose logs --no-color --tail 40 updater 2>&1)")
    (cd "$work/deploy" && ann "server log" "$(docker compose logs --no-color --tail 20 simpleapb 2>&1)")
    ann "updater folder" "$(sudo ls -la "$work/deploy/data/config/updater" 2>&1; sudo cat "$work/deploy/data/config/updater/result.json" 2>&1)"
  fi
}
trap 'report $LINENO' ERR

cleanup() {
  (cd "$work/deploy" && docker compose down -v >/dev/null 2>&1) || true
  docker rm -f sapb-registry >/dev/null 2>&1 || true
  [ -n "${files_pid:-}" ] && kill "$files_pid" 2>/dev/null || true
}
trap cleanup EXIT

docker run -d --name sapb-registry -p 5000:5000 registry:2 >/dev/null
for v in 1.0.0 1.1.0 1.2.0; do
  broken=0
  [ "$v" = 1.2.0 ] && broken=1
  docker build -q -t "$reg:$v" --build-arg VERSION=$v --build-arg BROKEN=$broken "$here" >/dev/null
  docker push -q "$reg:$v" >/dev/null
  docker rmi "$reg:$v" >/dev/null
done

# Release files, as GitHub would serve them: <base>/v<version>/deploy/…, each
# compose file marked with its version.
for v in 1.0.0 1.1.0 1.2.0; do
  mkdir -p "$work/release/v$v/deploy/updater"
  { cat "$deploy/docker-compose.yml"; echo "# release $v"; } >"$work/release/v$v/deploy/docker-compose.yml"
  cp "$deploy/updater/updater.sh" "$work/release/v$v/deploy/updater/updater.sh"
  cp "$deploy/.env.example" "$work/release/v$v/deploy/.env.example"
done
(cd "$work/release" && python3 -m http.server 8099 --bind 0.0.0.0 >/dev/null 2>&1) &
files_pid=$!

wait_for() { # seconds, condition...
  local t=$1; shift
  for _ in $(seq 1 "$t"); do "$@" && return 0; sleep 1; done
  return 1
}
wait_for 10 curl -fs -o /dev/null http://localhost:8099/v1.0.0/deploy/docker-compose.yml

# Install like a user would, with nothing typed (no terminal): the settings come from the environment.
mkdir -p "$work/library"
SIMPLEAPB_DIR="$work/deploy" SIMPLEAPB_VERSION=1.0.0 SIMPLEAPB_LIBRARY="$work/library"   NTFY_PUBLIC_URL=http://localhost:8081 SIMPLEAPB_IMAGE="$reg" SIMPLEAPB_RELEASE_FILES=http://localhost:8099   sh "$deploy/install.sh" </dev/null
cd "$work/deploy"
grep -q '^# release 1.0.0$' docker-compose.yml || { echo "install.sh didn't fetch 1.0.0's files"; report $LINENO; exit 1; }
# The updater reaches the stand-in release files through the host; quicker timeouts for the test.
sed -i 's|^SIMPLEAPB_RELEASE_FILES=.*|SIMPLEAPB_RELEASE_FILES=http://host.docker.internal:8099|' .env
echo SIMPLEAPB_UPDATE_TIMEOUT=45 >>.env
docker compose up -d --quiet-pull

running_version() { docker inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$(docker compose ps -q simpleapb)"; }
healthy() { [ "$(docker inspect -f '{{.State.Health.Status}}' "$(docker compose ps -q simpleapb)")" = healthy ]; }
result_is() { sudo grep -q "\"status\":\"$1\"" data/config/updater/result.json 2>/dev/null; }

wait_for 120 healthy || { echo "server never became healthy"; report $LINENO; exit 1; }
wait_for 30 sudo test -f data/config/updater/heartbeat || { echo "no updater heartbeat"; report $LINENO; exit 1; }
echo "running $(running_version), updater up"

update() { # to
  # The folder belongs to the server's user; the test acts as root, like the server would as 10001.
  sudo rm -f data/config/updater/result.json
  printf '{"from":"%s","to":"%s","status":"requested","at":"2026-01-01T00:00:00Z"}' "$(running_version)" "$1" |
    sudo tee data/config/updater/request.json >/dev/null
  wait_for 180 sudo test -f data/config/updater/result.json || { echo "no result"; report $LINENO; exit 1; }
  sudo cat data/config/updater/result.json
}

old_updater=$(docker compose ps -q updater)
update 1.1.0
result_is succeeded || { echo "1.1.0 should succeed"; report $LINENO; exit 1; }
[ "$(running_version)" = 1.1.0 ] || { echo "running $(running_version), want 1.1.0"; report $LINENO; exit 1; }
grep -q '^SIMPLEAPB_VERSION=1.1.0$' .env || { echo ".env not pinned to 1.1.0"; report $LINENO; exit 1; }
[ "$(stat -c %U:%a .env)" = "$(id -un):600" ] || { echo ".env changed owner or mode: $(stat -c %U:%a .env)"; report $LINENO; exit 1; }
[ "$(stat -c %U docker-compose.yml)" = "$(id -un)" ] || { echo "docker-compose.yml changed owner"; report $LINENO; exit 1; }
grep -q '^# release 1.1.0$' docker-compose.yml || { echo "1.1.0's docker-compose.yml wasn't fetched"; report $LINENO; exit 1; }
# The updater restarts itself on the new script.
new_updater() { id=$(docker compose ps -q updater); [ -n "$id" ] && [ "$id" != "$old_updater" ] && [ "$(docker inspect -f '{{.State.Running}}' "$id")" = true ]; }
wait_for 60 new_updater || { echo "the updater didn't restart on its new script"; report $LINENO; exit 1; }
sleep 6 # its first heartbeat

update 1.2.0
result_is rolled_back || { echo "1.2.0 should be rolled back"; report $LINENO; exit 1; }
sudo grep -q 'migration 00042 failed' data/config/updater/result.json || { echo "the failure's log is missing"; report $LINENO; exit 1; }
wait_for 60 healthy || { echo "not healthy after the rollback"; report $LINENO; exit 1; }
[ "$(running_version)" = 1.1.0 ] || { echo "running $(running_version) after rollback, want 1.1.0"; report $LINENO; exit 1; }
grep -q '^SIMPLEAPB_VERSION=1.1.0$' .env || { echo ".env not back on 1.1.0"; report $LINENO; exit 1; }
grep -q '^# release 1.1.0$' docker-compose.yml || { echo "docker-compose.yml not put back after the rollback"; report $LINENO; exit 1; }

echo "updater: update (with new deploy files) and rollback work"
