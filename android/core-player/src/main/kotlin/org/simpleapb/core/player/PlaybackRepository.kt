// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.content.Context
import android.net.ConnectivityManager
import androidx.core.net.toUri
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.MimeTypes
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.File
import java.io.IOException
import java.time.Instant
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.simpleapb.api.infrastructure.Serializer
import org.simpleapb.api.model.BookDetail
import org.simpleapb.api.model.Bookmark
import org.simpleapb.api.model.BookmarkCreate
import org.simpleapb.api.model.Progress
import org.simpleapb.api.model.ProgressConflict
import org.simpleapb.api.model.ProgressUpdate
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.download.DownloadWorker
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.local.LocalFolders
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.data.net.millis
import org.simpleapb.core.data.sync.LocalLibrary
import org.simpleapb.core.data.sync.SyncEngine

sealed interface ReportResult {
    data object Ok : ReportResult

    data class Conflict(val conflict: PendingConflict) : ReportResult

    data class Failed(val failure: ApiFailure) : ReportResult
}

/**
 * Loading books for playback (from downloads when there are any, which also
 * works offline), reporting progress and bookmarks. Writes that can't reach
 * the server go to the outbox and are sent by the sync engine.
 */
@Singleton
class PlaybackRepository
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val apis: ApiFactory,
    private val accounts: AccountStore,
    private val store: PlaybackStore,
    private val local: LocalLibrary,
    private val downloads: Downloads,
    private val engine: SyncEngine,
    private val folders: LocalFolders
) {
    // Progress writes for one device go out one at a time, in order.
    private val writes = Mutex()

    suspend fun account(id: String): Account? = accounts.current().accounts.firstOrNull { it.id == id }

    suspend fun activeAccount(): Account? = accounts.current().active

    suspend fun load(account: Account, bookId: String): Outcome<LoadedBook> {
        val id = runCatching { UUID.fromString(bookId) }.getOrNull() ?: return Outcome.Failed(ApiFailure("not_found", "", 404))
        val dir = downloads.completeDir(account.id, bookId)
        val online = call { apis.forAccount(account).library.getBook(id) }
        if (online is Outcome.Ok) local.saveDetail(account.id, online.value)
        // A download plays its own files (offline too); otherwise the server's details are needed.
        val d =
            when {
                dir != null -> local.detail(account.id, bookId) ?: (online as? Outcome.Ok)?.value
                else -> (online as? Outcome.Ok)?.value
            } ?: return online as? Outcome.Failed ?: Outcome.Failed(ApiFailure.Offline)
        val progress = (online as? Outcome.Ok)?.value?.progress ?: local.progress(account.id, bookId)
        val settings = store.settings()
        val profile = if (dir != null) null else hlsProfile(settings.quality, metered())
        val timeline = if (profile != null) hlsTimeline(d) else timeline(d)
        val start = chooseStart(progress, store.localPosition(bookId), timeline.durationMs)
        val speed = store.speed(bookId) ?: progress?.speed ?: settings.defaultSpeed
        val localCover = dir?.let { File(it, DownloadWorker.COVER) }?.takeIf { it.isFile }
        return Outcome.Ok(
            LoadedBook(
                id = bookId,
                accountId = account.id,
                serverUrl = account.serverUrl,
                title = d.title,
                authors = d.authors.joinToString(", ") { it.name },
                narrators = d.narrators.joinToString(", ") { it.name },
                coverUrl = localCover?.toUri()?.toString() ?: if (d.hasCover) ServerUrl.resolve(account.serverUrl, d.coverUrl) else null,
                timeline = timeline,
                startMs = start,
                speed = Speed.clamp(speed),
                hlsProfile = profile,
                localDir = dir?.path,
                seriesId = d.series?.id?.toString()
            )
        )
    }

    /**
     * A book from a folder on this device: its files play in order, each file
     * is a chapter (embedded chapter marks aren't read), and its position
     * and bookmarks stay on this device.
     */
    suspend fun loadLocal(bookId: String): Outcome<LoadedBook> {
        val book = folders.book(bookId) ?: return Outcome.Failed(ApiFailure("not_found", "", 404))
        var offset = 0L
        val files =
            book.files.map { f ->
                TimelineFile(f.uri, offset, f.durationMs, f.mimeType).also { offset += f.durationMs }
            }
        val chapters =
            if (book.files.size > 1) {
                files.mapIndexed { i, f -> TimelineChapter(i, book.files[i].name.substringBeforeLast('.'), f.offsetMs, f.offsetMs + f.durationMs) }
            } else {
                listOf(TimelineChapter(0, book.title, 0, offset))
            }
        val timeline = BookTimeline(files, chapters)
        val settings = store.settings()
        return Outcome.Ok(
            LoadedBook(
                id = bookId,
                accountId = LOCAL_ACCOUNT,
                serverUrl = "",
                title = book.title,
                authors = book.row.author.orEmpty(),
                narrators = "",
                coverUrl = book.row.coverPath?.let { File(it).toUri().toString() },
                timeline = timeline,
                startMs = chooseStart(null, store.localPosition(bookId), timeline.durationMs),
                speed = Speed.clamp(store.speed(bookId) ?: settings.defaultSpeed)
            )
        )
    }

    /** A position conflict found while this device was offline, to ask about now. */
    suspend fun storedConflict(b: LoadedBook): PendingConflict? =
        local.conflict(b.accountId, b.id)?.let { PendingConflict(b.id, it.conflictId, it.serverPositionMs, it.serverDevice) }

    suspend fun clearStoredConflict(b: LoadedBook) = local.clearConflict(b.accountId, b.id)

    private fun metered(): Boolean = context.getSystemService(ConnectivityManager::class.java)?.isActiveNetworkMetered ?: true

    /**
     * One playlist item per file; the session shows the book's title, author
     * and cover for each. The low-bitrate version is one HLS stream for the
     * whole book.
     */
    fun mediaItems(b: LoadedBook): List<MediaItem> = b.timeline.files.mapIndexed { i, f ->
        val uri =
            when {
                b.isLocal -> f.id // a file on this device: its content:// address
                b.localDir != null -> File(b.localDir, "${f.id}.${DownloadWorker.extension(f.mimeType)}").toUri().toString()
                b.hlsProfile != null -> ServerUrl.api(b.serverUrl) + "stream/${b.id}/hls/${b.hlsProfile}/index.m3u8"
                else -> ServerUrl.api(b.serverUrl) + "books/${b.id}/files/${f.id}"
            }
        MediaItem
            .Builder()
            .setMediaId("${b.id}/$i")
            .setUri(uri)
            .setMimeType(if (b.hlsProfile != null) MimeTypes.APPLICATION_M3U8 else f.mimeType)
            .setMediaMetadata(
                MediaMetadata
                    .Builder()
                    .setTitle(b.title)
                    .setArtist(b.authors)
                    .setAlbumTitle(b.title)
                    .setAlbumArtist(b.authors)
                    .setArtworkUri(b.coverUrl?.toUri())
                    .setTrackNumber(i + 1)
                    .setTotalTrackCount(b.timeline.files.size)
                    .setIsPlayable(true)
                    .setIsBrowsable(false)
                    .setMediaType(MediaMetadata.MEDIA_TYPE_AUDIO_BOOK_CHAPTER)
                    .build()
            ).build()
    }

    /** Remembers the position on this device, whatever happens to the report. */
    suspend fun saveLocal(bookId: String, positionMs: Long) {
        store.saveLocalPosition(bookId, positionMs, System.currentTimeMillis())
    }

    /**
     * Reports the position (brief §6.8). A 409 means another device holds a
     * far-away position written in the same offline window: the user decides.
     * A report that can't reach the server goes to the outbox (brief §6.8).
     */
    suspend fun report(b: LoadedBook, positionMs: Long, speed: Float, finished: Boolean?, resolution: String?): ReportResult = writes.withLock {
        if (b.isLocal) {
            store.saveLocalPosition(b.id, positionMs, System.currentTimeMillis())
            return ReportResult.Ok
        }
        val account = account(b.accountId) ?: return ReportResult.Failed(ApiFailure("signed_out", "", 401))
        store.saveLocalPosition(b.id, positionMs, System.currentTimeMillis())
        val body =
            ProgressUpdate(
                positionMs = positionMs.coerceAtLeast(0),
                deviceId = UUID.fromString(accounts.deviceId()),
                clientTs = Instant.now().toString(),
                speed = Speed.clamp(speed),
                finished = finished,
                lastSyncedAt = local.syncedAt(account.id, b.id),
                resolution = resolution
            )
        try {
            engine.wrote()
            val res = apis.forAccount(account).progress.putProgress(UUID.fromString(b.id), body)
            when {
                res.isSuccessful -> {
                    local.markSynced(account.id, b.id)
                    res.body()?.let { local.saveProgress(account.id, listOf(it.progress)) }
                    if (resolution != null) local.clearConflict(account.id, b.id)
                    ReportResult.Ok
                }
                res.code() == 409 -> {
                    val raw = runCatching { res.errorBody()?.string() }.getOrNull().orEmpty()
                    val c = runCatching { Serializer.kotlinxSerializationJson.decodeFromString(ProgressConflict.serializer(), raw) }.getOrNull()
                    if (c == null) {
                        ReportResult.Failed(ApiFailure("progress_conflict", "", 409))
                    } else {
                        ReportResult.Conflict(PendingConflict(b.id, c.conflictId, c.server.positionMs, c.server.deviceName))
                    }
                }
                else -> ReportResult.Failed(ApiFailure.from(res))
            }
        } catch (_: IOException) {
            local.queueProgress(account.id, b.id, body)
            engine.requestSync()
            ReportResult.Failed(ApiFailure.Offline)
        }
    }

    /**
     * Marks a book finished, or not finished, from its page. That is an
     * explicit choice, so it settles a device conflict in its own favour.
     */
    suspend fun setFinished(account: Account, bookId: String, durationMs: Long, speed: Float, finished: Boolean): Outcome<Unit> = writes.withLock {
        val id = runCatching { UUID.fromString(bookId) }.getOrNull() ?: return Outcome.Failed(ApiFailure("not_found", "", 404))
        val pos = if (finished) durationMs.coerceAtLeast(0) else 0L
        store.saveLocalPosition(bookId, pos, System.currentTimeMillis())
        val device = UUID.fromString(accounts.deviceId())
        val synced = local.syncedAt(account.id, bookId)

        fun body(resolution: String?) = ProgressUpdate(
            positionMs = pos,
            deviceId = device,
            clientTs = Instant.now().toString(),
            speed = Speed.clamp(speed),
            finished = finished,
            lastSyncedAt = synced,
            resolution = resolution
        )
        try {
            val api = apis.forAccount(account).progress
            engine.wrote()
            var res = api.putProgress(id, body(null))
            if (res.code() == 409) {
                val raw = runCatching { res.errorBody()?.string() }.getOrNull().orEmpty()
                val c = runCatching { Serializer.kotlinxSerializationJson.decodeFromString(ProgressConflict.serializer(), raw) }.getOrNull()
                if (c != null) res = api.putProgress(id, body(c.conflictId))
            }
            if (res.isSuccessful) {
                local.markSynced(account.id, bookId)
                res.body()?.let { local.saveProgress(account.id, listOf(it.progress)) }
                local.clearConflict(account.id, bookId)
                Outcome.Ok(Unit)
            } else {
                Outcome.Failed(ApiFailure.from(res))
            }
        } catch (_: IOException) {
            // Offline: the choice waits in the outbox like any other position.
            local.queueProgress(account.id, bookId, body(null))
            engine.requestSync()
            Outcome.Ok(Unit)
        }
    }

    /** Downloads the next unfinished book of [b]'s series, if downloads are on for it. */
    suspend fun autoNext(b: LoadedBook) {
        val series = b.seriesId?.let { runCatching { UUID.fromString(it) }.getOrNull() } ?: return
        if (!downloads.settings().autoNext) return
        val account = account(b.accountId) ?: return
        val page = (call { apis.forAccount(account).library.getSeries(series) } as? Outcome.Ok)?.value ?: return
        val i = page.books.indexOfFirst { it.id.toString() == b.id }
        if (i < 0) return
        val next = page.books.drop(i + 1).firstOrNull { it.progress?.finishedAt == null } ?: return
        if (downloads.get(account.id, next.id.toString()) != null) return
        downloads.enqueue(account.id, next.id.toString(), next.title, auto = true)
    }

    /** The book's bookmarks; the mirror's copy when the server is out of reach. */
    suspend fun bookmarks(b: LoadedBook): Outcome<List<Bookmark>> {
        if (b.isLocal) return Outcome.Ok(local.bookmarks(LOCAL_ACCOUNT, bookUuid(b.id).toString()))
        val account = account(b.accountId) ?: return Outcome.Failed(ApiFailure("signed_out", "", 401))
        return when (val r = call { apis.forAccount(account).progress.listBookmarks(UUID.fromString(b.id)) }) {
            is Outcome.Ok -> {
                local.saveBookmarks(account.id, r.value)
                r
            }
            is Outcome.Failed -> if (r.failure == ApiFailure.Offline) Outcome.Ok(local.bookmarks(account.id, b.id)) else r
        }
    }

    suspend fun addBookmark(b: LoadedBook, positionMs: Long, note: String?): Outcome<Bookmark> {
        val body =
            BookmarkCreate(
                bookId = bookUuid(b.id),
                positionMs = positionMs.coerceAtLeast(0),
                id = UUID.randomUUID(),
                note = note?.trim()?.ifEmpty { null },
                clientTs = Instant.now().toString()
            )
        if (b.isLocal) {
            val m = Bookmark(body.id!!, body.bookId, body.positionMs, body.clientTs!!, body.note)
            local.saveBookmarks(LOCAL_ACCOUNT, listOf(m))
            return Outcome.Ok(m)
        }
        val account = account(b.accountId) ?: return Outcome.Failed(ApiFailure("signed_out", "", 401))
        engine.wrote()
        return when (val r = call { apis.forAccount(account).progress.createBookmark(body) }) {
            is Outcome.Ok -> {
                local.saveBookmarks(account.id, listOf(r.value))
                r
            }
            is Outcome.Failed -> {
                if (r.failure != ApiFailure.Offline) return r
                // Offline: kept here and sent later with the same id.
                local.queueBookmark(account.id, body)
                engine.requestSync()
                Outcome.Ok(Bookmark(body.id!!, body.bookId, body.positionMs, body.clientTs!!, body.note))
            }
        }
    }

    suspend fun deleteBookmark(b: LoadedBook, id: UUID): Outcome<Unit> {
        if (b.isLocal) {
            local.dao.deleteBookmark(LOCAL_ACCOUNT, id.toString())
            return Outcome.Ok(Unit)
        }
        val account = account(b.accountId) ?: return Outcome.Failed(ApiFailure("signed_out", "", 401))
        engine.wrote()
        return when (val r = call { apis.forAccount(account).progress.deleteBookmark(id) }) {
            is Outcome.Ok -> {
                local.dao.deleteBookmark(account.id, id.toString())
                r
            }
            is Outcome.Failed -> {
                if (r.failure != ApiFailure.Offline) return r
                local.queueBookmarkDelete(account.id, id.toString())
                engine.requestSync()
                Outcome.Ok(Unit)
            }
        }
    }

    companion object {
        /** The "account" of books in folders on this device (bookmarks and the last book). */
        const val LOCAL_ACCOUNT = "local"

        /** A book's id as a UUID; books on this device get a stable one derived from theirs. */
        fun bookUuid(id: String): UUID = if (LocalFolders.isLocal(id)) UUID.nameUUIDFromBytes(id.toByteArray()) else UUID.fromString(id)

        /** The HLS profile to stream, or null for the original files. */
        fun hlsProfile(quality: Quality, metered: Boolean): String? = when (quality) {
            Quality.ORIGINAL -> null
            Quality.MEDIUM -> "medium"
            Quality.LOW -> "low"
            Quality.AUTO -> if (metered) "medium" else null
        }

        /** The whole book as one stream, with its chapters. */
        fun hlsTimeline(d: BookDetail) = BookTimeline(
            listOf(TimelineFile("hls", 0, d.durationMs, MimeTypes.APPLICATION_M3U8)),
            d.chapters.map { TimelineChapter(it.index, it.title, it.startMs, it.endMs) }
        )

        fun timeline(d: BookDetail) = BookTimeline(
            d.files.map { TimelineFile(it.id.toString(), it.offsetMs, it.durationMs, it.mimeType) },
            d.chapters.map { TimelineChapter(it.index, it.title, it.startMs, it.endMs) }
        )

        /**
         * Where to start: the newer of the server's record and this device's
         * own last position (a report may not have reached the server). A
         * book that was played to the end starts again from the beginning.
         */
        fun chooseStart(server: Progress?, local: LocalPosition?, durationMs: Long): Long {
            val pick =
                when {
                    server == null -> local?.positionMs ?: 0
                    local == null -> server.positionMs
                    local.atMs > millis(server.clientTs) -> local.positionMs
                    else -> server.positionMs
                }
            return if (durationMs > 0 && pick >= durationMs - END_MARGIN_MS) 0 else pick.coerceAtLeast(0)
        }

        private const val END_MARGIN_MS = 2_000L
    }
}
