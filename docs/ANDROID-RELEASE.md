# Signing the Android app

The three Android builds are signed differently (ADR-0045):

| Build | Signed by | Where it goes |
|---|---|---|
| `github` | your **release key** | an APK on GitHub releases, for sideloading |
| `play` | your **upload key** (or the release key) | a bundle (AAB) for Google Play, which re-signs it for users with the key it keeps (Play App Signing) |
| `fdroid` | F-Droid | F-Droid builds it from this repository's source and signs it with its own key |

The keys never go into the repository. A build without them is unsigned, which is fine for
testing.

**Keep the release key safe.** Phones only install an update to the `github` APK if it is
signed with the same key. If you lose that key, everyone who sideloaded the app has to uninstall
it, losing its downloads and settings, and install it again. Keep the keystore and its passwords
in your password manager, plus a copy somewhere offline.

## 1. Create the keys (once)

`keytool` comes with the JDK. Run this anywhere outside the repository:

```bash
keytool -genkeypair -keystore simpleapb-release.jks -alias simpleapb -keyalg RSA -keysize 4096 -validity 10000
```

It asks for a password and a name. Any name works, and it's visible to anyone who inspects the APK.

For Google Play, create a separate upload key the same way (`simpleapb-upload.jks`). If an upload
key is lost, Google can reset it; the release key can't be reset. You can also skip the upload key:
the play build then uses the release key.

## 2. Sign on your own computer (optional)

Add these lines to `~/.gradle/gradle.properties` (your home folder, not the repository):

```properties
SIMPLEAPB_KEYSTORE=/path/to/simpleapb-release.jks
SIMPLEAPB_KEYSTORE_PASSWORD=...
SIMPLEAPB_KEY_ALIAS=simpleapb
SIMPLEAPB_KEY_PASSWORD=...
# Optional, for Google Play:
SIMPLEAPB_PLAY_KEYSTORE=/path/to/simpleapb-upload.jks
SIMPLEAPB_PLAY_KEYSTORE_PASSWORD=...
SIMPLEAPB_PLAY_KEY_ALIAS=upload
SIMPLEAPB_PLAY_KEY_PASSWORD=...
```

Environment variables with the same names work too. Then build:

```bash
cd android && ./gradlew :app:assembleGithubRelease :app:bundlePlayRelease
```

## 3. Sign in GitHub Actions

In the repository on GitHub, go to Settings → Secrets and variables → Actions and add these
secrets:

| Secret | Value |
|---|---|
| `SIMPLEAPB_KEYSTORE_BASE64` | the release keystore, base64-encoded: `base64 -w0 simpleapb-release.jks` |
| `SIMPLEAPB_KEYSTORE_PASSWORD` | its password |
| `SIMPLEAPB_KEY_ALIAS` | `simpleapb` |
| `SIMPLEAPB_KEY_PASSWORD` | the key's password (often the same) |
| `SIMPLEAPB_PLAY_KEYSTORE_BASE64` and the three `SIMPLEAPB_PLAY_…` | the same for the upload key (optional) |

On Windows, `certutil -encode` adds header lines; `[Convert]::ToBase64String([IO.File]::ReadAllBytes("simpleapb-release.jks"))`
in PowerShell gives the plain value.

Pushing a tag such as `v0.3.0` then runs the **Android release** workflow. It builds and checks
the signed `github` APK and `play` bundle, plus the unsigned `fdroid` APK, and keeps them as the
run's artifacts. Publishing them on a GitHub release comes with Phase 6.

## F-Droid

F-Droid builds the `fdroid` flavor itself, so it has to build from source with no proprietary
libraries, and give the same APK wherever it's built. CI checks both:

- On every push, `./gradlew :app:checkFdroid` fails if the fdroid build depends on, or contains
  classes of, Google Play Services, Firebase or another proprietary Google library.
- Every night, the fdroid APK is built in two separate checkouts and the two must be
  byte-identical.

The F-Droid metadata (`fastlane/metadata/android/`) and the inclusion request come with Phase 6.
