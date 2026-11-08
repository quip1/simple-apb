// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Kotlin API client generated from server/openapi.yaml at build time.
// Pure JVM (no Android SDK needed) so it compiles anywhere, and core-data
// depends on it. Generated sources are not committed.

import org.openapitools.generator.gradle.plugin.tasks.GenerateTask

plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.openapi.generator)
}

kotlin {
    jvmToolchain(17)
}

val specFile = rootProject.layout.projectDirectory.file("../server/openapi.yaml")
val generatedDir = layout.buildDirectory.dir("generated/openapi")

val generateApi by tasks.registering(GenerateTask::class) {
    generatorName.set("kotlin")
    inputSpec.set(specFile.asFile.absolutePath)
    outputDir.set(generatedDir.get().asFile.absolutePath)
    packageName.set("org.simpleapb.api")
    apiPackage.set("org.simpleapb.api.endpoints")
    modelPackage.set("org.simpleapb.api.model")
    library.set("jvm-retrofit2")
    configOptions.set(
        mapOf(
            "serializationLibrary" to "kotlinx_serialization",
            "useCoroutines" to "true",
            "dateLibrary" to "string",
            "enumPropertyNaming" to "UPPERCASE",
            "omitGradleWrapper" to "true",
            "sourceFolder" to "src/main/kotlin",
        ),
    )
    generateApiTests.set(false)
    generateModelTests.set(false)
    generateApiDocumentation.set(false)
    generateModelDocumentation.set(false)
    inputs.file(specFile)
}

sourceSets {
    main {
        kotlin.srcDir(generatedDir.map { it.dir("src/main/kotlin") })
    }
}

tasks.named("compileKotlin") { dependsOn(generateApi) }

dependencies {
    api(libs.retrofit)
    api(libs.okhttp)
    implementation(libs.retrofit.scalars)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.okhttp.logging)
    implementation(libs.kotlinx.serialization.json)
    testImplementation(libs.junit)
}
