// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data

import android.app.Notification
import android.content.Context
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import androidx.work.ForegroundInfo
import androidx.work.ListenableWorker
import androidx.work.WorkerFactory
import androidx.work.WorkerParameters
import androidx.work.testing.TestListenableWorkerBuilder
import androidx.work.testing.WorkManagerTestInitHelper
import androidx.work.workDataOf
import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.shadows.ShadowStatFs
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.SecretBox
import org.simpleapb.core.data.account.Tokens
import org.simpleapb.core.data.db.DownloadRow
import org.simpleapb.core.data.db.MirrorDb
import org.simpleapb.core.data.download.DownloadError
import org.simpleapb.core.data.download.DownloadNotifications
import org.simpleapb.core.data.download.DownloadSettings
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.data.download.DownloadWorker
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.data.sync.LocalLibrary

@RunWith(RobolectricTestRunner::class)
class DownloadWorkerTest {
    @get:Rule val tmp = TemporaryFolder()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private lateinit var server: MockWebServer
    private lateinit var db: MirrorDb
    private lateinit var store: AccountStore
    private lateinit var apis: ApiFactory
    private lateinit var downloads: Downloads
    private lateinit var account: Account

    private val book = "0190c3b4-0000-7000-8000-0000000000b1"
    private val file = "0190c3b4-0000-7000-8000-0000000000f1"
    private val audio = ByteArray(10_000) { (it % 251).toByte() }

    private fun sha(b: ByteArray) = MessageDigest.getInstance("SHA-256").digest(b).joinToString("") { "%02x".format(it) }

    private fun detail(sha: String) =
        """{"id":"$book","title":"One","authors":[],"narrators":[],"duration_ms":600000,"cover_url":"/c","has_cover":false,
        "source":"distribution","updated_at":"2026-11-01T10:00:00Z","size_bytes":${audio.size},"has_epub":false,"tags":[],"chapters":[],
        "files":[{"id":"$file","index":0,"offset_ms":0,"duration_ms":600000,"size_bytes":${audio.size},"codec":"aac","mime_type":"audio/mp4","sha256":"$sha"}]}"""

    @Before
    fun setUp() = runBlocking {
        WorkManagerTestInitHelper.initializeTestWorkManager(context)
        server = MockWebServer()
        server.start()
        val ds = PreferenceDataStoreFactory.create(scope = CoroutineScope(Dispatchers.IO + SupervisorJob())) { tmp.newFile("a.preferences_pb") }
        store =
            AccountStore(
                ds,
                object : SecretBox {
                    override fun seal(plain: ByteArray) = plain

                    override fun open(sealed: ByteArray) = sealed
                }
            )
        account = Account("acc1", server.url("/").toString().trimEnd('/'), "S", "u1", "sam", "Sam", "user")
        store.add(account, Tokens("a", Long.MAX_VALUE, "r", Long.MAX_VALUE))
        apis = ApiFactory(OkHttpClient(), store, AppInfo("0.4.0", "Test phone"))
        db = Room.inMemoryDatabaseBuilder(context, MirrorDb::class.java).allowMainThreadQueries().build()
        downloads = Downloads(context, db.dao())
        downloads.update { DownloadSettings() } // the settings store outlives a single test
    }

    @After
    fun tearDown() {
        server.close()
        db.close()
    }

    private fun worker(): DownloadWorker {
        val notes =
            object : DownloadNotifications {
                override fun foreground(title: String, percent: Int?) = ForegroundInfo(1, Notification())
            }
        val factory =
            object : WorkerFactory() {
                override fun createWorker(appContext: Context, workerClassName: String, workerParameters: WorkerParameters): ListenableWorker =
                    DownloadWorker(appContext, workerParameters, store, apis, LocalLibrary(db.dao()), db.dao(), downloads, notes)
            }
        return TestListenableWorkerBuilder<DownloadWorker>(context)
            .setWorkerFactory(factory)
            .setInputData(workDataOf(DownloadWorker.ACCOUNT to account.id, DownloadWorker.BOOK to book))
            .build()
    }

    private suspend fun queue(dir: File) {
        // Robolectric reports no free space unless told otherwise.
        ShadowStatFs.registerStats(dir, 100_000, 100_000, 100_000)
        db.dao().upsertDownload(DownloadRow(account.id, book, "One", DownloadState.QUEUED, false, dir.path, 0, 0, null, 1, null))
    }

    @Test
    fun resumesAPartialFileWithARangeRequestAndChecksIt() = runBlocking {
        val dir = File(tmp.root, "dl").apply { mkdirs() }
        File(dir, "$file.part").writeBytes(audio.copyOfRange(0, 4_000)) // an earlier, interrupted attempt
        queue(dir)
        server.enqueue(MockResponse.Builder().code(200).addHeader("Content-Type", "application/json").body(detail(sha(audio))).build())
        server.enqueue(
            MockResponse
                .Builder()
                .code(206)
                .addHeader("Content-Range", "bytes 4000-9999/10000")
                .body(Buffer().write(audio.copyOfRange(4_000, audio.size)))
                .build()
        )

        val result = worker().doWork()

        assertEquals(ListenableWorker.Result.success(), result)
        server.takeRequest() // the book's details
        val get = server.takeRequest()
        assertEquals("bytes=4000-", get.headers["Range"])
        assertEquals("true", get.url.queryParameter("download"))
        val done = File(dir, "$file.m4b")
        assertTrue(done.readBytes().contentEquals(audio))
        assertFalse(File(dir, "$file.part").exists())
        val row = db.dao().download(account.id, book)!!
        assertEquals(DownloadState.DONE, row.state)
        assertEquals(audio.size.toLong(), row.doneBytes)
        assertEquals(book, LocalLibrary(db.dao()).detail(account.id, book)?.id.toString()) // kept for offline playback
    }

    @Test
    fun aDamagedFileIsThrownAwayAndRetried() = runBlocking {
        val dir = File(tmp.root, "dl2").apply { mkdirs() }
        queue(dir)
        server.enqueue(MockResponse.Builder().code(200).addHeader("Content-Type", "application/json").body(detail(sha(audio))).build())
        val damaged = audio.copyOf().also { it[5] = 99 }
        server.enqueue(MockResponse.Builder().code(200).body(Buffer().write(damaged)).build())

        assertEquals(ListenableWorker.Result.retry(), worker().doWork())
        assertFalse(File(dir, "$file.part").exists())
        assertFalse(File(dir, "$file.m4b").exists())
    }

    @Test
    fun theStorageLimitStopsADownload() = runBlocking {
        downloads.update { it.copy(capBytes = 5_000) }
        val dir = File(tmp.root, "dl3").apply { mkdirs() }
        queue(dir)
        server.enqueue(MockResponse.Builder().code(200).addHeader("Content-Type", "application/json").body(detail(sha(audio))).build())

        assertEquals(ListenableWorker.Result.failure(), worker().doWork())
        val row = db.dao().download(account.id, book)!!
        assertEquals(DownloadState.FAILED, row.state)
        assertEquals(DownloadError.CAP, row.error)
    }
}
