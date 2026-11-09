// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.net.Uri
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.roundToInt
import kotlin.math.sin
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class EffectsAndAutoTest {
    private val rate = 22_050

    /** Interleaved stereo sine, [amp] of full scale. */
    private fun sine(freq: Double, amp: Double, frames: Int = rate, right: Double = amp): ShortArray {
        val out = ShortArray(frames * 2)
        for (i in 0 until frames) {
            val v = sin(2 * PI * freq * i / rate)
            out[2 * i] = (v * amp * 32767).roundToInt().toShort()
            out[2 * i + 1] = (v * right * 32767).roundToInt().toShort()
        }
        return out
    }

    private fun channel(s: ShortArray, c: Int) = ShortArray(s.size / 2) { s[2 * it + c] }

    @Test
    fun neutralLeavesTheSoundAlone() {
        val s = sine(440.0, 0.5)
        val copy = s.copyOf()
        EffectsDsp(rate, 2).process(s, s.size / 2, EffectsConfig())
        assertArrayEquals(copy, s)
    }

    @Test
    fun boostIsLouderButNeverClips() {
        val quiet = sine(440.0, 0.2)
        val before = EffectsDsp.rms(quiet)
        EffectsDsp(rate, 2).process(quiet, quiet.size / 2, EffectsConfig(boostDb = 12))
        assertTrue(EffectsDsp.rms(quiet) > before * 2.5) // +12 dB ≈ ×4, minus a little limiting

        val loud = sine(440.0, 0.8)
        EffectsDsp(rate, 2).process(loud, loud.size / 2, EffectsConfig(boostDb = 12))
        val peak = loud.maxOf { abs(it.toInt()) } / 32768.0
        assertTrue("peak $peak", peak <= EffectsDsp.CEILING + 0.001)
    }

    @Test
    fun monoAndBalance() {
        val s = sine(440.0, 0.5, right = 0.0) // left only
        EffectsDsp(rate, 2).process(s, s.size / 2, EffectsConfig(mono = true))
        assertArrayEquals(channel(s, 0), channel(s, 1))

        val b = sine(440.0, 0.5)
        EffectsDsp(rate, 2).process(b, b.size / 2, EffectsConfig(balance = 0.5f)) // towards the right
        assertEquals(0.5, EffectsDsp.rms(channel(b, 0)) / EffectsDsp.rms(channel(b, 1)), 0.02)
    }

    @Test
    fun voiceClarityCutsRumbleAndKeepsSpeech() {
        val rumble = sine(40.0, 0.5)
        val voice = sine(1_000.0, 0.5)
        val r0 = EffectsDsp.rms(rumble)
        val v0 = EffectsDsp.rms(voice)
        EffectsDsp(rate, 2).process(rumble, rumble.size / 2, EffectsConfig(voiceEq = true))
        EffectsDsp(rate, 2).process(voice, voice.size / 2, EffectsConfig(voiceEq = true))
        assertTrue(EffectsDsp.rms(rumble) < r0 * 0.3)
        assertTrue(EffectsDsp.rms(voice) > v0 * 0.9)
    }

    @Test
    fun effectSettingsStayInRange() {
        val s = PlaybackSettings(effects = EffectsConfig(boostDb = 40, balance = 0.33f)).sanitized()
        assertEquals(12, s.effects.boostDb)
        assertEquals(0.3f, s.effects.balance)
    }

    @Test
    fun coverUrisRoundTripAndRefuseOddPaths() {
        assertEquals(
            "acc-1" to "0190c3b4-0000-7000-8000-0000000000b1",
            CoverProvider.parse(Uri.parse("content://x.covers/cover/acc-1/0190c3b4-0000-7000-8000-0000000000b1"))
        )
        assertNull(CoverProvider.parse(Uri.parse("content://x.covers/cover/acc-1/..%2F..%2Fsecret")))
        assertNull(CoverProvider.parse(Uri.parse("content://x.covers/other/a/b")))
    }
}
