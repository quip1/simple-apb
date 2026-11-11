// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data

import android.content.Context
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import androidx.work.testing.WorkManagerTestInitHelper
import java.io.File
import java.util.UUID
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.simpleapb.api.model.BookmarkCreate
import org.simpleapb.api.model.ProgressUpdate
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.SecretBox
import org.simpleapb.core.data.account.Tokens
import org.simpleapb.core.data.db.DownloadRow
import org.simpleapb.core.data.db.MirrorDb
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.data.download.DownloadWorker
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.data.sync.LiveHints
import org.simpleapb.core.data.sync.LocalLibrary
import org.simpleapb.core.data.sync.SyncEngine

private class PlainBox : SecretBox {
    override fun seal(plain: ByteArray) = plain

    override fun open(sealed: ByteArray) = sealed
}

@RunWith(RobolectricTestRunner::class)
class SyncEngineTest {
    @get:Rule val tmp = TemporaryFolder()

    private lateinit var server: MockWebServer
    private lateinit var db: MirrorDb
    private lateinit var local: LocalLibrary
    private lateinit var downloads: Downloads
    private lateinit var engine: SyncEngine
    private lateinit var account: Account
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    private val book1 = "0190c3b4-0000-7000-8000-0000000000b1"
    private val book2 = "0190c3b4-0000-7000-8000-0000000000b2"

    private fun summary(id: String, title: String) =
        """{"id":"$id","title":"$title","authors":[],"narrators":[],"duration_ms":600000,"cover_url":"/api/v1/books/$id/cover",
        "has_cover":false,"source":"distribution","updated_at":"2026-11-01T10:00:00Z"}"""

    private fun progress(id: String, pos: Long) =
        """{"book_id":"$id","position_ms":$pos,"device_id":"0190c3b4-0000-7000-8000-0000000000d1","client_ts":"2026-11-01T10:00:00Z",
        "updated_at":"2026-11-01T10:00:01Z","speed":1.0}"""

    private fun json(body: String, code: Int = 200) = MockResponse
        .Builder()
        .code(code)
        .addHeader("Content-Type", "application/json")
        .body(body)
        .build()

    @Before
    fun setUp() = runBlocking {
        WorkManagerTestInitHelper.initializeTestWorkManager(context)
        server = MockWebServer()
        server.start()
        val ds = PreferenceDataStoreFactory.create(scope = scope) { tmp.newFile("a.preferences_pb") }
        val store = AccountStore(ds, PlainBox())
        account = Account("acc1", server.url("/").toString().trimEnd('/'), "S", "u1", "sam", "Sam", "user")
        store.add(account, Tokens("a", Long.MAX_VALUE, "r", Long.MAX_VALUE))
        db = Room.inMemoryDatabaseBuilder(context, MirrorDb::class.java).allowMainThreadQueries().build()
        local = LocalLibrary(db.dao())
        downloads = Downloads(context, db.dao())
        engine = SyncEngine(context, store, ApiFactory(OkHttpClient(), store, AppInfo("0.4.0", "Test phone")), local, downloads, scope)
    }

    @After
    fun tearDown() {
        server.close()
        db.close()
    }

    private fun update(pos: Long, ts: String) = ProgressUpdate(
        positionMs = pos,
        deviceId = UUID.fromString("0190c3b4-0000-7000-8000-0000000000d2"),
        clientTs = ts,
        speed = 1f
    )

