// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import kotlin.math.roundToInt
import kotlinx.serialization.Serializable

/** Buttons the notification and lock screen can show (brief §7.2). */
enum class ControlButton { BACK, PLAY_PAUSE, FORWARD, PREVIOUS_CHAPTER, NEXT_CHAPTER, SPEED, SLEEP }

/** What a headset press does. */
enum class HeadsetAction { NONE, PLAY_PAUSE, BACK, FORWARD, PREVIOUS_CHAPTER, NEXT_CHAPTER, BOOKMARK }

/** What happens when another app briefly needs the audio (navigation prompts). Calls always pause. */
enum class FocusMode { PAUSE, DUCK }

/** Per-device playback settings. */
@Serializable
data class PlaybackSettings(
    val skipBackS: Int = 30,
    val skipForwardS: Int = 30,
    val autoRewindS: Int = 3,
    val defaultSpeed: Float = 1f,
    val buttons: List<ControlButton> = DEFAULT_BUTTONS,
    val singlePress: HeadsetAction = HeadsetAction.PLAY_PAUSE,
    val doublePress: HeadsetAction = HeadsetAction.FORWARD,
    val triplePress: HeadsetAction = HeadsetAction.BACK,
    val longPress: HeadsetAction = HeadsetAction.BOOKMARK,
    val focus: FocusMode = FocusMode.PAUSE,
    val shakeToExtend: Boolean = true,
    val shakeExtendMin: Int = 5,
    val lastSleepMin: Int = 30
) {
    /** The same settings with every value inside its allowed range. */
    fun sanitized(): PlaybackSettings = copy(
        skipBackS = skipBackS.coerceIn(SKIP_RANGE),
        skipForwardS = skipForwardS.coerceIn(SKIP_RANGE),
        autoRewindS = autoRewindS.coerceIn(REWIND_RANGE),
        defaultSpeed = Speed.clamp(defaultSpeed),
        buttons = normalizeButtons(buttons),
        shakeExtendMin = shakeExtendMin.coerceIn(1, 30),
        lastSleepMin = lastSleepMin.coerceIn(1, 600)
    )

    fun action(g: Gesture): HeadsetAction = when (g) {
        Gesture.SINGLE -> singlePress
        Gesture.DOUBLE -> doublePress
        Gesture.TRIPLE -> triplePress
        Gesture.LONG -> longPress
    }

    companion object {
        val SKIP_RANGE = 5..120
        val REWIND_RANGE = 0..30
        const val MAX_BUTTONS = 5
        val DEFAULT_BUTTONS = listOf(ControlButton.BACK, ControlButton.PLAY_PAUSE, ControlButton.FORWARD)

        /** No duplicates, at most five, and play/pause always there (the system always shows it). */
        fun normalizeButtons(list: List<ControlButton>): List<ControlButton> {
            val distinct = list.distinct()
            val withPlay = if (ControlButton.PLAY_PAUSE in distinct) distinct else distinct + ControlButton.PLAY_PAUSE
            if (withPlay.size <= MAX_BUTTONS) return withPlay
            val others = withPlay.filter { it != ControlButton.PLAY_PAUSE }.take(MAX_BUTTONS - 1).toSet()
            return withPlay.filter { it == ControlButton.PLAY_PAUSE || it in others }
        }
    }
}

/** Playback speed: 0.5×–3.5× in 0.05 steps (brief §7.2). */
object Speed {
    const val MIN = 0.5f
    const val MAX = 3.5f
    const val STEP = 0.05f

    fun clamp(s: Float): Float {
        if (s.isNaN()) return 1f
        val steps = (s / STEP).roundToInt()
        return (steps * 5 / 100f).coerceIn(MIN, MAX)
    }

    /** The speeds the notification button cycles through. */
    val CYCLE = listOf(1f, 1.25f, 1.5f, 1.75f, 2f)

    fun next(current: Float): Float = CYCLE.firstOrNull { it > current + 0.001f } ?: CYCLE.first()
}

/**
 * Auto-rewind on resume (brief §7.2): the full amount after a pause of a
 * minute or more, half after a shorter one.
 */
fun autoRewindMs(settingS: Int, pausedForMs: Long): Long {
    if (settingS <= 0 || pausedForMs <= 0) return 0
    val full = settingS * 1_000L
    return if (pausedForMs >= 60_000) full else full / 2
}
