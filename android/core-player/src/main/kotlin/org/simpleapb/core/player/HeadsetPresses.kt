// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

enum class Gesture { SINGLE, DOUBLE, TRIPLE, LONG }

/**
 * Turns headset button down/up events into single, double, triple and long
 * presses (brief §7.2). A press is decided only once [windowMs] passes with
 * no further press, so a double press never also fires a single one. A long
 * press is either flagged by the system on a repeated key-down or detected
 * after [longMs] held. Many phones keep a long press of the headset button
 * for the voice assistant and pass on only the key-up, which is ignored.
 */
class HeadsetPresses(
    private val scope: CoroutineScope,
    private val windowMs: Long = 450,
    private val longMs: Long = 800,
    private val onGesture: (Gesture) -> Unit
) {
    private var presses = 0
    private var held = false
    private var longFired = false
    private var pending: Job? = null
    private var longJob: Job? = null

    fun down(repeat: Int, systemLongPress: Boolean = false) {
        if (systemLongPress && held && !longFired) {
            longJob?.cancel()
            fireLong()
            return
        }
        if (repeat > 0 || held) return // key repeat while held; the long-press timer handles it
        held = true
        pending?.cancel()
        longFired = false
        longJob?.cancel()
        longJob =
            scope.launch {
                delay(longMs)
                fireLong()
            }
    }

    private fun fireLong() {
        longFired = true
        presses = 0
        onGesture(Gesture.LONG)
    }

    fun up() {
        if (!held) return // a key-up whose key-down the system kept
        held = false
        longJob?.cancel()
        if (longFired) {
            longFired = false
            return
        }
        presses++
        pending?.cancel()
        pending =
            scope.launch {
                delay(windowMs)
                val n = presses
                presses = 0
                onGesture(
                    when {
                        n >= 3 -> Gesture.TRIPLE
                        n == 2 -> Gesture.DOUBLE
                        else -> Gesture.SINGLE
                    }
                )
            }
    }
}
