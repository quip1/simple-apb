// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.sync

import java.time.Instant
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import org.simpleapb.api.infrastructure.Serializer
import org.simpleapb.api.model.BookDetail
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.Bookmark
import org.simpleapb.api.model.BookmarkCreate
import org.simpleapb.api.model.Collection
import org.simpleapb.api.model.Progress
import org.simpleapb.api.model.ProgressUpdate
import org.simpleapb.core.data.db.BookRow
import org.simpleapb.core.data.db.BookmarkRow
import org.simpleapb.core.data.db.CollectionRow
import org.simpleapb.core.data.db.ConflictRow
import org.simpleapb.core.data.db.DetailRow
import org.simpleapb.core.data.db.MirrorDao
import org.simpleapb.core.data.db.OutboxRow
import org.simpleapb.core.data.db.ProgressRow
import org.simpleapb.core.data.db.SyncedRow

/** A progress write waiting in the outbox. */
@Serializable
data class QueuedProgress(val bookId: String, val update: ProgressUpdate)

/**
 * Reading and writing the mirror in API terms, and queueing offline writes
 * (brief §7.5). Everything is per account.
 */
@Singleton
class LocalLibrary
@Inject
constructor(val dao: MirrorDao) {
    private val json = Serializer.kotlinxSerializationJson

    // ── books and details ──

    suspend fun saveBooks(accountId: String, books: List<BookSummary>) {
        if (books.isEmpty()) return
        dao.upsertBooks(books.map { BookRow(accountId, it.id.toString(), it.title, json.encodeToString(BookSummary.serializer(), it), it.updatedAt) })
    }

    suspend fun books(accountId: String): List<BookSummary> = dao.books(accountId).mapNotNull { decodeBook(it.json) }

    suspend fun book(accountId: String, bookId: String): BookSummary? = dao.book(accountId, bookId)?.let { decodeBook(it.json) }

    private fun decodeBook(s: String) = runCatching { json.decodeFromString(BookSummary.serializer(), s) }.getOrNull()

    suspend fun saveDetail(accountId: String, d: BookDetail) {
        dao.upsertDetail(DetailRow(accountId, d.id.toString(), json.encodeToString(BookDetail.serializer(), d), System.currentTimeMillis()))
    }

    suspend fun detail(accountId: String, bookId: String): BookDetail? = dao.detail(accountId, bookId)?.let {
        runCatching { json.decodeFromString(BookDetail.serializer(), it.json) }.getOrNull()
    }

    // ── progress ──

    suspend fun saveProgress(accountId: String, list: List<Progress>) {
        if (list.isEmpty()) return
        dao.upsertProgress(
            list.map {
                ProgressRow(accountId, it.bookId.toString(), it.positionMs, it.clientTs, it.speed, it.finishedAt, it.deviceName)
            }
        )
    }

    /** The newest progress this device knows of (from the server, or written here). */
    suspend fun progress(accountId: String, bookId: String): Progress? = dao.progress(accountId, bookId)?.let { it.toProgress() }

    suspend fun allProgress(accountId: String): Map<String, Progress> = dao.allProgress(accountId).associate { it.bookId to it.toProgress() }

    private fun ProgressRow.toProgress() = Progress(
        bookId = UUID.fromString(bookId),
        positionMs = positionMs,
        deviceId = NO_DEVICE,
        clientTs = clientTs,
        updatedAt = clientTs,
        speed = speed,
        deviceName = deviceName,
        finishedAt = finishedAt
    )

    suspend fun syncedAt(accountId: String, bookId: String): String? = dao.syncedAt(accountId, bookId)

    suspend fun markSynced(accountId: String, bookId: String, at: String = Instant.now().toString()) {
        dao.upsertSynced(SyncedRow(accountId, bookId, at))
    }

    // ── bookmarks and collections ──

    suspend fun saveBookmarks(accountId: String, list: List<Bookmark>) {
        if (list.isEmpty()) return
        dao.upsertBookmarks(list.map { BookmarkRow(accountId, it.id.toString(), it.bookId.toString(), it.positionMs, it.note, it.updatedAt) })
    }

    suspend fun bookmarks(accountId: String, bookId: String): List<Bookmark> = dao.bookmarks(accountId, bookId).map {
        Bookmark(UUID.fromString(it.id), UUID.fromString(it.bookId), it.positionMs, it.updatedAt, it.note)
    }

    suspend fun saveCollections(accountId: String, list: List<Collection>) {
        if (list.isEmpty()) return
        dao.upsertCollections(
            list.map { c ->
                CollectionRow(
                    accountId,
                    c.id.toString(),
                    c.name,
                    json.encodeToString(
                        ListSerializer(String.serializer()),
                        c.bookIds.map {
                            it.toString()
                        }
                    ),
                    c.updatedAt
                )
            }
        )
    }

    // ── the outbox ──

    /** Queues a progress write; an older queued write for the same book is replaced. */
    suspend fun queueProgress(accountId: String, bookId: String, update: ProgressUpdate) {
        dao.enqueue(
            OutboxRow(
                accountId = accountId,
                kind = KIND_PROGRESS,
                key = "progress:$bookId",
                payload = json.encodeToString(QueuedProgress.serializer(), QueuedProgress(bookId, update)),
                createdAt = System.currentTimeMillis()
            )
        )
        // The device's own newest position, so offline screens and the next load agree.
        val p = update.finished
        dao.upsertProgress(
            listOf(
                ProgressRow(
                    accountId,
                    bookId,
                    update.positionMs,
                    update.clientTs,
                    update.speed,
                    if (p == true) {
                        update.clientTs
                    } else if (p == false) {
                        null
                    } else {
                        dao.progress(accountId, bookId)?.finishedAt
                    },
                    null
                )
            )
        )
    }

    suspend fun queueBookmark(accountId: String, b: BookmarkCreate) {
        val id = b.id?.toString() ?: return
        dao.enqueue(
            OutboxRow(
                accountId = accountId,
                kind = KIND_BOOKMARK_CREATE,
                key = "bookmark:$id",
                payload = json.encodeToString(BookmarkCreate.serializer(), b),
                createdAt = System.currentTimeMillis()
            )
        )
        dao.upsertBookmarks(listOf(BookmarkRow(accountId, id, b.bookId.toString(), b.positionMs, b.note, b.clientTs ?: Instant.now().toString())))
    }

    /** Queues a delete; a bookmark that never reached the server is simply dropped. */
    suspend fun queueBookmarkDelete(accountId: String, id: String) {
        val pending = dao.outbox(accountId).any { it.key == "bookmark:$id" && it.kind == KIND_BOOKMARK_CREATE }
        if (pending) {
            dao.deleteOutboxKey(accountId, "bookmark:$id")
        } else {
            dao.enqueue(
                OutboxRow(
                    accountId = accountId,
                    kind = KIND_BOOKMARK_DELETE,
                    key = "bookmark:$id",
                    payload = id,
                    createdAt = System.currentTimeMillis()
                )
            )
        }
        dao.deleteBookmark(accountId, id)
    }

    // ── conflicts found while offline ──

    suspend fun conflict(accountId: String, bookId: String): ConflictRow? = dao.conflict(accountId, bookId)

    suspend fun clearConflict(accountId: String, bookId: String) = dao.deleteConflict(accountId, bookId)

    fun decodeProgress(payload: String): QueuedProgress? = runCatching { json.decodeFromString(QueuedProgress.serializer(), payload) }.getOrNull()

    fun decodeBookmark(payload: String): BookmarkCreate? = runCatching { json.decodeFromString(BookmarkCreate.serializer(), payload) }.getOrNull()

    companion object {
        const val KIND_PROGRESS = "progress"
        const val KIND_BOOKMARK_CREATE = "bookmark_create"
        const val KIND_BOOKMARK_DELETE = "bookmark_delete"

        /** Progress rebuilt from the mirror carries no device id. */
        val NO_DEVICE: UUID = UUID(0, 0)
    }
}
