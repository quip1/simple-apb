// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.update

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.os.Build
import android.provider.Settings
import android.util.Log
import androidx.core.content.IntentCompat
import androidx.core.content.pm.PackageInfoCompat
import androidx.core.net.toUri
import dagger.hilt.android.AndroidEntryPoint
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.File
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.simpleapb.core.data.net.AppScope

/**
 * Installs a release's APK from inside the app (ADR-0052): download it, check
 * it is this app, newer, and signed with the same key as the installed copy,
 * then hand it to Android's package installer in a session. The old version
 * stays until Android swaps in the new one, keeping all data; nothing is
 * uninstalled. Android asks once for "install unknown apps"; after the first
 * update through here, Android 12+ installs later ones without asking.
 */
@Singleton
class ApkInstaller
@Inject
constructor(@ApplicationContext private val context: Context, @AppScope private val scope: CoroutineScope) {
    sealed interface State {
        data object Idle : State

        data class Downloading(val percent: Int) : State

        /** The user has to allow this app to install apps (Settings), then come back. */
        data object NeedsPermission : State

        data object Installing : State

        /** Android wants a tap on its confirm dialog; [intent] shows it again. */
        data class Confirm(val intent: Intent) : State

        data class Failed(val reason: Reason) : State
    }

    enum class Reason { DOWNLOAD, SIGNATURE, INSTALL }

    private val _state = MutableStateFlow<State>(State.Idle)
    val state: StateFlow<State> = _state

    private var job: Job? = null
    private var pending: AppRelease? = null

    fun canInstall(): Boolean = context.packageManager.canRequestPackageInstalls()

    /** The Settings screen where the user allows this app to install apps. */
    fun permissionIntent(): Intent = Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, "package:${context.packageName}".toUri())

    fun install(release: AppRelease) {
        if (job?.isActive == true) return
        pending = release
        if (!canInstall()) {
            _state.value = State.NeedsPermission
            return
        }
        job =
            scope.launch {
                val apk =
                    try {
                        withContext(Dispatchers.IO) { download(release) }
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        Log.w(TAG, "update download failed: ${e.javaClass.simpleName}")
                        _state.value = State.Failed(Reason.DOWNLOAD)
                        return@launch
                    }
                if (!withContext(Dispatchers.IO) { verify(apk) }) {
                    apk.delete()
                    _state.value = State.Failed(Reason.SIGNATURE)
                    return@launch
                }
                _state.value = State.Installing
                try {
                    withContext(Dispatchers.IO) { commit(apk) }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    Log.w(TAG, "update install failed: ${e.javaClass.simpleName}")
                    _state.value = State.Failed(Reason.INSTALL)
                }
            }
    }

    /** Back from Settings: carry on if the permission was given. */
    fun resume() {
        if (_state.value == State.NeedsPermission && canInstall()) pending?.let(::install)
    }

    private fun dir() = File(context.cacheDir, "updates").apply { mkdirs() }

    /** Downloads to a .part file, fsyncs and renames it; older downloads are removed. */
    private fun download(release: AppRelease): File {
        val dest = File(dir(), "simple-apb-${release.version}.apk")
        dir().listFiles()?.filter { it != dest }?.forEach { it.delete() }
        if (dest.exists() && verify(dest)) return dest
        val part = File(dir(), dest.name + ".part")
        val conn = URL(release.apkUrl).openConnection() as HttpURLConnection
        conn.connectTimeout = 15_000
        conn.readTimeout = 30_000
        conn.setRequestProperty("User-Agent", "simple-apb-android")
        try {
            check(conn.responseCode == HttpURLConnection.HTTP_OK) { "HTTP ${conn.responseCode}" }
            val total = conn.contentLengthLong
            var done = 0L
            _state.value = State.Downloading(0)
            conn.inputStream.use { input ->
                FileOutputStream(part).use { out ->
                    val buf = ByteArray(64 * 1024)
                    while (true) {
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        done += n
                        if (total > 0) _state.value = State.Downloading((done * 100 / total).toInt())
                    }
                    out.fd.sync()
                }
            }
            check(total <= 0 || done == total) { "short download" }
        } finally {
            conn.disconnect()
        }
        check(part.renameTo(dest)) { "rename failed" }
        return dest
    }

    /** This app, a later version, and signed by the same certificates as the installed copy. */
    private fun verify(apk: File): Boolean {
        val pm = context.packageManager
        val archive = archiveInfo(pm, apk) ?: return false
        if (archive.packageName != context.packageName) return false
        val installed = installedInfo(pm)
        if (PackageInfoCompat.getLongVersionCode(archive) <= PackageInfoCompat.getLongVersionCode(installed)) return false
        val theirs = certs(archive)
        return theirs.isNotEmpty() && theirs == certs(installed)
    }

    private fun commit(apk: File) {
        val installer = context.packageManager.packageInstaller
        val params =
            PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
                setAppPackageName(context.packageName)
                setSize(apk.length())
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                    setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
                }
            }
        val id = installer.createSession(params)
        try {
            installer.openSession(id).use { session ->
                session.openWrite("base.apk", 0, apk.length()).use { out ->
                    apk.inputStream().use { it.copyTo(out) }
                    session.fsync(out)
                }
                val flags = PendingIntent.FLAG_UPDATE_CURRENT or
                    (if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) PendingIntent.FLAG_MUTABLE else 0)
                val result = Intent(context, InstallResultReceiver::class.java).setPackage(context.packageName)
                session.commit(PendingIntent.getBroadcast(context, id, result, flags).intentSender)
            }
        } catch (e: Exception) {
            runCatching { installer.abandonSession(id) }
            throw e
        }
    }

    /** What the package installer said about the session. */
    internal fun onStatus(intent: Intent) {
        when (val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                val confirm = IntentCompat.getParcelableExtra(intent, Intent.EXTRA_INTENT, Intent::class.java) ?: return
                confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                _state.value = State.Confirm(confirm)
                // Fine while the app is in front; otherwise the Confirm state offers it again.
                runCatching { context.startActivity(confirm) }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                _state.value = State.Idle
                dir().listFiles()?.forEach { it.delete() }
            }
            PackageInstaller.STATUS_FAILURE_ABORTED -> _state.value = State.Idle
            else -> {
                Log.w(TAG, "update install failed with status $status")
                _state.value = State.Failed(Reason.INSTALL)
            }
        }
    }

    companion object {
        private const val TAG = "ApkInstaller"

        @Suppress("DEPRECATION")
        private fun archiveInfo(pm: PackageManager, apk: File): PackageInfo? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            pm.getPackageArchiveInfo(apk.path, PackageManager.GET_SIGNING_CERTIFICATES)
                ?.takeIf { it.signingInfo != null }
                ?: pm.getPackageArchiveInfo(apk.path, PackageManager.GET_SIGNATURES)
        } else {
            pm.getPackageArchiveInfo(apk.path, PackageManager.GET_SIGNATURES)
        }

        @Suppress("DEPRECATION")
        private fun Context.installedInfo(pm: PackageManager): PackageInfo = pm.getPackageInfo(
            packageName,
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
        )

        /** SHA-256 of each signing certificate. */
        @Suppress("DEPRECATION")
        private fun certs(info: PackageInfo): Set<String> {
            val signing = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) info.signingInfo else null
            val sigs =
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P && signing != null) {
                    if (signing.hasMultipleSigners()) {
                        signing.apkContentsSigners
                    } else {
                        signing.signingCertificateHistory?.takeLast(1)?.toTypedArray()
                    }
                } else {
                    info.signatures
                } ?: return emptySet()
            val sha = MessageDigest.getInstance("SHA-256")
            return sigs.map { s -> sha.digest(s.toByteArray()).joinToString("") { "%02x".format(it) } }.toSet()
        }
    }

    private fun installedInfo(pm: PackageManager) = context.installedInfo(pm)
}

/** Receives the package installer's answers (only through the session's own PendingIntent). */
@AndroidEntryPoint
class InstallResultReceiver : BroadcastReceiver() {
    @Inject lateinit var installer: ApkInstaller

    override fun onReceive(context: Context, intent: Intent) {
        installer.onStatus(intent)
    }
}
