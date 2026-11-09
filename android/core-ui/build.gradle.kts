// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Design system shared by the phone app, widget and (later) Wear: the Fern
// dark theme and the e-ink theme (brief §11), plus common components.
//
// It also owns the app's strings: they are generated from the shared
// catalogs in /i18n (the same ones the web app and server use), so there is
// one translation source. See GenerateAndroidStrings below.

import groovy.json.JsonSlurper

plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "org.simpleapb.core.ui"
    compileSdk = 37
    defaultConfig {
        minSdk = 26
    }
    buildFeatures {
        compose = true
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

/**
 * Turns /i18n/<locale>.json (ICU messages) into Android string resources.
 *
 * - Keys become resource names: "auth.sign_in" → R.string.auth_sign_in.
 * - {name} placeholders become positional %1$s, %2$s… numbered in the order
 *   they first appear in the English message, for every locale.
 * - A message with one {x, plural, …} becomes <plurals>; "#" is the count.
 *   Exact-value branches (=0) have no Android equivalent and are dropped.
 * - {x, number} becomes a plain argument: the app formats numbers itself.
 * - Messages Android can't express (two plurals, select) are skipped.
 */
abstract class GenerateAndroidStrings : DefaultTask() {
    @get:InputDirectory
    @get:PathSensitive(PathSensitivity.RELATIVE)
    abstract val catalogs: DirectoryProperty

    @get:OutputDirectory
    abstract val outputDir: DirectoryProperty

    @TaskAction
    fun generate() {
        val out = outputDir.get().asFile
        out.deleteRecursively()
        val files =
            catalogs
                .get()
                .asFile
                .listFiles { f -> f.extension == "json" }
                .orEmpty()
                .sortedBy { it.name }
        val base = files.firstOrNull { it.nameWithoutExtension == "en" } ?: error("i18n/en.json is missing")

        @Suppress("UNCHECKED_CAST")
        val english = (JsonSlurper().parse(base) as Map<String, String>).mapValues { simplify(it.value) }
        // Argument order per key, fixed by the English message.
        val order = english.mapValues { (_, msg) -> argNames(msg) }
        for (file in files) {
            @Suppress("UNCHECKED_CAST")
            val messages = (JsonSlurper().parse(file) as Map<String, String>).mapValues { simplify(it.value) }
            val locale = file.nameWithoutExtension
            val dir = if (locale == "en") "values" else "values-" + locale.replace("-", "-r")
            val xml =
                StringBuilder(
                    "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<!-- Generated from i18n/$locale.json. Do not edit. -->\n<resources>\n"
                )
            for ((key, msg) in messages.toSortedMap()) {
                val args = order[key] ?: continue // only keys the English catalog has
                val name = key.replace(Regex("[^A-Za-z0-9_]"), "_")
                convert(name, msg, args)?.let { xml.append(it) }
            }
            xml.append("</resources>\n")
            File(out, dir).apply { mkdirs() }.resolve("strings.xml").writeText(xml.toString())
        }
    }

    private val numberRe = Regex("""\{(\w+),\s*number(?:,[^{}]*)?\}""")

    private fun simplify(msg: String) = numberRe.replace(msg) { "{" + it.groupValues[1] + "}" }

    private val pluralRe = Regex("""\{(\w+),\s*plural,\s*((?:[^{}]*\{[^{}]*\})+)\s*\}""")
    private val branchRe = Regex("""(=?\w+)\s*\{([^{}]*)\}""")
    private val argRe = Regex("""\{(\w+)\}""")

    private fun argNames(msg: String): List<String> {
        val names = mutableListOf<String>()
        val plural = pluralRe.find(msg)
        if (plural != null) names += plural.groupValues[1]
        argRe.findAll(msg.replace(pluralRe, "")).forEach { if (it.groupValues[1] !in names) names += it.groupValues[1] }
        pluralRe.find(msg)?.groupValues?.get(2)?.let { body ->
            argRe.findAll(body).forEach { if (it.groupValues[1] !in names) names += it.groupValues[1] }
        }
        return names
    }

    private fun convert(name: String, msg: String, args: List<String>): String? {
        if (msg.contains(", select,")) return null
        val plurals = pluralRe.findAll(msg).toList()
        if (plurals.size > 1) return null
        if (plurals.isEmpty()) {
            val text = format(msg, args, null)
            val formatted = if (args.isEmpty() && msg.contains('%')) " formatted=\"false\"" else ""
            return "    <string name=\"$name\"$formatted>${escape(text)}</string>\n"
        }
        val p = plurals[0]
        val prefix = msg.substring(0, p.range.first)
        val suffix = msg.substring(p.range.last + 1)
        if (pluralRe.containsMatchIn(prefix + suffix)) return null
        val countVar = p.groupValues[1]
        val items =
            branchRe
                .findAll(p.groupValues[2])
                .filter { !it.groupValues[1].startsWith("=") }
                .joinToString("") { b ->
                    val text = format(prefix + b.groupValues[2] + suffix, args, countVar)
                    "        <item quantity=\"${b.groupValues[1]}\">${escape(text)}</item>\n"
                }
        return "    <plurals name=\"$name\">\n$items    </plurals>\n"
    }

    /**
     * ICU → Android format string. Percent signs are escaped only when the
     * text is formatted; a string without arguments is `formatted="false"`
     * and shown as written.
     */
    private fun format(msg: String, args: List<String>, countVar: String?): String {
        var s = if (args.isEmpty() && countVar == null) msg else msg.replace("%", "%%")
        if (countVar != null) s = s.replace("#", "%" + (args.indexOf(countVar) + 1) + "\$d")
        return argRe.replace(s) { m ->
            val i = args.indexOf(m.groupValues[1])
            if (i < 0) m.value else "%" + (i + 1) + "\$s"
        }
    }

    private fun escape(s: String): String {
        var t =
            s
                .replace("&", "&amp;")
                .replace("<", "&lt;")
                .replace(">", "&gt;")
                .replace("\\", "\\\\")
                .replace("'", "\\'")
                .replace("\"", "\\\"")
                .replace("\n", "\\n")
        if (t.startsWith("@") || t.startsWith("?")) t = "\\" + t
        return t
    }
}

val generateStrings =
    tasks.register<GenerateAndroidStrings>("generateAndroidStrings") {
        catalogs.set(rootProject.layout.projectDirectory.dir("../i18n"))
        outputDir.set(layout.buildDirectory.dir("generated/i18n/res"))
    }

androidComponents {
    onVariants { variant ->
        variant.sources.res?.addGeneratedSourceDirectory(generateStrings, GenerateAndroidStrings::outputDir)
    }
}

dependencies {
    api(platform(libs.compose.bom))
    api(libs.compose.ui)
    api(libs.compose.material3)
    api(libs.compose.material.icons)
    api(libs.compose.ui.tooling.preview)
    debugImplementation(libs.compose.ui.tooling)
}
