# Releases and updates

Every part of Simple APB (server, web app, Android app, Windows app) shares one version number. A
release is a git tag `vX.Y.Z`. Pushing the tag builds and publishes everything, and every
installed copy can then update to it. The design is in ADR-0049 (`docs/DECISIONS.md`).

## What a release publishes

The **Release** workflow (`.github/workflows/release.yml`) runs on the tag and publishes:

| What | Where | How it updates |
|---|---|---|
| Server image | `ghcr.io/quip1/simple-apb-server:X.Y.Z` (also `X.Y` and `latest`) | Admin → Updates → **Update now**, or two commands (below) |
| Android app | `simple-apb-android-X.Y.Z.apk` on the GitHub release | The app offers it on Home and in Settings; it installs over the old one |
| Preset Android app | `simple-apb-android-X.Y.Z-preset.apk` (only with the `SIMPLEAPB_PRESET_SERVER` secret) | Like the Android app, from the preset APK; see `docs/ANDROID-RELEASE.md` |
| Play bundle | `simple-apb-android-X.Y.Z-play.aab` on the GitHub release | Upload it in the Play Console |
| Windows app | `simple-apb-windows-X.Y.Z-setup.exe` on the GitHub release | The app shows "X.Y.Z is available"; run the new installer over the old one |

The release notes come from the version's section in `CHANGELOG.md`.

## One-time setup

1. **Android signing keys:** follow `docs/ANDROID-RELEASE.md`, steps 1 and 3. Without them a
   release has no APK, and a phone only accepts an update signed with the same key as the
   installed app.
2. **The server image must be public.** After the first release, on GitHub go to your profile →
   Packages → `simple-apb-server` → Package settings → Change visibility → Public. (Otherwise every
   server has to `docker login ghcr.io` first.)

## Making a release

From a clean `main` where CI passes:

```bash
node scripts/release.mjs 0.5.0
```

That sets 0.5.0 everywhere (web, Android versionName and versionCode, the Windows app, the lock
files) and adds a dated `## [0.5.0]` heading under `[Unreleased]` in the changelog. Read the
changelog section over, then:

```bash
git commit -am "chore: release 0.5.0"
git tag v0.5.0
git push origin main v0.5.0
```

The workflow first checks that every part says 0.5.0 (`node scripts/release.mjs 0.5.0 --check`),
so a tag that doesn't match the code fails before anything is published. A version with a
suffix (`0.6.0-rc.1`) is published as a pre-release. Servers and apps don't offer pre-releases.

## Installing the server from a release

On the server (Debian with Docker and the compose plugin; `deploy/proxmox/README.md` covers the VM):

```bash
git clone https://github.com/quip1/simple-apb.git /opt/simpleapb
cd /opt/simpleapb/deploy
cp .env.example .env
nano .env           # SIMPLEAPB_LIBRARY, NTFY_PUBLIC_URL, SIMPLEAPB_VERSION=0.5.0, SIMPLEAPB_DEPLOY_DIR=/opt/simpleapb/deploy
docker compose --profile updater up -d
docker compose logs simpleapb | grep setup_token
```

Open the server in a browser and finish the setup wizard with that token. `deploy/npm/README.md`
covers the reverse proxy.

Leave out `--profile updater` if you'd rather not run the updater (see below).

## Updating the server

**One click (with the updater).** Admin → Updates shows the running version and the newest
release with its notes. The server checks every day, and "Check now" asks at once. **Update now**:
1. backs up the database (the Backups page lists it);
2. tells the updater container, which pins `SIMPLEAPB_VERSION` in `.env`, pulls the image and
   recreates only the server;
3. waits up to 5 minutes (`SIMPLEAPB_UPDATE_TIMEOUT`) for the server's health check. If the new
   version doesn't come up (for example, its database migration failed), the updater puts the
   previous version back and the page says so, with the new version's last log lines.

The updater needs the Docker socket, which is as powerful as root on that machine. It has no
network of its own and does only this one thing (`deploy/updater/updater.sh`), but if you don't
want that, skip it and update by hand.

**By hand.** In the deploy folder:

```bash
sed -i 's/^SIMPLEAPB_VERSION=.*/SIMPLEAPB_VERSION=0.5.0/' .env
docker compose pull simpleapb && docker compose up -d simpleapb
```

Take a backup first (Admin → Backups → Back up now). To go back, put the previous version in
`.env` and run the second line again. Database migrations only add things, so an older version
starts on a newer database unless a release note says otherwise.

To update the updater itself and ntfy, use `git pull` in `/opt/simpleapb`, then
`docker compose --profile updater up -d`.

## Updating the apps

- **Android (the GitHub APK):** about twice a day, the app looks for a newer release. Home then
  shows "Simple APB X.Y.Z is available" with Download and Not now. Settings → App updates shows
  the version and can check by hand. Download opens the APK in the browser; open it to install
  over the current app. Your sign-in, downloads and settings stay. The Play and F-Droid builds get
  their updates from their stores and never check.
- **Windows:** the app checks at start. A bar offers the release page, where the new installer
  runs over the old one. Settings → This computer can check by hand. Automatic installs come
  with an update-signing key in a later release.
- **Old apps and a new server:** the server's `/instance` says which app versions it needs
  (`min_client_version`). An app below it shows "Update required", and one below the
  recommended version shows a softer notice. For now both are 0.0.0 (every version works);
  they're raised only when a release really needs it.

## Building from source instead

The server image can be built from the checked-out code:

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

A server built that way reports version `dev` and isn't offered updates.
