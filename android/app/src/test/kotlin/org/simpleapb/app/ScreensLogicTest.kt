// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app

import java.util.UUID
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.simpleapb.api.endpoints.LibraryApi.ShelfListLibrary
import org.simpleapb.api.endpoints.LibraryApi.SortListLibrary
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.LibrarySource
import org.simpleapb.api.model.PersonRef
import org.simpleapb.api.model.Progress
import org.simpleapb.app.book.BookForm
import org.simpleapb.app.data.SyncedPrefs
import org.simpleapb.app.library.OfflineFilter
import org.simpleapb.app.ui.coverUrl
import org.simpleapb.app.ui.percent
import org.simpleapb.core.player.PlaybackRepository
import org.simpleapb.core.player.PlaybackSettings
import org.simpleapb.core.player.Quality

class ScreensLogicTest {
    private fun book(
        title: String,
        author: String,
        pos: Long? = null,
        finished: Boolean = false,
        hidden: Boolean = false,
        added: String = "2026-01-01T00:00:00Z"
    ) = BookSummary(
        id = UUID.nameUUIDFromBytes(title.toByteArray()),
        title = title,
        authors = listOf(PersonRef(UUID.randomUUID(), author)),
        narrators = emptyList(),
        durationMs = 1_000_000,
        coverUrl = "",
        hasCover = false,
        source = LibrarySource.DISTRIBUTION,
        updatedAt = added,
        addedAt = added,
        hidden = hidden,
        progress =
        pos?.let {
            Progress(
                UUID.nameUUIDFromBytes(title.toByteArray()),
                it,
                UUID(0, 0),
                "2026-02-0${it % 9 + 1}T00:00:00Z",
                "2026-02-01T00:00:00Z",
                1f,
                finishedAt = if (finished) "2026-02-02T00:00:00Z" else null
            )
        }
    )

    @Test
    fun offlineLibraryFiltersLikeTheServer() {
        val all =
            listOf(
                book("Dune", "Frank Herbert", pos = 5_000),
                book("Emma", "Jane Austen", pos = 1_000_000, finished = true),
                book("Ivanhoe", "Walter Scott"),
                book("Old", "Someone", hidden = true)
            )
        assertEquals(listOf("Dune"), OfflineFilter.apply(all, ShelfListLibrary.IN_PROGRESS, SortListLibrary.TITLE, "", null).map { it.title })
        assertEquals(listOf("Emma"), OfflineFilter.apply(all, ShelfListLibrary.FINISHED, SortListLibrary.TITLE, "", null).map { it.title })
        assertEquals(listOf("Ivanhoe"), OfflineFilter.apply(all, ShelfListLibrary.NOT_STARTED, SortListLibrary.TITLE, "", null).map { it.title })
        assertEquals(listOf("Old"), OfflineFilter.apply(all, ShelfListLibrary.ARCHIVED, SortListLibrary.TITLE, "", null).map { it.title })
        assertEquals(listOf("Emma"), OfflineFilter.apply(all, ShelfListLibrary.ALL, SortListLibrary.TITLE, "austen", null).map { it.title })
        assertEquals(
            listOf("Dune", "Emma", "Ivanhoe"),
            OfflineFilter.apply(all, ShelfListLibrary.ALL, SortListLibrary.TITLE, "", null).map {
                it.title
            }
        )
        assertTrue(OfflineFilter.apply(all, ShelfListLibrary.ALL, SortListLibrary.TITLE, "", "some-genre").isEmpty())
    }

    @Test
    fun syncedPrefsRoundTrip() {
        val s = PlaybackSettings(skipBackS = 15, skipForwardS = 45, einkTickS = 20, leftHanded = true, defaultSpeed = 1.25f)
        val wire = SyncedPrefs.extract(s)
        assertEquals(JsonPrimitive(15), wire["skip_back_s"])
        assertEquals(JsonPrimitive(true), wire["left_handed"])
        val back = SyncedPrefs.apply(PlaybackSettings(), wire)
        assertEquals(s.skipBackS, back.skipBackS)
        assertEquals(s.skipForwardS, back.skipForwardS)
        assertEquals(s.einkTickS, back.einkTickS)
        assertEquals(s.leftHanded, back.leftHanded)
        assertEquals(s.defaultSpeed, back.defaultSpeed)
    }

