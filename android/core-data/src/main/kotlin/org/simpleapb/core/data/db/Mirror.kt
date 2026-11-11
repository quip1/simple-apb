// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.db

import android.content.Context
import androidx.room.AutoMigration
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Insert
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.room.Transaction
import androidx.room.Upsert
import kotlinx.coroutines.flow.Flow

/*
 * The device's copy of what each signed-in account can see (brief §7.5),
 * keyed by account so several servers live side by side. Books and details
 * are kept as the API's own JSON, so new server fields need no migration.
 */

/** A book in the account's library (`BookSummary` JSON). */
@Entity(tableName = "books", primaryKeys = ["accountId", "bookId"])
data class BookRow(val accountId: String, val bookId: String, val title: String, val json: String, val updatedAt: String)

/** A book's full details (`BookDetail` JSON): files and chapters for offline playback. */
@Entity(tableName = "details", primaryKeys = ["accountId", "bookId"])
data class DetailRow(val accountId: String, val bookId: String, val json: String, val savedAt: Long)

@Entity(tableName = "progress", primaryKeys = ["accountId", "bookId"])
data class ProgressRow(
    val accountId: String,
    val bookId: String,
    val positionMs: Long,
    val clientTs: String,
    val speed: Float,
    val finishedAt: String?,
    val deviceName: String?
)

@Entity(tableName = "bookmarks", primaryKeys = ["accountId", "id"])
data class BookmarkRow(val accountId: String, val id: String, val bookId: String, val positionMs: Long, val note: String?, val updatedAt: String)

@Entity(tableName = "collections", primaryKeys = ["accountId", "id"])
data class CollectionRow(val accountId: String, val id: String, val name: String, val bookIds: String, val updatedAt: String)

/**
 * A write made while the server was out of reach, sent in order once it is
 * reachable again. [key] collapses repeats: only the newest progress write
 * per book is kept (the server keeps the latest by client time anyway).
 */
@Entity(tableName = "outbox")
data class OutboxRow(
    @PrimaryKey(autoGenerate = true) val seq: Long = 0,
    val accountId: String,
    val kind: String,
    val key: String,
    val payload: String,
    val createdAt: Long,
    val attempts: Int = 0
)

/** A position conflict found while flushing the outbox; asked about when the book is next opened. */
@Entity(tableName = "conflicts", primaryKeys = ["accountId", "bookId"])
data class ConflictRow(val accountId: String, val bookId: String, val conflictId: String, val serverPositionMs: Long, val serverDevice: String?)

/** When this device last wrote a book's progress successfully (sent as `last_synced_at`). */
@Entity(tableName = "synced", primaryKeys = ["accountId", "bookId"])
data class SyncedRow(val accountId: String, val bookId: String, val at: String)

@Entity(tableName = "sync_state")
data class SyncStateRow(@PrimaryKey val accountId: String, val cursor: String?, val lastSyncAt: Long)

/** A downloaded (or downloading) book. Files live in [dir]/<file id>.<ext>. */
@Entity(tableName = "downloads", primaryKeys = ["accountId", "bookId"])
data class DownloadRow(
    val accountId: String,
    val bookId: String,
    val title: String,
    val state: String,
    val auto: Boolean,
    val dir: String,
    val totalBytes: Long,
    val doneBytes: Long,
    val error: String?,
    val requestedAt: Long,
    val finishedAt: Long?
)

/**
 * A book in a folder on this device (brief §7.4 screen 9). [id] is
 * `local:` + a hash of the folder's document URI; [files] is JSON of
 * [LocalFile]s in playing order.
 */
@Entity(tableName = "local_books")
data class LocalBookRow(
    @PrimaryKey val id: String,
    val treeUri: String,
    val folderUri: String,
    val title: String,
    val author: String?,
    val durationMs: Long,
    val sizeBytes: Long,
    val files: String,
    val coverPath: String?,
    /** The folder's newest file time, to skip unchanged folders on a rescan. */
    val modified: Long,
    /** Upload to a server: the account, the tus URL while sending, and the state. */
    val uploadAccountId: String? = null,
    val uploadUrl: String? = null,
    val uploadState: String? = null,
    val uploadError: String? = null,
    val uploadBookId: String? = null
)

