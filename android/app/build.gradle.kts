// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The phone app (brief §7). Flavors (§7.1):
//   play   — for Google Play: no in-app update check
//   github — sideloaded APKs: the in-app update check
//   fdroid — no Google libraries at all, no update check
// (Casting, which would have put the Google Cast SDK in play and github, was dropped: ADR-0046.)
//
// Release signing (ADR-0045): the keys come from environment variables or
// ~/.gradle/gradle.properties, never from this repository. Without them,
// release builds are unsigned.
//   SIMPLEAPB_KEYSTORE, _KEYSTORE_PASSWORD, _KEY_ALIAS, _KEY_PASSWORD
//       the release key: signs the github APK (and the play bundle if no upload key is set)
//   SIMPLEAPB_PLAY_KEYSTORE, _KEYSTORE_PASSWORD, _KEY_ALIAS, _KEY_PASSWORD
//       the Google Play upload key (Play App Signing re-signs for users)
// The fdroid build is never signed here: F-Droid builds it from source and signs it.
//
// Preset build (ADR-0050): SIMPLEAPB_PRESET_SERVER=https://books.example.com bakes one
// server into the app, which then opens straight to its sign-in and offers no other
// server. Set only when building that APK (the release workflow reads a secret); the
// repository never names a server, and without it the app asks as usual.

import java.util.zip.ZipFile
import org.gradle.api.artifacts.component.ModuleComponentIdentifier
import org.gradle.api.artifacts.result.ResolvedComponentResult
import org.gradle.api.artifacts.result.ResolvedDependencyResult

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
        versionCode = 502
        versionName = "0.5.2"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        buildConfigField("String", "PRESET_SERVER", "\"${presetServer()}\"")
    }

    signingConfigs {
        for ((name, prefix) in listOf("release" to "SIMPLEAPB_", "upload" to "SIMPLEAPB_PLAY_")) {
            val store = secret("${prefix}KEYSTORE") ?: continue
            create(name) {
                storeFile = file(store)
                storePassword = secret("${prefix}KEYSTORE_PASSWORD")
                keyAlias = secret("${prefix}KEY_ALIAS")
                keyPassword = secret("${prefix}KEY_PASSWORD") ?: storePassword
            }
        }
    }

    flavorDimensions += "distribution"
    productFlavors {
        create("play") {
            dimension = "distribution"
            (signingConfigs.findByName("upload") ?: signingConfigs.findByName("release"))?.let { signingConfig = it }
            buildConfigField("boolean", "UPDATE_CHECK", "false")
        }
        create("github") {
            dimension = "distribution"
            signingConfigs.findByName("release")?.let { signingConfig = it }
            buildConfigField("boolean", "UPDATE_CHECK", "true")
        }
        create("fdroid") {
            dimension = "distribution"
            buildConfigField("boolean", "UPDATE_CHECK", "false")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            // Reproducible builds: no git commit baked into the APK, so a build from
            // a source tarball matches one from a checkout.
            vcsInfo.include = false
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
    implementation(libs.androidx.hilt.work)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.glance.appwidget)
    implementation(libs.unifiedpush.connector)
    implementation(libs.camerax.camera2)
    implementation(libs.camerax.lifecycle)
    implementation(libs.camerax.view)
    implementation(libs.zxing.core)
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

/** The preset server, checked: an http(s) address with nothing that could break out of a Java string. */
fun presetServer(): String {
    val v = secret("SIMPLEAPB_PRESET_SERVER")?.trim()?.trimEnd('/') ?: return ""
    require(Regex("^https?://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~/-]*)?$").matches(v)) {
        "SIMPLEAPB_PRESET_SERVER must be a plain http(s) address, e.g. https://books.example.com"
    }
    return v
}

fun secret(name: String): String? = (providers.environmentVariable(name).orNull ?: providers.gradleProperty(name).orNull)?.takeIf { it.isNotBlank() }

// F-Droid checks (brief §7.1, §14): the fdroid flavor carries no proprietary
// Google library, neither as a dependency nor as classes in the APK.
val notForFdroid =
    listOf(
        "com.google.android.gms",
        "com.google.firebase",
        "com.google.android.play",
        "com.google.android.datatransport",
        "com.google.mlkit",
        "com.google.android.ads",
        "com.crashlytics"
    )

fun modulesOf(root: ResolvedComponentResult): List<String> {
    val seen = mutableSetOf<String>()
    val todo = ArrayDeque(listOf(root))
    while (todo.isNotEmpty()) {
        for (d in todo.removeFirst().dependencies) {
            val to = (d as? ResolvedDependencyResult)?.selected ?: continue
            val id = to.id
            val name = if (id is ModuleComponentIdentifier) "${id.group}:${id.module}" else id.displayName
            if (seen.add(name)) todo.add(to)
        }
    }
    return seen.sorted()
}

val checkFdroidDependencies by tasks.registering {
    group = "verification"
    description = "Fails if the fdroid release build depends on a proprietary Google library."
    val modules = configurations.named("fdroidReleaseRuntimeClasspath").flatMap { it.incoming.resolutionResult.rootComponent.map(::modulesOf) }
    inputs.property("modules", modules)
    doLast {
        val bad = modules.get().filter { m -> notForFdroid.any { m.startsWith("$it:") || m.startsWith("$it.") } }
        if (bad.isNotEmpty()) throw GradleException("The fdroid build pulls in proprietary libraries: ${bad.joinToString()}")
        logger.lifecycle("fdroid: ${modules.get().size} dependencies, none proprietary")
    }
}

val checkFdroidApk by tasks.registering {
    group = "verification"
    description = "Fails if the fdroid release APK contains classes of a proprietary Google library."
    dependsOn("assembleFdroidRelease")
    val apks = layout.buildDirectory.dir("outputs/apk/fdroid/release")
    val needles = notForFdroid.map { "L" + it.replace('.', '/') + "/" }
    doLast {
        val apk = apks.get().asFile.listFiles { f -> f.extension == "apk" }?.singleOrNull() ?: throw GradleException("no fdroid release APK")
        val found = mutableSetOf<String>()
        ZipFile(apk).use { zip ->
            for (e in zip.entries().asSequence().filter { it.name.endsWith(".dex") }) {
                val dex = String(zip.getInputStream(e).readBytes(), Charsets.ISO_8859_1)
                needles.filterTo(found) { dex.contains(it) }
            }
        }
        if (found.isNotEmpty()) throw GradleException("${apk.name} contains ${found.joinToString()}")
        logger.lifecycle("fdroid: ${apk.name} has no proprietary classes")
    }
}

tasks.register("checkFdroid") {
    group = "verification"
    description = "F-Droid inclusion checks for the fdroid flavor."
    dependsOn(checkFdroidDependencies, checkFdroidApk)
}
