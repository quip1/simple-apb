// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.download

import android.content.Context
import android.os.StatFs
import android.os.SystemClock
import androidx.hilt.work.HiltWorker
import androidx.work.CoroutineWorker
import androidx.work.ForegroundInfo
import androidx.work.WorkerParameters
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.security.MessageDigest
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.simpleapb.api.model.BookDetail
import org.simpleapb.api.model.BookFile
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.db.MirrorDao
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.data.sync.LocalLibrary

/** The app draws the download notification (its strings live in the shared catalog). */
interface DownloadNotifications {
    fun foreground(title: String, percent: Int?): ForegroundInfo
}

/**
 * Downloads one book: each original file with HTTP range requests (resuming
 * a partial file), checked against the server's SHA-256, then moved into
 * place. A book counts as downloaded only when every file has passed.
 */
@HiltWorker
class DownloadWorker
@AssistedInject
constructor(
    @Assisted context: Context,
    @Assisted params: WorkerParameters,
    private val accounts: AccountStore,
    private val apis: ApiFactory,
    private val local: LocalLibrary,
    private val dao: MirrorDao,
    private val downloads: Downloads,
    private val notifications: DownloadNotifications
) : CoroutineWorker(context, params) {
    private val accountId = params.inputData.getString(ACCOUNT).orEmpty()
    private val bookId = params.inputData.getString(BOOK).orEmpty()

    override suspend fun doWork(): Result {
        val account = accounts.current().accounts.firstOrNull { it.id == accountId } ?: return Result.success()
        val row = dao.download(accountId, bookId) ?: return Result.success() // removed meanwhile
        if (row.state == DownloadState.DONE) return Result.success()
        runCatching { setForeground(notifications.foreground(row.title, null)) }

        val detail =
            try {
                val res = apis.forAccount(account).library.getBook(UUID.fromString(bookId))
                when {
                    res.isSuccessful -> res.body()!!
                    res.code() == 404 || res.code() == 403 -> return fail(DownloadError.GONE)
                    else -> return Result.retry()
                }
            } catch (_: IOException) {
                return waiting()
            }
        local.saveDetail(accountId, detail)

        val total = detail.files.sumOf { it.sizeBytes }
        val s = downloads.settings()
        val others = dao.allDownloads().filter { !(it.accountId == accountId && it.bookId == bookId) }.sumOf { it.doneBytes }
        if (s.capBytes > 0 && others + total > s.capBytes) return fail(DownloadError.CAP, total)
        val dir = File(row.dir)
        if (!withContext(Dispatchers.IO) { dir.mkdirs() || dir.isDirectory }) return fail(DownloadError.NO_CARD, total)
        val have = detail.files.sumOf { existingBytes(dir, it) }
        if (StatFs(dir.path).availableBytes < total - have + SPACE_MARGIN) return fail(DownloadError.SPACE, total)

        dao.updateDownload(accountId, bookId, DownloadState.RUNNING, have, total, null, null)
        val http = apis.http(account)
        var done = have
        for (f in detail.files) {
            val target = File(dir, fileName(f))
            if (target.length() == f.sizeBytes && target.isFile) continue
            val r =
                try {
                    fetch(http, account, detail, f, dir) { n ->
                        done += n
                        progress(row.title, done, total)
                    }
                } catch (_: IOException) {
                    return waiting(done, total)
                }
            when (r) {
                Fetch.OK -> Unit
                Fetch.STOPPED -> return Result.retry()
                Fetch.GONE -> return fail(DownloadError.GONE, total)
                Fetch.SERVER -> return Result.retry()
                Fetch.CHECKSUM -> return if (runAttemptCount < 2) Result.retry() else fail(DownloadError.CHECKSUM, total)
            }
        }
        cover(http, account, detail, dir)
        if (dao.download(accountId, bookId) == null) return Result.success() // removed while finishing
        dao.updateDownload(accountId, bookId, DownloadState.DONE, total, total, null, System.currentTimeMillis())
        return Result.success()
    }

    private enum class Fetch { OK, STOPPED, GONE, SERVER, CHECKSUM }

    private var lastUpdate = 0L

    private suspend fun progress(title: String, done: Long, total: Long) {
        val now = SystemClock.elapsedRealtime()
        if (now - lastUpdate < PROGRESS_MS) return
        lastUpdate = now
        dao.updateDownload(accountId, bookId, DownloadState.RUNNING, done, total, null, null)
        val pct = if (total > 0) ((done * 100) / total).toInt() else null
        runCatching { setForeground(notifications.foreground(title, pct)) }
    }

    private suspend fun fetch(http: OkHttpClient, account: Account, d: BookDetail, f: BookFile, dir: File, onBytes: suspend (Long) -> Unit): Fetch =
        withContext(Dispatchers.IO) {
            val part = File(dir, "${f.id}.part")
            if (part.length() > f.sizeBytes) part.delete()
            var start = part.length()
            val url = ServerUrl.api(account.serverUrl) + "books/${d.id}/files/${f.id}?download=true"
            val req = Request.Builder().url(url).apply { if (start > 0) header("Range", "bytes=$start-") }.build()
            http.newCall(req).execute().use { res ->
                when {
                    res.code == 416 && start == f.sizeBytes -> Unit // already complete
                    res.code == 404 || res.code == 403 || res.code == 451 -> return@withContext Fetch.GONE
                    !res.isSuccessful -> return@withContext Fetch.SERVER
                    else -> {
                        // 206 continues the partial file; a plain 200 starts it over.
                        val append = res.code == 206 && start > 0
                        if (!append && start > 0) {
                            onBytes(-start) // the partial bytes counted so far are thrown away
                            start = 0
                        }
                        FileOutputStream(part, append).use { out ->
                            val input = res.body.byteStream()
                            val buf = ByteArray(BUFFER)
                            var pending = 0L
                            while (true) {
                                if (isStopped) {
                                    out.fd.sync()
                                    return@withContext Fetch.STOPPED
                                }
                                val n = input.read(buf)
                                if (n < 0) break
                                out.write(buf, 0, n)
                                pending += n
                                if (pending >= REPORT_BYTES) {
                                    onBytes(pending)
                                    pending = 0
                                }
                            }
                            out.flush()
                            out.fd.sync()
                            onBytes(pending)
                        }
                    }
                }
            }
            if (!sha256(part).equals(f.sha256, ignoreCase = true)) {
                part.delete()
                return@withContext Fetch.CHECKSUM
            }
            val target = File(dir, fileName(f))
            if (!part.renameTo(target)) throw IOException("rename failed")
            Fetch.OK
        }

    /** The cover, for the downloads list and the lock screen while offline (best effort). */
    private suspend fun cover(http: OkHttpClient, account: Account, d: BookDetail, dir: File) = withContext(Dispatchers.IO) {
        if (!d.hasCover) return@withContext
        val url = ServerUrl.resolve(account.serverUrl, d.coverUrl).let { it + (if ('?' in it) "&" else "?") + "size=512" }
        runCatching {
            http.newCall(Request.Builder().url(url).build()).execute().use { res ->
                if (!res.isSuccessful) return@use
                val tmp = File(dir, "cover.tmp")
                FileOutputStream(tmp).use { out ->
                    res.body.byteStream().copyTo(out)
                    out.fd.sync()
                }
                tmp.renameTo(File(dir, COVER))
            }
        }
    }

    private suspend fun waiting(done: Long? = null, total: Long? = null): Result {
        val row = dao.download(accountId, bookId) ?: return Result.success()
        dao.updateDownload(accountId, bookId, DownloadState.QUEUED, done ?: row.doneBytes, total ?: row.totalBytes, null, null)
        return Result.retry()
    }

    private suspend fun fail(error: String, total: Long? = null): Result {
        val row = dao.download(accountId, bookId) ?: return Result.success()
        dao.updateDownload(accountId, bookId, DownloadState.FAILED, row.doneBytes, total ?: row.totalBytes, error, null)
        return Result.failure()
    }

    companion object {
        const val ACCOUNT = "account"
        const val BOOK = "book"
        const val COVER = "cover.jpg"
        private const val BUFFER = 64 * 1024
        private const val REPORT_BYTES = 256 * 1024L
        private const val PROGRESS_MS = 750L
        private const val SPACE_MARGIN = 50L * 1024 * 1024

        fun fileName(f: BookFile): String = "${f.id}.${extension(f.mimeType)}"

        fun extension(mime: String): String = when (mime.substringBefore(';').trim().lowercase()) {
            "audio/mpeg", "audio/mp3" -> "mp3"
            "audio/mp4", "audio/x-m4b", "audio/m4b", "audio/x-m4a", "audio/aac" -> "m4b"
            "audio/ogg", "audio/opus" -> "ogg"
            "audio/flac", "audio/x-flac" -> "flac"
            else -> "bin"
        }

        private fun existingBytes(dir: File, f: BookFile): Long {
            val done = File(dir, fileName(f))
            if (done.isFile && done.length() == f.sizeBytes) return f.sizeBytes
            return File(dir, "${f.id}.part").length().coerceAtMost(f.sizeBytes)
        }

        fun sha256(file: File): String {
            val md = MessageDigest.getInstance("SHA-256")
            file.inputStream().use { input ->
                val buf = ByteArray(BUFFER)
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    md.update(buf, 0, n)
                }
            }
            return md.digest().joinToString("") { "%02x".format(it) }
        }
    }
}
