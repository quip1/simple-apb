// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import java.io.File
import java.nio.file.Files
import java.util.UUID
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeFalse
import org.junit.Test
import org.simpleapb.api.model.Progress

class PlayerLogicTest {
    // Two files (10 min + 5 min), three chapters, one spanning the file boundary.
    private val tl =
        BookTimeline(
            listOf(
                TimelineFile("b", 600_000, 300_000, "audio/mpeg"),
                TimelineFile("a", 0, 600_000, "audio/mpeg")
            ),
            listOf(
                TimelineChapter(0, "One", 0, 240_000),
                TimelineChapter(1, "Two", 240_000, 720_000),
                TimelineChapter(2, "Three", 720_000, 900_000)
            )
        )

    @Test
    fun timelineMapsBetweenBookAndFiles() {
        assertEquals(900_000, tl.durationMs)
        assertEquals("a", tl.files[0].id) // sorted by offset
        assertEquals(FilePosition(0, 0), tl.locate(-5))
        assertEquals(FilePosition(0, 599_999), tl.locate(599_999))
        assertEquals(FilePosition(1, 0), tl.locate(600_000))
        assertEquals(FilePosition(1, 300_000), tl.locate(2_000_000)) // clamped to the end
        assertEquals(650_000, tl.global(1, 50_000))
    }

    @Test
    fun chapterNavigation() {
        assertEquals("Two", tl.chapterAt(600_500)?.title)
        // Previous: start of this chapter, or the one before within 3 s of its start.
        assertEquals(240_000, tl.previousChapterStart(300_000))
        assertEquals(0, tl.previousChapterStart(241_000))
        assertEquals(0, tl.previousChapterStart(1_000))
        assertEquals(720_000L, tl.nextChapterStart(300_000))
        assertNull(tl.nextChapterStart(800_000))
        assertEquals(720_000, tl.chapterEnd(300_000))
        // No chapters: the end of the book.
        assertEquals(10_000, BookTimeline(listOf(TimelineFile("x", 0, 10_000, "audio/mp4")), emptyList()).chapterEnd(5))
    }

    @Test
    fun speedStepsAndRange() {
        assertEquals(1.25f, Speed.clamp(1.26f))
        assertEquals(0.5f, Speed.clamp(0.1f))
        assertEquals(3.5f, Speed.clamp(9f))
        assertEquals(1.05f, Speed.clamp(1.049f))
        assertEquals(1f, Speed.clamp(Float.NaN))
        assertEquals(1.25f, Speed.next(1f))
        assertEquals(1f, Speed.next(2f))
        assertEquals(2f, Speed.next(1.8f))
    }

    @Test
    fun autoRewindScalesWithPause() {
        assertEquals(3_000, autoRewindMs(3, 60_000))
        assertEquals(1_500, autoRewindMs(3, 20_000))
        assertEquals(0, autoRewindMs(0, 600_000))
        assertEquals(0, autoRewindMs(3, 0))
    }

    @Test
    fun settingsAreClampedAndButtonsNormalized() {
        val s = PlaybackSettings(skipBackS = 1, skipForwardS = 500, autoRewindS = 99, defaultSpeed = 7f).sanitized()
        assertEquals(5, s.skipBackS)
        assertEquals(120, s.skipForwardS)
        assertEquals(30, s.autoRewindS)
        assertEquals(3.5f, s.defaultSpeed)
        // Play/pause is always there; at most five; no duplicates.
        assertEquals(
            listOf(ControlButton.BACK, ControlButton.PLAY_PAUSE),
            PlaybackSettings.normalizeButtons(listOf(ControlButton.BACK, ControlButton.BACK))
        )
        val six = ControlButton.entries.toList()
        val five = PlaybackSettings.normalizeButtons(six)
        assertEquals(5, five.size)
        assertTrue(ControlButton.PLAY_PAUSE in five)
    }

    @Test
    fun sleepTimerFadesAndExtends() {
        val t = SleepTimer.Until(100_000)
        assertEquals(1f, SleepTimer.volume(t, 50_000, 0, 1f))
        assertEquals(0.5f, SleepTimer.volume(t, 95_000, 0, 1f))
        assertEquals(0L, SleepTimer.remainingMs(t, 200_000, 0, 1f))
        // End of chapter counts playback time, scaled by speed.
        val eoc = SleepTimer.EndOfChapter(720_000)
        assertEquals(10_000, SleepTimer.remainingMs(eoc, 0, 700_000, 2f))
        // Extending adds on top of what's left.
        assertEquals(SleepTimer.Until(1_000 + 10_000 + 300_000), SleepTimer.extend(eoc, 1_000, 700_000, 2f, 300_000))
    }

