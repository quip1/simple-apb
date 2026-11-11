// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.local

import android.content.Context
import android.util.Base64
import androidx.core.net.toUri
import androidx.hilt.work.HiltWorker
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.io.InputStream
import java.security.MessageDigest
import java.util.zip.CRC32
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.simpleapb.api.model.UploadItem
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.db.MirrorDao
import org.simpleapb.core.data.download.DownloadNotifications
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ServerUrl

/** Where an upload of a book on this device stands. */
object UploadState {
    const val QUEUED = "queued"
    const val SENDING = "sending"
    const val PROCESSING = "processing"
    const val DONE = "done"
    const val FAILED = "failed"
}

/** Why an upload stopped (shown translated). */
object UploadError {
    const val FORBIDDEN = "forbidden"
    const val QUOTA = "quota"
    const val UNSUPPORTED = "unsupported"
    const val REJECTED = "rejected"
    const val OTHER = "other"
}

/**
 * Uploads a book from this device to a server (brief §7.4 screen 9) with
 * the server's resumable tus endpoint (§6.11): 8 MB chunks, resumed from
 * what the server says it has after a dropped connection, and the file's
 * SHA-256 checked by the server at the end. A single m4b/m4a/mp3 goes as
 * it is; a folder of mp3s is sent as one zip. Afterwards the server imports
 * it in the background; the result is read from `/me/uploads`.
 */
