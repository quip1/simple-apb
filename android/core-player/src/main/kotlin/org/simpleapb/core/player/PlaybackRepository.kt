// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.content.Context
import android.net.ConnectivityManager
import androidx.core.net.toUri
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.MimeTypes
import dagger.hilt.android.qualifiers.ApplicationContext
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
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.data.net.millis

sealed interface ReportResult {
    data object Ok : ReportResult

    data class Conflict(val conflict: PendingConflict) : ReportResult

    data class Failed(val failure: ApiFailure) : ReportResult
}

/** Loading books for playback, reporting progress and bookmarks. */
@Singleton
class PlaybackRepository
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val apis: ApiFactory,
    private val accounts: AccountStore,
    private val store: PlaybackStore
) {
    // Progress writes for one device go out one at a time, in order.
    private val writes = Mutex()

    suspend fun account(id: String): Account? = accounts.current().accounts.firstOrNull { it.id == id }

    suspend fun activeAccount(): Account? = accounts.current().active

    suspend fun load(account: Account, bookId: String): Outcome<LoadedBook> {
        val id = runCatching { UUID.fromString(bookId) }.getOrNull() ?: return Outcome.Failed(ApiFailure("not_found", "", 404))
        val api = apis.forAccount(account)
        return when (val r = call { api.library.getBook(id) }) {
            is Outcome.Failed -> r
            is Outcome.Ok -> {
                val d = r.value
                val settings = store.settings()
                val profile = hlsProfile(settings.quality, metered())
                val timeline = if (profile != null) hlsTimeline(d) else timeline(d)
                val start = chooseStart(d.progress, store.localPosition(bookId), timeline.durationMs)
                val speed = store.speed(bookId) ?: d.progress?.speed ?: settings.defaultSpeed
                Outcome.Ok(
                    LoadedBook(
                        id = bookId,
                        accountId = account.id,
                        serverUrl = account.serverUrl,
                        title = d.title,
                        authors = d.authors.joinToString(", ") { it.name },
                        narrators = d.narrators.joinToString(", ") { it.name },
                        coverUrl = if (d.hasCover) ServerUrl.resolve(account.serverUrl, d.coverUrl) else null,
                        timeline = timeline,
                        startMs = start,
                        speed = Speed.clamp(speed),
                        hlsProfile = profile
                    )
                )
            }
        }
    }

    private fun metered(): Boolean = context.getSystemService(ConnectivityManager::class.java)?.isActiveNetworkMetered ?: true

    /**
     * One playlist item per file; the session shows the book's title, author
     * and cover for each. The low-bitrate version is one HLS stream for the
     * whole book.
     */
    fun mediaItems(b: LoadedBook): List<MediaItem> = b.timeline.files.mapIndexed { i, f ->
        val uri =
            if (b.hlsProfile != null) {
                ServerUrl.api(b.serverUrl) + "stream/${b.id}/hls/${b.hlsProfile}/index.m3u8"
            } else {
                ServerUrl.api(b.serverUrl) + "books/${b.id}/files/${f.id}"
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
     * Offline reports are dropped here; the local position is kept, and the
     * outbox replaces this with the offline milestone.
     */
    suspend fun report(b: LoadedBook, positionMs: Long, speed: Float, finished: Boolean?, resolution: String?): ReportResult = writes.withLock {
        val account = account(b.accountId) ?: return ReportResult.Failed(ApiFailure("signed_out", "", 401))
        store.saveLocalPosition(b.id, positionMs, System.currentTimeMillis())
        val body =
            ProgressUpdate(
                positionMs = positionMs.coerceAtLeast(0),
                deviceId = UUID.fromString(accounts.deviceId()),
                clientTs = Instant.now().toString(),
                speed = Speed.clamp(speed),
                finished = finished,
                lastSyncedAt = store.syncedAt(b.id),
                resolution = resolution
            )
        try {
            val res = apis.forAccount(account).progress.putProgress(UUID.fromString(b.id), body)
            when {
                res.isSuccessful -> {
                    store.markSynced(b.id, Instant.now().toString())
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
        val synced = store.syncedAt(bookId)

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
            var res = api.putProgress(id, body(null))
            if (res.code() == 409) {
                val raw = runCatching { res.errorBody()?.string() }.getOrNull().orEmpty()
                val c = runCatching { Serializer.kotlinxSerializationJson.decodeFromString(ProgressConflict.serializer(), raw) }.getOrNull()
                if (c != null) res = api.putProgress(id, body(c.conflictId))
            }
            if (res.isSuccessful) {
                store.markSynced(bookId, Instant.now().toString())
                Outcome.Ok(Unit)
            } else {
                Outcome.Failed(ApiFailure.from(res))
            }
        } catch (_: IOException) {
            Outcome.Failed(ApiFailure.Offline)
        }
    }

    suspend fun bookmarks(b: LoadedBook): Outcome<List<Bookmark>> {
        val account = account(b.accountId) ?: return Outcome.Failed(ApiFailure("signed_out", "", 401))
        return call { apis.forAccount(account).progress.listBookmarks(UUID.fromString(b.id)) }
    }

    suspend fun addBookmark(b: LoadedBook, positionMs: Long, note: String?): Outcome<Bookmark> {
        val account = account(b.accountId) ?: return Outcome.Failed(ApiFailure("signed_out", "", 401))
        val body =
            BookmarkCreate(
                bookId = UUID.fromString(b.id),
                positionMs = positionMs.coerceAtLeast(0),
                id = UUID.randomUUID(),
                note = note?.trim()?.ifEmpty { null },
                clientTs = Instant.now().toString()
            )
        return call { apis.forAccount(account).progress.createBookmark(body) }
    }

    suspend fun deleteBookmark(b: LoadedBook, id: UUID): Outcome<Unit> {
        val account = account(b.accountId) ?: return Outcome.Failed(ApiFailure("signed_out", "", 401))
        return call { apis.forAccount(account).progress.deleteBookmark(id) }
    }

    companion object {
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
