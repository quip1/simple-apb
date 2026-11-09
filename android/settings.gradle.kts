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

// core-api is pure JVM (generated client); the rest need the Android SDK.
// wear and cast join as their milestones start.
include(":core-api")
include(":core-ui")
include(":core-data")
include(":core-player")
include(":app")
