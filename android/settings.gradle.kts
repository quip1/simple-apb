// SPDX-License-Identifier: AGPL-3.0-or-later

pluginManagement {
    repositories {
        gradlePluginPortal()
        google()
        mavenCentral()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "simple-apb"

// Phase 0: only the pure-JVM generated API client is a real module.
// app, wear, core-player, core-data, core-ui and cast join in Phase 3/5
// (they need the Android SDK; see scripts/build-android.*).
include(":core-api")