@HiltWorker
class UploadWorker
@AssistedInject
constructor(
    @Assisted context: Context,
    @Assisted params: WorkerParameters,
    private val accounts: AccountStore,
    private val apis: ApiFactory,
    private val folders: LocalFolders,
    private val dao: MirrorDao,
    private val notifications: DownloadNotifications
) : CoroutineWorker(context, params) {
    private val bookId = params.inputData.getString(BOOK).orEmpty()
    private val accountId = params.inputData.getString(ACCOUNT).orEmpty()

    override suspend fun doWork(): Result {
        val account = accounts.current().accounts.firstOrNull { it.id == accountId } ?: return fail(UploadError.OTHER)
        val book = folders.book(bookId) ?: return Result.success()
        val row = book.row
        if (row.uploadState == UploadState.PROCESSING) return check(account, row.uploadUrl)
        runCatching { setForeground(notifications.uploadForeground(book.title, null)) }
        val source = prepare(book) ?: return fail(UploadError.UNSUPPORTED)
        val http = apis.http(account)
        return try {
            val url = row.uploadUrl ?: create(http, account, source) ?: return Result.success() // create() already recorded why
            dao.updateUpload(bookId, accountId, url, UploadState.SENDING, null, null)
            when (val sent = send(http, url, source, book.title)) {
                null -> {
                    dao.updateUpload(bookId, accountId, url, UploadState.PROCESSING, null, null)
                    source.cleanup()
                    check(account, url)
                }
                else -> sent
            }
        } catch (_: IOException) {
            Result.retry()
        }
    }

    /** What is sent: one file as it is, or the mp3s zipped. */
    private class Source(val name: String, val size: Long, val sha256: String, val open: () -> InputStream, val cleanup: () -> Unit = {})

    private suspend fun prepare(book: LocalBook): Source? = withContext(Dispatchers.IO) {
        val cr = applicationContext.contentResolver
        val files = book.files
        if (files.isEmpty()) return@withContext null
        val single = files.singleOrNull()
        if (single != null && single.name.substringAfterLast('.').lowercase() in SINGLE_EXT) {
            val uri = single.uri.toUri()
            val sha = cr.openInputStream(uri)?.use { sha256(it) } ?: return@withContext null
            return@withContext Source(single.name, single.sizeBytes, sha, { cr.openInputStream(uri) ?: throw IOException("gone") })
        }
        // Several files: the server takes a zip of one book's mp3s.
        if (files.any { it.name.substringAfterLast('.').lowercase() != "mp3" }) return@withContext null
        val zip = File(File(applicationContext.cacheDir, "uploads"), book.id.removePrefix(LocalFolders.PREFIX) + ".zip")
        if (!zip.isFile) {
            zip.parentFile?.mkdirs()
            val tmp = File(zip.path + ".tmp")
            ZipOutputStream(FileOutputStream(tmp)).use { out ->
                for (f in files) {
                    val uri = f.uri.toUri()
                    // Stored, not compressed: mp3 doesn't shrink, and stored entries need their CRC first.
                    val crc = CRC32()
                    cr.openInputStream(uri)?.use { input -> input.copyToDigest { b, n -> crc.update(b, 0, n) } } ?: throw IOException("gone")
                    val e =
                        ZipEntry(f.name).apply {
                            method = ZipEntry.STORED
                            size = f.sizeBytes
                            compressedSize = f.sizeBytes
                            this.crc = crc.value
                        }
                    out.putNextEntry(e)
                    cr.openInputStream(uri)?.use { it.copyTo(out) }
                    out.closeEntry()
                }
                // The folder's cover goes along, so the server's book has it too.
                book.row.coverPath?.let { File(it) }?.takeIf { it.isFile }?.let { cover ->
                    val bytes = cover.readBytes()
                    val crc = CRC32().apply { update(bytes) }
                    out.putNextEntry(
                        ZipEntry("cover.jpg").apply {
                            method = ZipEntry.STORED
                            size = bytes.size.toLong()
                            compressedSize = bytes.size.toLong()
                            this.crc = crc.value
                        }
                    )
                    out.write(bytes)
                    out.closeEntry()
                }
            }
            tmp.renameTo(zip)
        }
        val sha = zip.inputStream().use { sha256(it) }
        Source(book.title.replace(Regex("[\\\\/:*?\"<>|]"), "_") + ".zip", zip.length(), sha, { zip.inputStream() }, { zip.delete() })
    }

    /** Starts an upload on the server; null when it was refused (recorded). */
    private suspend fun create(http: OkHttpClient, account: Account, s: Source): String? = withContext(Dispatchers.IO) {
        val meta = "filename ${b64(s.name)},sha256 ${b64(s.sha256)}"
        val req =
            Request
                .Builder()
                .url(ServerUrl.api(account.serverUrl) + "uploads/")
                .post(ByteArray(0).toRequestBody())
                .header("Tus-Resumable", "1.0.0")
                .header("Upload-Length", s.size.toString())
                .header("Upload-Metadata", meta)
                .build()
        http.newCall(req).execute().use { res ->
            if (res.code != 201) {
                if (res.code >= 500) throw IOException("server ${res.code}")
                fail(errorFor(res.code))
                return@withContext null
            }
            val loc = res.header("Location") ?: throw IOException("no location")
            // Only the path: the server may name its own host, which can differ behind a proxy.
            val path = if (loc.startsWith("http")) loc.toUri().path.orEmpty() else loc
            ServerUrl.resolve(account.serverUrl, path)
        }
    }

    /** Sends what the server doesn't have yet; null when every byte has arrived. */
    private suspend fun send(http: OkHttpClient, url: String, s: Source, title: String): Result? = withContext(Dispatchers.IO) {
        val head = http.newCall(Request.Builder().url(url).head().header("Tus-Resumable", "1.0.0").build()).execute()
        val start =
            head.use {
                when {
                    it.code == 404 || it.code == 410 -> {
                        // The server dropped the partial upload: start a new one next time.
                        dao.updateUpload(bookId, accountId, null, UploadState.QUEUED, null, null)
                        return@withContext Result.retry()
                    }
                    !it.isSuccessful -> throw IOException("head ${it.code}")
                    else -> it.header("Upload-Offset")?.toLongOrNull() ?: 0L
                }
            }
        var offset = start
        s.open().use { input ->
            var skip = offset
            while (skip > 0) {
                val n = input.skip(skip)
                if (n <= 0) throw IOException("skip")
                skip -= n
            }
            val buf = ByteArray(CHUNK)
            while (offset < s.size) {
                if (isStopped) return@withContext Result.retry()
                var n = 0
                while (n < buf.size) {
                    val r = input.read(buf, n, buf.size - n)
                    if (r < 0) break
                    n += r
                }
                if (n == 0) throw IOException("short read")
                val req =
                    Request
                        .Builder()
                        .url(url)
                        .patch(buf.toRequestBody(OCTETS, 0, n))
                        .header("Tus-Resumable", "1.0.0")
                        .header("Upload-Offset", offset.toString())
                        .build()
                http.newCall(req).execute().use { res ->
                    when {
                        res.code == 204 -> offset = res.header("Upload-Offset")?.toLongOrNull() ?: (offset + n)
                        res.code >= 500 || res.code == 409 -> throw IOException("patch ${res.code}")
                        else -> return@withContext fail(errorFor(res.code))
                    }
                }
                runCatching { setForeground(notifications.uploadForeground(title, ((offset * 100) / s.size).toInt())) }
            }
        }
        null
    }

    /** Waits a little for the server's import, then records the result (or leaves it processing). */
    private suspend fun check(account: Account, url: String?): Result {
        val id = url?.trimEnd('/')?.substringAfterLast('/') ?: return Result.success()
        repeat(POLLS) {
            val item = runCatching { apis.forAccount(account).uploads.listMyUploads(limit = 50).body() }.getOrNull()?.firstOrNull { it.id == id }
            when (item?.status) {
                UploadItem.Status.DONE -> {
                    dao.updateUpload(bookId, accountId, null, UploadState.DONE, null, item.bookId?.toString())
                    return Result.success()
                }
                UploadItem.Status.FAILED, UploadItem.Status.CANCELLED -> {
                    dao.updateUpload(bookId, accountId, null, UploadState.FAILED, item.error ?: UploadError.REJECTED, null)
                    return Result.success()
                }
                else -> delay(POLL_MS)
            }
        }
        return Result.success() // still importing: the book's page asks again
    }

    private suspend fun fail(error: String): Result {
        dao.updateUpload(bookId, accountId, null, UploadState.FAILED, error, null)
        return Result.failure()
    }

    companion object {
        const val BOOK = "book"
        const val ACCOUNT = "account"
        private const val CHUNK = 8 shl 20
        private const val POLLS = 24
        private const val POLL_MS = 5_000L
        private val OCTETS = "application/offset+octet-stream".toMediaType()
        private val SINGLE_EXT = setOf("m4b", "m4a", "mp3")

        fun errorFor(code: Int) = when (code) {
            403 -> UploadError.FORBIDDEN
            413 -> UploadError.QUOTA
            415 -> UploadError.UNSUPPORTED
            else -> UploadError.OTHER
        }

        private fun b64(s: String) = Base64.encodeToString(s.toByteArray(), Base64.NO_WRAP)

        fun sha256(input: InputStream): String {
            val md = MessageDigest.getInstance("SHA-256")
            input.copyToDigest { b, n -> md.update(b, 0, n) }
            return md.digest().joinToString("") { "%02x".format(it) }
        }

        private inline fun InputStream.copyToDigest(update: (ByteArray, Int) -> Unit) {
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = read(buf)
                if (n < 0) break
                update(buf, n)
            }
        }

        /** Queues an upload of a book on this device to [accountId]'s server (Wi-Fi or not: the user asked). */
        suspend fun enqueue(context: Context, dao: MirrorDao, bookId: String, accountId: String) {
            val row = dao.localBook(bookId) ?: return
            // A new target, or a finished/failed attempt, starts fresh.
            val fresh = row.uploadAccountId != accountId || row.uploadState == UploadState.FAILED || row.uploadState == UploadState.DONE
            dao.updateUpload(bookId, accountId, if (fresh) null else row.uploadUrl, UploadState.QUEUED, null, null)
            WorkManager.getInstance(context).enqueueUniqueWork(
                "upload-$bookId",
                ExistingWorkPolicy.REPLACE,
                OneTimeWorkRequestBuilder<UploadWorker>()
                    .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                    .setInputData(workDataOf(BOOK to bookId, ACCOUNT to accountId))
                    .build()
            )
        }
    }
}