@Dao
interface MirrorDao {
    // ── books in folders on this device ──
    @Upsert suspend fun upsertLocalBooks(rows: List<LocalBookRow>)

    @Query("SELECT * FROM local_books ORDER BY title COLLATE NOCASE")
    fun localBooks(): Flow<List<LocalBookRow>>

    @Query("SELECT * FROM local_books ORDER BY title COLLATE NOCASE")
    suspend fun allLocalBooks(): List<LocalBookRow>

    @Query("SELECT * FROM local_books WHERE id = :id")
    suspend fun localBook(id: String): LocalBookRow?

    @Query("DELETE FROM local_books WHERE id IN (:ids)")
    suspend fun deleteLocalBooks(ids: List<String>)

    @Query(
        "UPDATE local_books SET uploadAccountId = :account, uploadUrl = :url, uploadState = :state, uploadError = :error, uploadBookId = :bookId WHERE id = :id"
    )
    suspend fun updateUpload(id: String, account: String?, url: String?, state: String?, error: String?, bookId: String?)

    // ── books ──
    @Upsert suspend fun upsertBooks(rows: List<BookRow>)

    @Query("DELETE FROM books WHERE accountId = :a AND bookId = :b")
    suspend fun deleteBook(a: String, b: String)

    @Query("SELECT * FROM books WHERE accountId = :a ORDER BY title COLLATE NOCASE")
    suspend fun books(a: String): List<BookRow>

    @Query("SELECT * FROM books WHERE accountId = :a AND bookId = :b")
    suspend fun book(a: String, b: String): BookRow?

    // ── details ──
    @Upsert suspend fun upsertDetail(row: DetailRow)

    @Query("SELECT * FROM details WHERE accountId = :a AND bookId = :b")
    suspend fun detail(a: String, b: String): DetailRow?

    @Query("DELETE FROM details WHERE accountId = :a AND bookId = :b")
    suspend fun deleteDetail(a: String, b: String)

    // ── progress ──
    @Upsert suspend fun upsertProgress(rows: List<ProgressRow>)

    @Query("SELECT * FROM progress WHERE accountId = :a AND bookId = :b")
    suspend fun progress(a: String, b: String): ProgressRow?

    @Query("SELECT * FROM progress WHERE accountId = :a")
    suspend fun allProgress(a: String): List<ProgressRow>

    @Query("DELETE FROM progress WHERE accountId = :a AND bookId = :b")
    suspend fun deleteProgress(a: String, b: String)

    // ── bookmarks ──
    @Upsert suspend fun upsertBookmarks(rows: List<BookmarkRow>)

    @Query("SELECT * FROM bookmarks WHERE accountId = :a AND bookId = :b ORDER BY positionMs")
    suspend fun bookmarks(a: String, b: String): List<BookmarkRow>

    @Query("DELETE FROM bookmarks WHERE accountId = :a AND id = :id")
    suspend fun deleteBookmark(a: String, id: String)

    // ── collections ──
    @Upsert suspend fun upsertCollections(rows: List<CollectionRow>)

    @Query("DELETE FROM collections WHERE accountId = :a AND id = :id")
    suspend fun deleteCollection(a: String, id: String)

    // ── outbox ──
    @Insert suspend fun insertOutbox(row: OutboxRow): Long

    @Query("DELETE FROM outbox WHERE accountId = :a AND `key` = :key")
    suspend fun deleteOutboxKey(a: String, key: String)

    /** Queues [row], replacing an older write with the same key. */
    @Transaction
    suspend fun enqueue(row: OutboxRow) {
        deleteOutboxKey(row.accountId, row.key)
        insertOutbox(row)
    }

    @Query("SELECT * FROM outbox WHERE accountId = :a ORDER BY seq")
    suspend fun outbox(a: String): List<OutboxRow>

    @Query("SELECT COUNT(*) FROM outbox")
    fun outboxCount(): Flow<Int>

    @Query("DELETE FROM outbox WHERE seq = :seq")
    suspend fun deleteOutbox(seq: Long)

    @Query("UPDATE outbox SET attempts = attempts + 1 WHERE seq = :seq")
    suspend fun bumpOutbox(seq: Long)

