// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import java.time.Instant
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class FollowTest {
    private val saved = Instant.parse("2026-11-13T22:00:00Z").toEpochMilli()
    private val later = "2026-11-13T22:01:00Z"

    @Test
    fun followsAnotherDevicesNewerPositionWhilePaused() {
        assertEquals(150_000L, Follow.target(150_000, later, false, saved, 88_000, paused = true))
    }

    @Test
    fun staysPut() {
        // Playing: the user is listening here.
        assertNull(Follow.target(150_000, later, false, saved, 88_000, paused = false))
        // This device's own write, coming back from the server a moment later.
        assertNull(Follow.target(150_000, "2026-11-13T22:00:00.400Z", false, saved, 88_000, paused = true))
        // Older than this device's own last position.
        assertNull(Follow.target(150_000, "2026-11-13T21:59:00Z", false, saved, 88_000, paused = true))
        // About where we are already, or finished elsewhere, or no time at all.
        assertNull(Follow.target(89_000, later, false, saved, 88_000, paused = true))
        assertNull(Follow.target(150_000, later, true, saved, 88_000, paused = true))
        assertNull(Follow.target(150_000, null, false, saved, 88_000, paused = true))
    }
}
