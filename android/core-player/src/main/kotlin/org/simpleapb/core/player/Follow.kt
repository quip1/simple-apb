// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import java.time.Instant

/**
 * Following another device (ADR-0051): when this device is paused (or has
 * only just pressed play) and another device saved a newer position, jump
 * there quietly instead of carrying on from the old one and overwriting it.
 * Big jumps while both devices listened offline are the server's conflict
 * prompt's job, not this.
 */
object Follow {
    /** A position this device wrote itself shows up in the mirror a few ms after it saved it locally. */
    private const val OWN_WRITE_SLACK_MS = 1_000L

    /** Smaller differences aren't worth a jump. */
    const val MIN_JUMP_MS = 2_000L

    /** Just after play is pressed, a position fetched for the resume still counts as "paused". */
    const val RESUME_WINDOW_MS = 5_000L

    /**
     * The position to jump to, or null to stay. [remoteTs] is when the mirror's
     * position was written; [localSavedAtMs] when this device last saved its own.
     */
    fun target(remotePositionMs: Long, remoteTs: String?, remoteFinished: Boolean, localSavedAtMs: Long, currentMs: Long, paused: Boolean): Long? {
        if (!paused || remoteFinished) return null
        val at = remoteTs?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() } ?: return null
        if (at <= localSavedAtMs + OWN_WRITE_SLACK_MS) return null
        if (kotlin.math.abs(remotePositionMs - currentMs) < MIN_JUMP_MS) return null
        return remotePositionMs
    }
}