    // ── conflicts ──
    @Upsert suspend fun upsertConflict(row: ConflictRow)

    @Query("SELECT * FROM conflicts WHERE accountId = :a AND bookId = :b")
    suspend fun conflict(a: String, b: String): ConflictRow?

    @Query("DELETE FROM conflicts WHERE accountId = :a AND bookId = :b")
    suspend fun deleteConflict(a: String, b: String)

    // ── last successful write per book ──
    @Upsert suspend fun upsertSynced(row: SyncedRow)

    @Query("SELECT at FROM synced WHERE accountId = :a AND bookId = :b")
    suspend fun syncedAt(a: String, b: String): String?

    // ── sync state ──
    @Upsert suspend fun upsertSyncState(row: SyncStateRow)

    @Query("SELECT * FROM sync_state WHERE accountId = :a")
    suspend fun syncState(a: String): SyncStateRow?

    // ── downloads ──
    @Upsert suspend fun upsertDownload(row: DownloadRow)

    @Query("SELECT * FROM downloads WHERE accountId = :a AND bookId = :b")
    suspend fun download(a: String, b: String): DownloadRow?

    @Query("SELECT * FROM downloads ORDER BY requestedAt DESC")
    fun downloads(): Flow<List<DownloadRow>>

    @Query("SELECT * FROM downloads")
    suspend fun allDownloads(): List<DownloadRow>

    @Query(
        "UPDATE downloads SET state = :state, doneBytes = :done, totalBytes = :total, error = :error, finishedAt = :finishedAt WHERE accountId = :a AND bookId = :b"
    )
    suspend fun updateDownload(a: String, b: String, state: String, done: Long, total: Long, error: String?, finishedAt: Long?)

    @Query("DELETE FROM downloads WHERE accountId = :a AND bookId = :b")
    suspend fun deleteDownload(a: String, b: String)

    // ── an account signed out: forget everything of it ──
    @Transaction
    suspend fun forgetAccount(a: String) {
        clearBooks(a)
        clearDetails(a)
        clearProgress(a)
        clearBookmarks(a)
        clearCollections(a)
        clearOutbox(a)
        clearConflicts(a)
        clearSync(a)
        clearSynced(a)
    }

    /** A full snapshot replaces what the mirror holds for the account (downloads stay). */
    @Transaction
    suspend fun clearForSnapshot(a: String) {
        clearBooks(a)
        clearProgress(a)
        clearBookmarks(a)
        clearCollections(a)
    }

    @Query("DELETE FROM books WHERE accountId = :a")
    suspend fun clearBooks(a: String)

    @Query("DELETE FROM details WHERE accountId = :a")
    suspend fun clearDetails(a: String)

    @Query("DELETE FROM progress WHERE accountId = :a")
    suspend fun clearProgress(a: String)

    @Query("DELETE FROM bookmarks WHERE accountId = :a")
    suspend fun clearBookmarks(a: String)

    @Query("DELETE FROM collections WHERE accountId = :a")
    suspend fun clearCollections(a: String)

    @Query("DELETE FROM outbox WHERE accountId = :a")
    suspend fun clearOutbox(a: String)

    @Query("DELETE FROM conflicts WHERE accountId = :a")
    suspend fun clearConflicts(a: String)

    @Query("DELETE FROM sync_state WHERE accountId = :a")
    suspend fun clearSync(a: String)

    @Query("DELETE FROM synced WHERE accountId = :a")
    suspend fun clearSynced(a: String)
}

@Database(
    entities = [
        BookRow::class, DetailRow::class, ProgressRow::class, BookmarkRow::class, CollectionRow::class,
        OutboxRow::class, ConflictRow::class, SyncedRow::class, SyncStateRow::class, DownloadRow::class,
        LocalBookRow::class
    ],
    version = 2,
    exportSchema = true,
    autoMigrations = [AutoMigration(from = 1, to = 2)]
)
abstract class MirrorDb : RoomDatabase() {
    abstract fun dao(): MirrorDao

    companion object {
        fun open(context: Context): MirrorDb = Room.databaseBuilder(context, MirrorDb::class.java, "mirror.db").build()
    }
}
