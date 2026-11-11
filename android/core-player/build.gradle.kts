// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Playback (brief §7.2): one Media3 MediaLibraryService for background play,
// the notification and lock screen, headset and Bluetooth buttons, and later
// Android Auto. Also the book timeline, sleep timer and progress
// reporting shared by every controller.

plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
    alias(libs.plugins.hilt)
}

android {
    namespace = "org.simpleapb.core.player"
    compileSdk = 37
    defaultConfig {
        minSdk = 26
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    api(project(":core-data"))
    implementation(project(":core-ui"))
    api(libs.media3.session)
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.exoplayer.hls)
    implementation(libs.media3.datasource.okhttp)
    implementation(libs.kotlinx.coroutines.guava)
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.datastore.preferences)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.okhttp)
    implementation(libs.hilt.android)
    ksp(libs.hilt.compiler)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.androidx.work.testing)
}