    @Test
    fun flushesTheOutboxInOrderThenPullsChanges() = runBlocking {
        // Two offline reports for one book collapse into the newest; a bookmark follows.
        local.queueProgress(account.id, book1, update(1_000, "2026-11-01T09:00:00Z"))
        local.queueProgress(account.id, book1, update(2_000, "2026-11-01T09:00:15Z"))
        val mark = UUID.randomUUID()
        local.queueBookmark(account.id, BookmarkCreate(UUID.fromString(book1), 2_000, mark, "here", "2026-11-01T09:00:16Z"))
        assertEquals(2_000L, local.progress(account.id, book1)?.positionMs) // shown locally at once

        server.enqueue(json("""{"applied":true,"progress":${progress(book1, 2_000)}}"""))
        server.enqueue(json("""{"id":"$mark","book_id":"$book1","position_ms":2000,"note":"here","updated_at":"2026-11-01T09:00:16Z"}""", 201))
        server.enqueue(
            json(
                """{"cursor":"c1","has_more":true,"full":true,"library":[${summary(
                    book1,
                    "One"
                )}],"progress":[],"bookmarks":[],"collections":[],"tombstones":[]}"""
            )
        )
        server.enqueue(
            json(
                """{"cursor":"c2","has_more":false,"library":[${summary(book2, "Two")}],"progress":[${progress(book2, 5_000)}],
                "bookmarks":[],"collections":[],"tombstones":[]}"""
            )
        )
        val changed = async { withTimeout(5_000) { engine.changes.first() } }
        assertTrue(engine.sync(account))

        val put = server.takeRequest()
        assertEquals("/api/v1/me/progress/$book1", put.url.encodedPath)
        assertTrue(put.body!!.utf8().contains("\"position_ms\":2000"))
        assertEquals("/api/v1/me/bookmarks", server.takeRequest().url.encodedPath)
        assertNull(server.takeRequest().url.queryParameter("since"))
        assertEquals("c1", server.takeRequest().url.queryParameter("since"))

        assertEquals(account.id, changed.await())
        assertEquals(listOf("One", "Two"), local.books(account.id).map { it.title })
        assertEquals(5_000L, local.progress(account.id, book2)?.positionMs)
        assertEquals("c2", db.dao().syncState(account.id)?.cursor)
        assertTrue(db.dao().outbox(account.id).isEmpty())
        assertNotNull(local.syncedAt(account.id, book1))
    }

    @Test
    fun aConflictWhileFlushingIsKeptForLater() = runBlocking {
        local.queueProgress(account.id, book1, update(9_000_000, "2026-11-01T09:00:00Z"))
        server.enqueue(
            json(
                """{"code":"progress_conflict","conflict_id":"cf-1","server":${progress(book1, 60_000)},"incoming":${progress(book1, 9_000_000)}}""",
                409
            )
        )
        server.enqueue(json("""{"cursor":"c1","has_more":false,"library":[],"progress":[],"bookmarks":[],"collections":[],"tombstones":[]}"""))
        val found = async { withTimeout(5_000) { engine.conflicts.first() } }
        assertTrue(engine.sync(account))
        assertEquals("cf-1", found.await().conflictId)
        val c = local.conflict(account.id, book1)!!
        assertEquals(60_000L, c.serverPositionMs)
        assertTrue(db.dao().outbox(account.id).isEmpty())
    }

    @Test
    fun aBookThatDisappearsTakesItsDownloadAlong() = runBlocking {
        local.saveBooks(account.id, listOf())
        val dir = File(tmp.root, "dl/$book1").apply { mkdirs() }
        File(dir, "f.m4b").writeText("audio")
        db.dao().upsertDownload(DownloadRow(account.id, book1, "One", DownloadState.DONE, false, dir.path, 5, 5, null, 1, 2))
        server.enqueue(
            json(
                """{"cursor":"c9","has_more":false,"library":[],"progress":[],"bookmarks":[],"collections":[],
                "tombstones":[{"entity":"library_entry","id":"$book1","deleted_at":"2026-11-01T11:00:00Z"}]}"""
            )
        )
        assertTrue(engine.sync(account))
        assertNull(db.dao().download(account.id, book1))
        assertFalse(dir.exists())
    }

    @Test
    fun anExpiredCursorStartsOverWithASnapshot() = runBlocking {
        db.dao().upsertSyncState(org.simpleapb.core.data.db.SyncStateRow(account.id, "old", 0))
        local.saveBooks(account.id, listOf())
        server.enqueue(json("""{"code":"cursor_expired","message":"gone"}""", 410))
        server.enqueue(
            json(
                """{"cursor":"new","has_more":false,"full":true,"library":[${summary(
                    book2,
                    "Two"
                )}],"progress":[],"bookmarks":[],"collections":[],"tombstones":[]}"""
            )
        )
        assertTrue(engine.sync(account))
        assertEquals("old", server.takeRequest().url.queryParameter("since"))
        assertNull(server.takeRequest().url.queryParameter("since"))
        assertEquals("new", db.dao().syncState(account.id)?.cursor)
    }

