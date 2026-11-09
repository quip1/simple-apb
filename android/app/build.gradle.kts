// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The phone app (brief §7). Flavors (§7.1):
//   play   — Google Cast, no in-app update check
//   github — Google Cast and the in-app update check (sideloaded APKs)
//   fdroid — no Google libraries at all, no Cast, no update check

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
    alias(libs.plugins.hilt)
}

android {
    namespace = "org.simpleapb.app"
    compileSdk = 37

    defaultConfig {
        applicationId = "org.simpleapb.app"
        minSdk = 26
        targetSdk = 37
        versionCode = 1
        versionName = "0.3.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    flavorDimensions += "distribution"
    productFlavors {
        create("play") {
            dimension = "distribution"
            buildConfigField("boolean", "CAST", "true")
            buildConfigField("boolean", "UPDATE_CHECK", "false")
        }
        create("github") {
            dimension = "distribution"
            buildConfigField("boolean", "CAST", "true")
            buildConfigField("boolean", "UPDATE_CHECK", "true")
        }
        create("fdroid") {
            dimension = "distribution"
            buildConfigField("boolean", "CAST", "false")
            buildConfigField("boolean", "UPDATE_CHECK", "false")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
        debug {
            applicationIdSuffix = ".debug"
        }
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
    lint {
        warningsAsErrors = true
        abortOnError = true
        checkDependencies = true
    }
    // Reproducible builds for F-Droid: no dependency metadata blob in the APK.
    dependenciesInfo {
        includeInApk = false
        includeInBundle = false
    }
}

dependencies {
    implementation(project(":core-ui"))
    implementation(project(":core-data"))
    implementation(project(":core-player"))

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.datastore.preferences)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.hilt.android)
    implementation(libs.hilt.lifecycle.viewmodel.compose)
    ksp(libs.hilt.compiler)
    implementation(libs.coil.compose)
    implementation(libs.coil.network.okhttp)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}
