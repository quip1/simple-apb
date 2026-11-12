// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.update

import android.content.Context
import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.edit
import androidx.core.net.toUri
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import java.net.HttpURLConnection
import java.net.URL
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.simpleapb.app.BuildConfig
import org.simpleapb.app.Preset
import org.simpleapb.app.ui.Section
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.data.net.AppScope
import org.simpleapb.core.ui.R as UiR

/** A newer release, with its APK (brief §6.21: sideloaded builds check GitHub releases). */
data class AppRelease(val version: String, val apkUrl: String, val pageUrl: String)

/**
 * The in-app update check of the sideloaded (github) build (ADR-0049). It
 * looks at the newest GitHub release at most twice a day and offers its APK,
 * which installs over this one because both are signed with the release key.
 * The play and fdroid builds have their stores and never check.
 */
@Singleton
class AppUpdates
@Inject
constructor(@ApplicationContext private val context: Context, @AppScope private val scope: CoroutineScope) {
    private val prefs = context.getSharedPreferences("updates", Context.MODE_PRIVATE)
    private val _available = MutableStateFlow<AppRelease?>(null)
    val available: StateFlow<AppRelease?> = _available

    enum class Outcome { AVAILABLE, NONE, FAILED }

    /** Checks if the last check is old enough. */
    fun checkSoon() {
        if (!BuildConfig.UPDATE_CHECK) return
        if (System.currentTimeMillis() - prefs.getLong(LAST_CHECK, 0) < CHECK_EVERY_MS) {
            _available.value = cached()
            return
        }
        scope.launch { check() }
    }

    suspend fun check(): Outcome {
        if (!BuildConfig.UPDATE_CHECK) return Outcome.NONE
        val release = withContext(Dispatchers.IO) { runCatching { fetch() } }.getOrElse { return Outcome.FAILED }
        prefs.edit {
            putLong(LAST_CHECK, System.currentTimeMillis())
            putString(VERSION, release?.version)
            putString(APK, release?.apkUrl)
            putString(PAGE, release?.pageUrl)
        }
        _available.value = cached()
        return if (release != null && newer(release.version, BuildConfig.VERSION_NAME)) Outcome.AVAILABLE else Outcome.NONE
    }

    /** "Not now": this version isn't offered again (a newer one will be). */
    fun skip(version: String) {
        prefs.edit { putString(SKIPPED, version) }
        _available.value = null
    }

    private fun cached(): AppRelease? {
        val v = prefs.getString(VERSION, null) ?: return null
        val apk = prefs.getString(APK, null) ?: return null
        if (!newer(v, BuildConfig.VERSION_NAME) || prefs.getString(SKIPPED, null) == v) return null
        return AppRelease(v, apk, prefs.getString(PAGE, null).orEmpty())
    }

    private fun fetch(): AppRelease? {
        val conn = URL(RELEASES).openConnection() as HttpURLConnection
        conn.connectTimeout = 15_000
        conn.readTimeout = 15_000
        conn.setRequestProperty("Accept", "application/vnd.github+json")
        conn.setRequestProperty("User-Agent", "simple-apb-android")
        try {
            if (conn.responseCode == HttpURLConnection.HTTP_NOT_FOUND) return null
            check(conn.responseCode == HttpURLConnection.HTTP_OK) { "HTTP ${conn.responseCode}" }
            val body = conn.inputStream.bufferedReader().use { it.readText() }
            return parse(Json.parseToJsonElement(body).jsonObject, preset = Preset.server != null)
        } finally {
            conn.disconnect()
        }
    }

    companion object {
        const val RELEASES = "https://api.github.com/repos/quip1/simple-apb/releases/latest"
        private const val CHECK_EVERY_MS = 12 * 60 * 60 * 1000L
        private const val LAST_CHECK = "last_check"
        private const val VERSION = "version"
        private const val APK = "apk"
        private const val PAGE = "page"
        private const val SKIPPED = "skipped"

        /**
         * The release's version and its signed APK: simple-apb-android-X.Y.Z.apk, or
         * for a preset build (ADR-0050) simple-apb-android-X.Y.Z-preset.apk.
         */
        fun parse(o: JsonObject, preset: Boolean = false): AppRelease? {
            val tag = o["tag_name"]?.jsonPrimitive?.contentOrNull?.removePrefix("v") ?: return null
            if (o["draft"]?.jsonPrimitive?.contentOrNull == "true" || o["prerelease"]?.jsonPrimitive?.contentOrNull == "true") return null
            val apk =
                o["assets"]?.jsonArray?.map { it.jsonObject }?.firstOrNull {
                    val name = it["name"]?.jsonPrimitive?.contentOrNull.orEmpty()
                    name.startsWith("simple-apb-android-") && name.endsWith(".apk") && name.endsWith("-preset.apk") == preset
                }?.get("browser_download_url")?.jsonPrimitive?.contentOrNull ?: return null
            if (!apk.startsWith("https://github.com/")) return null
            return AppRelease(tag, apk, o["html_url"]?.jsonPrimitive?.contentOrNull.orEmpty())
        }

        private val SEMVER = Regex("""^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$""")

        /** Whether [candidate] is a later semver than [current] (a release beats its pre-releases). */
        fun newer(candidate: String, current: String): Boolean {
            val a = SEMVER.matchEntire(candidate)?.groupValues ?: return false
            val b = SEMVER.matchEntire(current)?.groupValues ?: return false
            for (i in 1..3) {
                val x = a[i].toInt()
                val y = b[i].toInt()
                if (x != y) return x > y
            }
            return a[4].isEmpty() && b[4].isNotEmpty()
        }
    }
}

