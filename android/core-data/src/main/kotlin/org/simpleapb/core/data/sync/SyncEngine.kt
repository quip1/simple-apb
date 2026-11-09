// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.sync

import android.content.Context
import androidx.hilt.work.HiltWorker
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.IOException
import java.time.Instant
import java.util.UUID
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.simpleapb.api.infrastructure.Serializer
import org.simpleapb.api.model.ProgressConflict
import org.simpleapb.api.model.SyncDelta
import org.simpleapb.api.model.Tombstone
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.db.ConflictRow
import org.simpleapb.core.data.db.OutboxRow
import org.simpleapb.core.data.db.SyncStateRow
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.Api
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppScope
import retrofit2.Response

/**
 * The sync engine (brief §6.8, §7.5): sends the outbox in order, then pulls
 * everything that changed since the stored cursor into the mirror. It runs
 * from WorkManager (on reconnect and every 15 minutes) and right away when
 * the app opens or a live hint arrives.
 */
@Singleton
class SyncEngine
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val accounts: AccountStore,
    private val apis: ApiFactory,
    private val local: LocalLibrary,
    private val downloads: Downloads,
    @AppScope private val scope: CoroutineScope
) {
    private val lock = Mutex()
    private val dao get() = local.dao

    private val _changes = MutableSharedFlow<String>(extraBufferCapacity = 8)

    /** An account's data changed (account id): open screens reload. */
    val changes: SharedFlow<String> = _changes

    private val _conflicts = MutableSharedFlow<ConflictRow>(extraBufferCapacity = 8)

    /** A queued position write met a far-away position from another device. */
    val conflicts: SharedFlow<ConflictRow> = _conflicts

    @Volatile private var lastOwnWrite = 0L

    /** This device just wrote something: the server's hint about it is an echo. */
    fun wrote() {
        lastOwnWrite = System.currentTimeMillis()
    }

    fun isEcho(now: Long = System.currentTimeMillis()) = now - lastOwnWrite < ECHO_MS

    /** Sync every signed-in account; false when some server was out of reach. */
    suspend fun syncAll(): Boolean {
        var ok = true
        for (a in accounts.current().accounts) ok = sync(a) && ok
        return ok
    }

    /** Sends the outbox, then pulls changes. False when the server was out of reach. */
    suspend fun sync(account: Account): Boolean = lock.withLock {
        val api = apis.forAccount(account)
        try {
            flush(account, api) && pull(account, api)
        } catch (_: IOException) {
            false
        }
    }

    /** Runs a sync now, in the app's process (app opened, live hint). */
    fun syncSoon(account: Account) {
        scope.launch { if (!sync(account)) requestSync() }
    }

    private suspend fun flush(account: Account, api: Api): Boolean {
        for (row in dao.outbox(account.id)) {
            when (send(account, api, row)) {
                Sent.DONE -> dao.deleteOutbox(row.seq)
                Sent.RETRY -> {
                    // A server error: try again later, but never block the queue forever.
                    if (row.attempts + 1 >= MAX_ATTEMPTS) dao.deleteOutbox(row.seq) else dao.bumpOutbox(row.seq)
                    return false
                }
            }
        }
        return true
    }

    private enum class Sent { DONE, RETRY }

    private fun <T> outcome(res: Response<T>): Sent = when {
        res.isSuccessful -> Sent.DONE
        res.code() >= 500 || res.code() == 429 -> Sent.RETRY
        else -> Sent.DONE // refused for good (gone, forbidden…): drop it
    }

    private suspend fun send(account: Account, api: Api, row: OutboxRow): Sent {
        wrote()
        return when (row.kind) {
            LocalLibrary.KIND_PROGRESS -> {
                val q = local.decodeProgress(row.payload) ?: return Sent.DONE
                val id = runCatching { UUID.fromString(q.bookId) }.getOrNull() ?: return Sent.DONE
                // The last sync time is the one stored now: other devices' writes since then are what conflict.
                val res = api.progress.putProgress(id, q.update)
                if (res.code() == 409) {
                    val raw = runCatching { res.errorBody()?.string() }.getOrNull().orEmpty()
                    val c = runCatching { Serializer.kotlinxSerializationJson.decodeFromString(ProgressConflict.serializer(), raw) }.getOrNull()
                    if (c != null) {
                        val row2 = ConflictRow(account.id, q.bookId, c.conflictId, c.server.positionMs, c.server.deviceName)
                        dao.upsertConflict(row2)
                        _conflicts.tryEmit(row2)
                    }
                    return Sent.DONE
                }
                res.body()?.let { local.saveProgress(account.id, listOf(it.progress)) }
                if (res.isSuccessful) local.markSynced(account.id, q.bookId)
                outcome(res)
            }
            LocalLibrary.KIND_BOOKMARK_CREATE -> {
                val b = local.decodeBookmark(row.payload) ?: return Sent.DONE
                outcome(api.progress.createBookmark(b))
            }
            LocalLibrary.KIND_BOOKMARK_DELETE -> {
                val id = runCatching { UUID.fromString(row.payload) }.getOrNull() ?: return Sent.DONE
                outcome(api.progress.deleteBookmark(id))
            }
            else -> Sent.DONE
        }
    }

    private suspend fun pull(account: Account, api: Api): Boolean {
        var cursor = dao.syncState(account.id)?.cursor
        var changed = false
        var pages = 0
        while (pages++ < MAX_PAGES) {
            val res = api.progress.getSyncDelta(since = cursor, limit = PAGE)
            if (res.code() == 410) {
                // The change log was pruned past our cursor: start over with a snapshot.
                cursor = null
                continue
            }
            val d = res.body() ?: return res.code() < 500
            if (d.full == true && pages == 1) dao.clearForSnapshot(account.id)
            if (apply(account, d)) changed = true
            cursor = d.cursor
            dao.upsertSyncState(SyncStateRow(account.id, cursor, System.currentTimeMillis()))
            if (!d.hasMore) break
        }
        if (changed) _changes.tryEmit(account.id)
        return true
    }

    private suspend fun apply(account: Account, d: SyncDelta): Boolean {
        val a = account.id
        local.saveBooks(a, d.library)
        local.saveProgress(a, d.progress)
        local.saveBookmarks(a, d.bookmarks)
        local.saveCollections(a, d.collections)
        for (t in d.tombstones) {
            when (t.entity) {
                Tombstone.Entity.LIBRARY_ENTRY -> {
                    // No longer visible (removed, taken down…): its download goes too.
                    dao.deleteBook(a, t.id)
                    dao.deleteDetail(a, t.id)
                    downloads.remove(a, t.id)
                }
                Tombstone.Entity.PROGRESS -> dao.deleteProgress(a, t.id)
                Tombstone.Entity.BOOKMARK -> dao.deleteBookmark(a, t.id)
                Tombstone.Entity.COLLECTION -> dao.deleteCollection(a, t.id)
            }
        }
        return d.library.isNotEmpty() ||
            d.progress.isNotEmpty() ||
            d.bookmarks.isNotEmpty() ||
            d.collections.isNotEmpty() ||
            d.tombstones.isNotEmpty()
    }

    // ── scheduling ──

    private val online = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

    /** Every 15 minutes while connected, and once now (brief §7.5). */
    fun schedule() {
        val wm = WorkManager.getInstance(context)
        wm.enqueueUniquePeriodicWork(
            PERIODIC,
            ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<SyncWorker>(15, TimeUnit.MINUTES).setConstraints(online).build()
        )
        requestSync()
    }

    /** A sync as soon as a network is available (after an offline write, or a failed attempt). */
    fun requestSync() {
        WorkManager.getInstance(context).enqueueUniqueWork(
            ONCE,
            ExistingWorkPolicy.APPEND_OR_REPLACE,
            OneTimeWorkRequestBuilder<SyncWorker>()
                .setConstraints(online)
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
                .build()
        )
    }

    /** Signed out: forget the account's mirror, outbox and downloads. */
    suspend fun forget(accountId: String) {
        downloads.removeAll(accountId)
        dao.forgetAccount(accountId)
    }

    fun now(): String = Instant.now().toString()

    private companion object {
        const val PAGE = 200
        const val MAX_PAGES = 1_000
        const val MAX_ATTEMPTS = 8
        const val ECHO_MS = 1_500L
        const val PERIODIC = "sync-periodic"
        const val ONCE = "sync-now"
    }
}

@HiltWorker
class SyncWorker
@AssistedInject
constructor(@Assisted context: Context, @Assisted params: WorkerParameters, private val engine: SyncEngine) :
    CoroutineWorker(context, params) {
    override suspend fun doWork(): Result = if (engine.syncAll()) Result.success() else Result.retry()
}
