// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.download

import android.content.Context
import android.os.Environment
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.workDataOf
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.File
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withContext
import org.simpleapb.core.data.db.DownloadRow
import org.simpleapb.core.data.db.MirrorDao

private val Context.downloadStore: DataStore<Preferences> by preferencesDataStore(name = "downloads")

/** Where downloads are kept: the phone's own storage or a removable card. */
enum class StorageLocation { INTERNAL, SD_CARD }

data class DownloadSettings(
    val location: StorageLocation = StorageLocation.INTERNAL,
    val wifiOnly: Boolean = true,
    /** 0 = no limit. */
    val capBytes: Long = DEFAULT_CAP,
    /** Download the next book in a series when the current one passes 90 % (decision #4). */
    val autoNext: Boolean = true
) {
    companion object {
        /** Decimal, as Android shows sizes ("10 GB"). */
        const val GB = 1_000_000_000L
        const val DEFAULT_CAP = 10 * GB
        val CAPS = listOf(1 * GB, 2 * GB, 5 * GB, 10 * GB, 20 * GB, 50 * GB, 0L)
    }
}

/** A download's state, as stored. */
object DownloadState {
    const val QUEUED = "queued"
    const val RUNNING = "running"
    const val DONE = "done"
    const val FAILED = "failed"
}

/** Why a download stopped (shown translated). */
object DownloadError {
    const val CAP = "cap"
    const val SPACE = "space"
    const val CHECKSUM = "checksum"
    const val GONE = "gone"
    const val NO_CARD = "no_card"
    const val OTHER = "other"
}

/**
 * Downloads (brief §7.4 screen 6, §7.5): queued in WorkManager, saved as the
 * original files under app-specific storage (internal, or the removable card
 * when chosen — no storage permission needed), checked against the server's
 * SHA-256 before they count.
 */
@Singleton
class Downloads
@Inject
constructor(@ApplicationContext private val context: Context, private val dao: MirrorDao) {
    private val store = context.downloadStore
    private val locationKey = stringPreferencesKey("location")
    private val wifiKey = booleanPreferencesKey("wifi_only")
    private val capKey = longPreferencesKey("cap_bytes")
    private val autoKey = booleanPreferencesKey("auto_next")

    val settings: Flow<DownloadSettings> =
        store.data.map { p ->
            DownloadSettings(
                location = p[locationKey]?.let { runCatching { StorageLocation.valueOf(it) }.getOrNull() } ?: StorageLocation.INTERNAL,
                wifiOnly = p[wifiKey] ?: true,
                capBytes = p[capKey] ?: DownloadSettings.DEFAULT_CAP,
                autoNext = p[autoKey] ?: true
            )
        }

    suspend fun settings(): DownloadSettings = settings.first()

    suspend fun update(change: (DownloadSettings) -> DownloadSettings) {
        store.edit { p ->
            val now =
                DownloadSettings(
                    location = p[locationKey]?.let { runCatching { StorageLocation.valueOf(it) }.getOrNull() } ?: StorageLocation.INTERNAL,
                    wifiOnly = p[wifiKey] ?: true,
                    capBytes = p[capKey] ?: DownloadSettings.DEFAULT_CAP,
                    autoNext = p[autoKey] ?: true
                )
            val next = change(now)
            p[locationKey] = next.location.name
            p[wifiKey] = next.wifiOnly
            p[capKey] = next.capBytes
            p[autoKey] = next.autoNext
        }
    }

    val all: Flow<List<DownloadRow>> = dao.downloads()

    suspend fun get(accountId: String, bookId: String): DownloadRow? = dao.download(accountId, bookId)

    /** The folder of a finished download, or null. */
    suspend fun completeDir(accountId: String, bookId: String): File? =
        dao.download(accountId, bookId)?.takeIf { it.state == DownloadState.DONE }?.let { File(it.dir) }?.takeIf { it.isDirectory }

    /** The removable card's app folder, when one is mounted. */
    fun cardRoot(): File? = context
        .getExternalFilesDirs(null)
        .drop(1)
        .firstOrNull {
            it != null &&
                Environment.getExternalStorageState(it) == Environment.MEDIA_MOUNTED &&
                Environment.isExternalStorageRemovable(it)
        }

    private fun root(location: StorageLocation): File? = when (location) {
        StorageLocation.INTERNAL -> File(context.filesDir, "downloads")
        StorageLocation.SD_CARD -> cardRoot()?.let { File(it, "downloads") }
    }

    /** Bytes used by finished and partial downloads. */
    suspend fun usedBytes(): Long = dao.allDownloads().sumOf { it.doneBytes }

    /** Queues a book (again). A finished download is left alone. */
    suspend fun enqueue(accountId: String, bookId: String, title: String, auto: Boolean = false) {
        val existing = dao.download(accountId, bookId)
        if (existing?.state == DownloadState.DONE) return
        val s = settings()
        val dir =
            existing?.dir ?: root(s.location)?.let { File(File(it, accountId.safe()), bookId.safe()).path }
                ?: root(StorageLocation.INTERNAL)!!.let { File(File(it, accountId.safe()), bookId.safe()).path }
        dao.upsertDownload(
            DownloadRow(
                accountId = accountId,
                bookId = bookId,
                title = title,
                state = DownloadState.QUEUED,
                auto = auto && existing?.auto != false,
                dir = dir,
                totalBytes = existing?.totalBytes ?: 0,
                doneBytes = existing?.doneBytes ?: 0,
                error = null,
                requestedAt = existing?.requestedAt ?: System.currentTimeMillis(),
                finishedAt = null
            )
        )
        schedule(accountId, bookId, s.wifiOnly)
    }

    private fun schedule(accountId: String, bookId: String, wifiOnly: Boolean) {
        val net = Constraints.Builder().setRequiredNetworkType(if (wifiOnly) NetworkType.UNMETERED else NetworkType.CONNECTED).build()
        WorkManager.getInstance(context).enqueueUniqueWork(
            workName(accountId, bookId),
            ExistingWorkPolicy.REPLACE,
            OneTimeWorkRequestBuilder<DownloadWorker>()
                .setConstraints(net)
                .setInputData(workDataOf(DownloadWorker.ACCOUNT to accountId, DownloadWorker.BOOK to bookId))
                .addTag(TAG)
                .build()
        )
    }

    /** The Wi-Fi-only setting changed: waiting downloads follow it. */
    suspend fun reschedule() {
        val wifi = settings().wifiOnly
        dao.allDownloads().filter {
            it.state == DownloadState.QUEUED || it.state == DownloadState.RUNNING
        }.forEach { schedule(it.accountId, it.bookId, wifi) }
    }

    /** Cancels and deletes a download (finished or not). */
    suspend fun remove(accountId: String, bookId: String) {
        WorkManager.getInstance(context).cancelUniqueWork(workName(accountId, bookId))
        val row = dao.download(accountId, bookId) ?: return
        withContext(Dispatchers.IO) { File(row.dir).deleteRecursively() }
        dao.deleteDownload(accountId, bookId)
    }

    suspend fun removeAll(accountId: String) {
        dao.allDownloads().filter { it.accountId == accountId }.forEach { remove(it.accountId, it.bookId) }
    }

    companion object {
        const val TAG = "download"

        fun workName(accountId: String, bookId: String) = "download-$accountId-$bookId"

        /** Ids are UUIDs; anything else is reduced to safe file-name characters. */
        fun String.safe(): String = filter { it.isLetterOrDigit() || it == '-' }.ifEmpty { "x" }
    }
}