    @Test
    fun syncedPrefsIgnoreBadValuesAndKeepTheRest() {
        val base = PlaybackSettings(skipBackS = 10, autoRewindS = 7)
        val r =
            SyncedPrefs.apply(
                base,
                mapOf(
                    "skip_back_s" to JsonPrimitive("ten"),
                    "skip_forward_s" to JsonPrimitive(500),
                    "eink_tick_s" to JsonPrimitive(1),
                    "email_alerts" to JsonPrimitive(true)
                )
            )
        assertEquals(10, r.skipBackS) // a string is not a number
        assertEquals(120, r.skipForwardS) // clamped
        assertEquals(10, r.einkTickS) // clamped
        assertEquals(7, r.autoRewindS) // device-only settings untouched
    }

    @Test
    fun qualityPicksTheHlsProfile() {
        assertNull(PlaybackRepository.hlsProfile(Quality.AUTO, metered = false))
        assertEquals("medium", PlaybackRepository.hlsProfile(Quality.AUTO, metered = true))
        assertNull(PlaybackRepository.hlsProfile(Quality.ORIGINAL, metered = true))
        assertEquals("low", PlaybackRepository.hlsProfile(Quality.LOW, metered = false))
    }

    @Test
    fun bookFormSendsOnlyChangedFields() {
        val from = BookForm(title = "Dune", authors = "Frank Herbert", year = "1965", description = "Spice.")
        val p = from.copy(title = "Dune ", authors = "Frank Herbert\n\n", year = "1966").patch(from)
        assertNull(p.title) // only whitespace changed
        assertNull(p.authors) // same people
        assertEquals(1966, p.publishedYear)
        assertNull(p.description)
        assertNull(p.series)
        val q = from.copy(series = "Dune", seriesIndex = "1").patch(from)
        assertEquals("Dune", q.series?.name)
        assertEquals("1", q.series?.index)
    }

    @Test
    fun coverAddressesAskForASize() {
        assertEquals("https://b.example/api/v1/books/x/cover?v=3&size=256", coverUrl("https://b.example", "/api/v1/books/x/cover?v=3", true))
        assertNull(coverUrl("https://b.example", "/api/v1/books/x/cover", false))
    }

    @Test
    fun percentRoundsDown() {
        assertEquals(0, percent(10, 0))
        assertEquals(99, percent(999, 1000))
        assertEquals(100, percent(2000, 1000))
    }

    @Test
    fun qrCodesDecodeFromCameraBrightness() {
        val url = "https://books.example.org/sub"
        val m = com.google.zxing.qrcode.QRCodeWriter().encode(url, com.google.zxing.BarcodeFormat.QR_CODE, 240, 240)
        // A camera row is often wider than the image (row stride).
        val stride = 256
        val data = ByteArray(stride * m.height) { 0x80.toByte() }
        for (y in 0 until m.height) for (x in 0 until m.width) data[y * stride + x] = if (m.get(x, y)) 0 else 0xFF.toByte()
        assertEquals(url, org.simpleapb.app.onboarding.decodeLuminance(org.simpleapb.app.onboarding.qrReader(), data, stride, m.width, m.height))
        assertNull(org.simpleapb.app.onboarding.decodeLuminance(org.simpleapb.app.onboarding.qrReader(), ByteArray(stride * 240), stride, 240, 240))
    }

    @Test
    fun offlineSeriesSortGoesByNumber() {
        val dcc = UUID.randomUUID()
        fun inSeries(title: String, index: String?) =
            book(title, "Matt Dinniman").copy(series = org.simpleapb.api.model.SeriesRef(dcc, "Dungeon Crawler Carl", index))
        val books =
            listOf(
                inSeries("Carl's Doomsday Scenario", "2"),
                inSeries("The Eye of the Bedlam Bride", "10"),
                book("Kaiju", "Matt Dinniman"),
                inSeries("Dungeon Crawler Carl", "1"),
                inSeries("A Side Story", "Novella"),
                inSeries("The Dungeon Anarchist's Cookbook", "Book 3")
            )
        val want =
            listOf(
                "Dungeon Crawler Carl",
                "Carl's Doomsday Scenario",
                "The Dungeon Anarchist's Cookbook",
                "The Eye of the Bedlam Bride",
                "A Side Story",
                "Kaiju"
            )
        for (sort in listOf(SortListLibrary.SERIES, SortListLibrary.AUTHOR)) {
            assertEquals(want, OfflineFilter.apply(books, ShelfListLibrary.ALL, sort, "", null).map { it.title })
        }
    }
}