    @Test
    fun shakeNeedsTwoJolts() {
        val d = ShakeDetector()
        val g = 9.81f
        assertFalse(d.onSample(0f, 0f, g, 0)) // resting
        assertFalse(d.onSample(3 * g, 0f, 0f, 1_000)) // first jolt
        assertTrue(d.onSample(3 * g, 0f, 0f, 1_300)) // second jolt: shake
        assertFalse(d.onSample(3 * g, 0f, 0f, 1_500)) // cooldown
        assertFalse(d.onSample(3 * g, 0f, 0f, 5_000)) // lone jolt later
        assertFalse(d.onSample(3 * g, 0f, 0f, 7_000)) // too far apart
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun headsetPressesAreCounted() = runTest {
        val got = mutableListOf<Gesture>()
        val p = HeadsetPresses(backgroundScope) { got += it }
        fun press() {
            p.down(0)
            p.up()
        }
        press()
        advanceTimeBy(500)
        press()
        advanceTimeBy(100)
        press()
        advanceTimeBy(500)
        repeat(3) {
            press()
            advanceTimeBy(100)
        }
        advanceTimeBy(500)
        p.down(0)
        p.down(1) // key repeat while held
        advanceTimeBy(900)
        p.up()
        runCurrent()
        advanceTimeBy(1_000)
        // A lone key-up (the system kept the long press for the assistant) does nothing.
        p.up()
        advanceTimeBy(1_000)
        // The system flags a long press on a repeated key-down before our own timer.
        p.down(0)
        advanceTimeBy(400)
        p.down(1, systemLongPress = true)
        p.up()
        advanceTimeBy(1_000)
        assertEquals(listOf(Gesture.SINGLE, Gesture.DOUBLE, Gesture.TRIPLE, Gesture.LONG, Gesture.LONG), got)
    }

    private fun progress(pos: Long, ts: String) = Progress(UUID.randomUUID(), pos, UUID.randomUUID(), ts, ts, 1f)

    @Test
    fun startPositionPicksTheNewerRecord() {
        val dur = 900_000L
        assertEquals(0, PlaybackRepository.chooseStart(null, null, dur))
        assertEquals(5_000, PlaybackRepository.chooseStart(progress(5_000, "2026-11-01T10:00:00Z"), null, dur))
        assertEquals(7_000, PlaybackRepository.chooseStart(null, LocalPosition(7_000, 0), dur))
        val serverAt = java.time.Instant.parse("2026-11-01T10:00:00Z").toEpochMilli()
        assertEquals(9_000, PlaybackRepository.chooseStart(progress(5_000, "2026-11-01T10:00:00Z"), LocalPosition(9_000, serverAt + 1), dur))
        assertEquals(5_000, PlaybackRepository.chooseStart(progress(5_000, "2026-11-01T10:00:00Z"), LocalPosition(9_000, serverAt - 1), dur))
        // Finished: start over.
        assertEquals(0, PlaybackRepository.chooseStart(progress(899_500, "2026-11-01T10:00:00Z"), null, dur))
    }

    @Test
    fun storeRoundTrips() = runTest {
        // DataStore replaces its file with a rename the Windows JVM refuses; CI runs on Linux.
        assumeFalse(System.getProperty("os.name").orEmpty().startsWith("Windows"))
        val dir: File = Files.createTempDirectory("pstore").toFile()
        val store = PlaybackStore(PreferenceDataStoreFactory.create(scope = backgroundScope) { File(dir, "p.preferences_pb") })
        assertEquals(PlaybackSettings(), store.settings())
        store.update { it.copy(skipBackS = 15, buttons = listOf(ControlButton.SPEED, ControlButton.BACK)) }
        val s = store.settings()
        assertEquals(15, s.skipBackS)
        assertEquals(listOf(ControlButton.SPEED, ControlButton.BACK, ControlButton.PLAY_PAUSE), s.buttons)
        store.setSpeed("b1", 1.33f)
        assertEquals(1.35f, store.speed("b1"))
        store.saveLocalPosition("b1", 1234, 99)
        assertEquals(LocalPosition(1234, 99), store.localPosition("b1"))
        store.setLastBook("acc", "b1")
        assertEquals(LastBook("acc", "b1"), store.lastBook())
        dir.deleteRecursively()
    }
}
