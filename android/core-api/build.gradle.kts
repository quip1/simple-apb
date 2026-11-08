// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Kotlin API client generated from server/openapi.yaml at build time.
// Pure JVM (no Android SDK needed) so it compiles anywhere, and core-data
// depends on it. Generated sources are not committed.

import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.openapitools.generator.gradle.plugin.tasks.GenerateTask

plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.openapi.generator)
}

// Emit Java 17 bytecode (what Android modules consume) using whichever JDK
// runs Gradle, instead of requiring a separate JDK 17 toolchain to be installed.
java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
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
    // Free-form JSON (e.g. Error.details) would become Map<String, Any>, which
    // kotlinx.serialization cannot handle. Use JsonElement instead.
    typeMappings.set(mapOf("AnyType" to "JsonElement"))
    importMappings.set(mapOf("JsonElement" to "kotlinx.serialization.json.JsonElement"))
    generateApiTests.set(false)
    generateModelTests.set(false)
    generateApiDocumentation.set(false)
    generateModelDocumentation.set(false)
    inputs.file(specFile)

    // Generator bug: two apiKey security schemes (cookieAuth + streamSignature)
    // make ApiClient.kt import ApiKeyAuth twice, which Kotlin rejects.
    // Drop exact duplicate import lines from every generated file.
    doLast {
        generatedDir.get().asFile.walkTopDown()
            .filter { it.isFile && it.extension == "kt" }
            .forEach { file ->
                val seen = HashSet<String>()
                val text = file.readText()
                val fixed = text.lines()
                    .filter { line -> !line.startsWith("import ") || seen.add(line.trim()) }
                    .joinToString("\n")
                if (fixed != text) file.writeText(fixed)
            }
    }
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
