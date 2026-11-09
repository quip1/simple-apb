// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.content.Context
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.media3.common.MediaMetadata
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import androidx.work.testing.WorkManagerTestInitHelper
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.SecretBox
import org.simpleapb.core.data.account.Tokens
import org.simpleapb.core.data.db.MirrorDb
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.data.sync.LocalLibrary

@RunWith(RobolectricTestRunner::class)
class BrowseTreeTest {
    @get:Rule val tmp = TemporaryFolder()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private lateinit var server: MockWebServer
    private lateinit var db: MirrorDb
    private lateinit var tree: BrowseTree

    private fun book(id: String, title: String, progress: String) =
        """{"id":"$id","title":"$title","authors":[{"id":"0190c3b4-0000-7000-8000-0000000000a1","name":"Ann Writer"}],"narrators":[],
        "duration_ms":100000,"cover_url":"/api/v1/books/$id/cover?v=1","has_cover":true,"source":"distribution","updated_at":"2026-11-01T10:00:00Z"$progress}"""

    @Before
    fun setUp() = runBlocking {
        WorkManagerTestInitHelper.initializeTestWorkManager(context)
        server = MockWebServer()
        server.start()
        val ds = PreferenceDataStoreFactory.create(scope = CoroutineScope(Dispatchers.IO + SupervisorJob())) { tmp.newFile("a.preferences_pb") }
        val store =
            AccountStore(
                ds,
                object : SecretBox {
                    override fun seal(plain: ByteArray) = plain

                    override fun open(sealed: ByteArray) = sealed
                }
            )
        store.add(
            Account("acc1", server.url("/").toString().trimEnd('/'), "S", "u1", "sam", "Sam", "user"),
            Tokens("a", Long.MAX_VALUE, "r", Long.MAX_VALUE)
        )
        db = Room.inMemoryDatabaseBuilder(context, MirrorDb::class.java).allowMainThreadQueries().build()
        val apis = ApiFactory(OkHttpClient(), store, AppInfo("0.5.0", "Test phone"))
        tree = BrowseTree(context, store, apis, LocalLibrary(db.dao()), Downloads(context, db.dao()))
    }

    @After
    fun tearDown() {
        server.close()
        db.close()
    }

    @Test
    fun theRootHasTheSixSections() = runBlocking {
        val root = tree.children(BrowseTree.ROOT)
        assertEquals(
            listOf(BrowseTree.CONTINUE, BrowseTree.RECENT, BrowseTree.DOWNLOADED, BrowseTree.COLLECTIONS, BrowseTree.SERIES, BrowseTree.AUTHORS),
            root.map { it.mediaId }
        )
        assertTrue(root.all { it.mediaMetadata.isBrowsable == true && it.mediaMetadata.isPlayable == false })
    }

    @Test
    fun continueListeningListsPlayableBooksWithCoversAndProgress() = runBlocking {
        val id = "0190c3b4-0000-7000-8000-0000000000b1"
        val p =
            ""","progress":{"book_id":"$id","position_ms":25000,"device_id":"0190c3b4-0000-7000-8000-0000000000d1",""" +
                """"client_ts":"2026-11-01T10:00:00Z","updated_at":"2026-11-01T10:00:00Z","speed":1.0}"""
        server.enqueue(
            MockResponse.Builder().code(200).addHeader("Content-Type", "application/json").body("""{"items":[${book(id, "Dune", p)}]}""").build()
        )

        val items = tree.children(BrowseTree.CONTINUE)

        val req = server.takeRequest()
        assertEquals("in_progress", req.url.queryParameter("shelf"))
        val b = items.single()
        assertEquals("book:$id", b.mediaId)
        assertEquals("Dune", b.mediaMetadata.title.toString())
        assertEquals("Ann Writer", b.mediaMetadata.artist.toString())
        assertEquals(MediaMetadata.MEDIA_TYPE_AUDIO_BOOK, b.mediaMetadata.mediaType)
        assertTrue(b.mediaMetadata.isPlayable == true)
        assertFalse(b.mediaMetadata.isBrowsable == true)
        assertEquals("content", b.mediaMetadata.artworkUri?.scheme)
        assertEquals("acc1" to id, CoverProvider.parse(b.mediaMetadata.artworkUri!!))
        val extras = b.mediaMetadata.extras!!
        assertEquals(BrowseTree.STATUS_PARTIALLY_PLAYED, extras.getInt(BrowseTree.PLAYBACK_STATUS))
        assertEquals(0.25, extras.getDouble(BrowseTree.COMPLETION), 0.001)
    }

    @Test
    fun searchUsesTheLibrarySearchAndFallsBackOffline() = runBlocking {
        val id = "0190c3b4-0000-7000-8000-0000000000b2"
        server.enqueue(
            MockResponse.Builder().code(
                200
            ).addHeader("Content-Type", "application/json").body("""{"items":[${book(id, "The Hobbit", "")}]}""").build()
        )
        assertEquals(listOf("book:$id"), tree.search("hobbit").map { it.mediaId })
        assertEquals("hobbit", server.takeRequest().url.queryParameter("q"))

        server.close() // offline: nothing in the mirror yet
        assertTrue(tree.search("hobbit").isEmpty())
        assertNull(tree.search("  ").firstOrNull())
    }
}
