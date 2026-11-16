# Releases and updates

Every part of Simple APB (server, web app, Android app, Windows app) shares one version number. A
release is a git tag `vX.Y.Z`. Pushing the tag builds and publishes everything, and every
installed copy can then update to it. The design is in ADR-0049 and ADR-0052 (`docs/DECISIONS.md`).

## What a release publishes

The **Release** workflow (`.github/workflows/release.yml`) runs on the tag and publishes:

| What | Where | How it updates |
|---|---|---|
| Server image | `ghcr.io/quip1/simple-apb-server:X.Y.Z` (also `X.Y` and `latest`) | Admin → Updates → **Update now**, or two commands (below) |
| Server deploy files | `deploy/` at the tag (`docker-compose.yml`, `updater/updater.sh`, `.env.example`) | Fetched by `install.sh` and by the updater |
| Android app | `simple-apb-android-X.Y.Z.apk` on the GitHub release | **Update now** in the app downloads and installs it over the old one |
| Preset Android app | `simple-apb-android-X.Y.Z-preset.apk` (only with the `SIMPLEAPB_PRESET_SERVER` secret) | Like the Android app, from the preset APK; see `docs/ANDROID-RELEASE.md` |
| Play bundle | `simple-apb-android-X.Y.Z-play.aab` on the GitHub release | Upload it in the Play Console |
| Windows app | `simple-apb-windows-X.Y.Z-setup.exe`, its `.sig` and `latest.json` on the GitHub release | **Update now** in the app installs it and restarts |

The release notes come from the version's section in `CHANGELOG.md`.

## One-time setup

1. **Android signing keys:** follow `docs/ANDROID-RELEASE.md`, steps 1 and 3. Without them a
   release has no APK, and a phone only accepts an update signed with the same key as the
   installed app.
2. **Windows updater key:** the secrets `TAURI_SIGNING_PRIVATE_KEY` (the whole private key file)
   and `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`. Its public key is in
   `desktop/src-tauri/tauri.conf.json`; the app installs only installers signed with the private
   one. Without them a release has no `latest.json`, and the Windows app sends people to the
   release page instead. Keep the private key safe: a new one means a new public key, and apps
   built with the old one can't update by themselves to releases signed with the new one.
3. **The server image must be public.** After the first release, on GitHub go to your profile →
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

## Installing the server

On a Linux machine with Docker and its compose plugin (`deploy/proxmox/README.md` covers a VM):

```bash
curl -fsSL https://raw.githubusercontent.com/quip1/simple-apb/main/deploy/install.sh | sh
```

`deploy/install.sh` downloads the newest release's deploy files into `./simple-apb` (another
folder: `SIMPLEAPB_DIR=/opt/simpleapb`), asks for your audiobook folder and how phones reach ntfy,
writes `.env` and starts everything, the updater included. Then:

```bash
cd simple-apb && docker compose logs simpleapb | grep setup_token
```

Open the server in a browser and finish the setup wizard with that token. `deploy/npm/README.md`
covers the reverse proxy. Run the script again later and it only starts what's there; it never
overwrites `.env`.

A git checkout works too (`git clone`, then in `deploy/`: `cp .env.example .env`, edit it,
`docker compose up -d`). The updater then leaves the deploy files to `git pull`.

## Updating the server

**One click.** Admin → Updates shows the running version and the newest release with its notes.
The server checks every day, and "Check now" asks at once. **Update now**:
1. backs up the database (the Backups page lists it);
2. tells the updater container, which fetches the release's `docker-compose.yml` and
   `updater.sh` (keeping the old ones), pins `SIMPLEAPB_VERSION` in `.env`, pulls the image and
   recreates the server;
3. waits up to 5 minutes (`SIMPLEAPB_UPDATE_TIMEOUT`) for the server's health check. If the new
   version doesn't come up (for example, its database migration failed), the updater puts the
   previous version and files back and the page says so, with the new version's last log lines;
4. once it's healthy, brings ntfy up to date and restarts itself on its new script.

Nothing is removed first: the old container keeps running until the new one replaces it, and
your data, settings and `.env` stay where they are.

The updater runs by default and needs no settings: it finds its folder from the labels Docker
Compose puts on its container. It needs the Docker socket, which is as powerful as root on that
machine. It has no network of its own (its short-lived helper downloads the release files from
GitHub) and does only this one thing (`deploy/updater/updater.sh`). If you'd rather not run it,
`docker compose stop updater` (or delete the service) and update by hand.

**By hand.** In the deploy folder:

```bash
sed -i 's/^SIMPLEAPB_VERSION=.*/SIMPLEAPB_VERSION=0.5.0/' .env
docker compose pull simpleapb && docker compose up -d simpleapb
```

Take a backup first (Admin → Backups → Back up now). To go back, put the previous version in
`.env` and run the second line again. Database migrations only add things, so an older version
starts on a newer database unless a release note says otherwise.

## Updating the apps

Every app updates over the installed one: sign-in, downloads and settings stay.

- **Android (the GitHub and preset APKs):** about twice a day, the app looks for a newer release.
  Home shows "Simple APB X.Y.Z is available" with **Update now** and Not now (also in local-only
  mode); Settings → App updates shows the version and can check by hand. Update now downloads
  the APK inside the app, checks it is this app, newer, and signed with the same key, and hands it
  to Android's installer. The first time, Android asks to allow installing apps from Simple APB;
  after that it's one tap, and on Android 12+ updates after the first in-app one need no tap at
  all. If something fails, the release page is offered. The Play and F-Droid builds get their
  updates from their stores and never check.
- **Windows:** the app checks at start, and Settings → This computer can check by hand. **Update
  now** downloads the release's installer, checks its signature against the key built into the
  app, and runs it with only a progress bar; the app closes and reopens on the new version.
  Releases made without the updater key (0.5.3 and older) can't be installed this way; the app
  then offers the release page.
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