    @Test
    fun offlineLeavesTheOutboxAlone() = runBlocking {
        local.queueProgress(account.id, book1, update(1_000, "2026-11-01T09:00:00Z"))
        server.close() // nothing listening any more
        assertFalse(engine.sync(account))
        assertEquals(1, db.dao().outbox(account.id).size)
    }

    @Test
    fun aDeleteOfAnUnsentBookmarkCancelsIt() = runBlocking {
        val mark = UUID.randomUUID()
        local.queueBookmark(account.id, BookmarkCreate(UUID.fromString(book1), 2_000, mark, null, "2026-11-01T09:00:16Z"))
        local.queueBookmarkDelete(account.id, mark.toString())
        assertTrue(db.dao().outbox(account.id).isEmpty())
        assertTrue(local.bookmarks(account.id, book1).isEmpty())
    }

    @Test
    fun downloadHelpers() {
        assertEquals("m4b", DownloadWorker.extension("audio/mp4"))
        assertEquals("mp3", DownloadWorker.extension("audio/mpeg; charset=x"))
        assertEquals("bin", DownloadWorker.extension("application/octet-stream"))
        val f = tmp.newFile("x.bin").apply { writeText("abc") }
        assertEquals("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", DownloadWorker.sha256(f))
        assertEquals("https://b.example/sub/ws", LiveHints.wsUrl("https://b.example/sub/"))
    }

    @Test
    fun newBooksAreAnnouncedButNotOnASnapshot() = runBlocking {
        // First sync is a snapshot: nothing is "new".
        server.enqueue(
            json(
                """{"cursor":"c1","has_more":false,"full":true,"library":[${summary(
                    book1,
                    "One"
                )}],"progress":[],"bookmarks":[],"collections":[],"tombstones":[]}"""
            )
        )
        val first = async { withTimeoutOrNull(1_500) { engine.newBooks.first() } }
        assertTrue(engine.sync(account))
        assertNull(first.await())
        // Later a book appears: it is announced, the known one isn't.
        server.enqueue(
            json(
                """{"cursor":"c2","has_more":false,"library":[${summary(
                    book1,
                    "One"
                )},${summary(book2, "Two")}],"progress":[],"bookmarks":[],"collections":[],"tombstones":[]}"""
            )
        )
        val next = async { withTimeout(5_000) { engine.newBooks.first() } }
        assertTrue(engine.sync(account))
        assertEquals(listOf("Two"), next.await().second.map { it.title })
    }

    @Test
    fun localFolderHelpers() {
        val names = listOf("Part 10.mp3", "part 2.mp3", "Part 1.mp3", "Intro.mp3")
        assertEquals(
            listOf("Intro.mp3", "Part 1.mp3", "part 2.mp3", "Part 10.mp3"),
            names.sortedWith(org.simpleapb.core.data.local.LocalFolders.NaturalOrder)
        )
        assertTrue(org.simpleapb.core.data.local.LocalFolders.isAudio("Book.M4B", ""))
        assertTrue(org.simpleapb.core.data.local.LocalFolders.isAudio("x", "audio/mpeg"))
        assertFalse(org.simpleapb.core.data.local.LocalFolders.isAudio("cover.jpg", "image/jpeg"))
        val a = org.simpleapb.core.data.local.LocalFolders.idFor("content://tree/primary%3AAudiobooks/document/primary%3AAudiobooks%2FHome")
        assertEquals(a, org.simpleapb.core.data.local.LocalFolders.idFor("content://tree/primary%3AAudiobooks/document/primary%3AAudiobooks%2FHome"))
        assertTrue(a.startsWith("local:") && a.length == "local:".length + 24)
    }
}