@HiltViewModel
class UpdatesViewModel
@Inject
constructor(private val updates: AppUpdates) : ViewModel() {
    val available: StateFlow<AppRelease?> = updates.available

    init {
        updates.checkSoon()
    }

    suspend fun check() = updates.check()

    fun skip(version: String) = updates.skip(version)
}

private fun openDownload(context: Context, url: String) {
    context.startActivity(Intent(Intent.ACTION_VIEW, url.toUri()).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

/** Home: a newer version is out. Nothing at all in the play and fdroid builds. */
@Composable
fun UpdateBanner() {
    if (!BuildConfig.UPDATE_CHECK) return
    val vm: UpdatesViewModel = hiltViewModel()
    val release by vm.available.collectAsStateWithLifecycle()
    val r = release ?: return
    val context = LocalContext.current
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(stringResource(UiR.string.app_update_available, r.version), style = MaterialTheme.typography.titleSmall)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { openDownload(context, r.apkUrl) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_update_download))
            }
            TextButton(onClick = { vm.skip(r.version) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_update_later))
            }
        }
    }
}

/** Settings: the version, and checking by hand (github build only). */
@Composable
fun UpdateSection() {
    if (!BuildConfig.UPDATE_CHECK) return
    val vm: UpdatesViewModel = hiltViewModel()
    val release by vm.available.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var outcome by remember { mutableStateOf<AppUpdates.Outcome?>(null) }
    var busy by remember { mutableStateOf(false) }
    Section(stringResource(UiR.string.app_update_title)) {
        Text(stringResource(UiR.string.app_update_current, BuildConfig.VERSION_NAME))
        release?.let { r ->
            Text(stringResource(UiR.string.app_update_available, r.version))
            Button(onClick = { openDownload(context, r.apkUrl) }, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_update_download))
            }
        }
        when (outcome) {
            AppUpdates.Outcome.NONE -> Text(stringResource(UiR.string.app_update_none))
            AppUpdates.Outcome.FAILED -> Text(stringResource(UiR.string.app_update_failed))
            else -> {}
        }
        OutlinedButton(
            onClick = {
                busy = true
                scope.launch {
                    outcome = vm.check()
                    busy = false
                }
            },
            enabled = !busy,
            modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)
        ) { Text(stringResource(UiR.string.app_update_check)) }
    }
}
