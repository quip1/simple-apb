// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import kotlin.math.sqrt

/** An active sleep timer (brief §7.2). Times are SystemClock.elapsedRealtime(). */
sealed interface SleepTimer {
    /** Stops at a moment in time. */
    data class Until(val endsAtMs: Long) : SleepTimer

    /** Stops when playback reaches the end of the chapter that was playing when it was set. */
    data class EndOfChapter(val chapterEndMs: Long) : SleepTimer

    companion object {
        const val FADE_MS = 10_000L

        /** Volume (0–1) while the timer runs: a linear fade over the last 10 s. */
        fun volume(t: SleepTimer, nowMs: Long, positionMs: Long, speed: Float): Float {
            val left = remainingMs(t, nowMs, positionMs, speed)
            return if (left >= FADE_MS) 1f else (left.toFloat() / FADE_MS).coerceIn(0f, 1f)
        }

        /** Wall-clock time left. For end of chapter, the playback left divided by the speed. */
        fun remainingMs(t: SleepTimer, nowMs: Long, positionMs: Long, speed: Float): Long = when (t) {
            is Until -> t.endsAtMs - nowMs
            is EndOfChapter -> ((t.chapterEndMs - positionMs) / speed.coerceAtLeast(0.1f)).toLong()
        }.coerceAtLeast(0)

        /** Shake to extend: [extendMs] on top of the time left. */
        fun extend(t: SleepTimer, nowMs: Long, positionMs: Long, speed: Float, extendMs: Long): SleepTimer =
            Until(nowMs + remainingMs(t, nowMs, positionMs, speed) + extendMs)
    }
}

/**
 * Detects a deliberate shake from accelerometer samples: two jolts above
 * [thresholdG] within [windowMs], then quiet for [cooldownMs] so one shake
 * extends once.
 */
class ShakeDetector(private val thresholdG: Float = 2.3f, private val windowMs: Long = 800, private val cooldownMs: Long = 1_500) {
    private var firstJoltAt = -1L
    private var lastShakeAt = Long.MIN_VALUE / 2

    /** Feed one sample (m/s²); true when it completes a shake. */
    fun onSample(x: Float, y: Float, z: Float, nowMs: Long): Boolean {
        val g = sqrt(x * x + y * y + z * z) / EARTH_G
        if (g < thresholdG || nowMs - lastShakeAt < cooldownMs) return false
        if (firstJoltAt >= 0 && nowMs - firstJoltAt in MIN_GAP_MS..windowMs) {
            firstJoltAt = -1
            lastShakeAt = nowMs
            return true
        }
        if (firstJoltAt < 0 || nowMs - firstJoltAt > windowMs) firstJoltAt = nowMs
        return false
    }

    private companion object {
        const val EARTH_G = 9.80665f
        const val MIN_GAP_MS = 100L
    }
}
