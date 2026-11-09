// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import org.simpleapb.core.data.net.ApiFailure

/** The book loaded into the player. */
data class LoadedBook(
    val id: String,
    val accountId: String,
    val serverUrl: String,
    val title: String,
    val authors: String,
    val narrators: String,
    val coverUrl: String?,
    val timeline: BookTimeline,
    val startMs: Long,
    val speed: Float,
    /** Set when streaming the server's low-bitrate HLS version (one item for the whole book). */
    val hlsProfile: String? = null
)

/**
 * The server holds a position from another device that is far from ours
 * (brief §6.8). The user picks one; the answer is sent with [conflictId].
 */
data class PendingConflict(val bookId: String, val conflictId: String, val serverPositionMs: Long, val serverDevice: String?)

/** One-off things the UI may tell the user about. */
sealed interface PlayerEvent {
    data class Bookmarked(val positionMs: Long) : PlayerEvent

    data object SleepExtended : PlayerEvent

    data object PlaybackFailed : PlayerEvent

    data class LoadFailed(val failure: ApiFailure) : PlayerEvent

    data class BookmarkFailed(val failure: ApiFailure) : PlayerEvent
}

/**
 * State the playback service shares with the app's screens. Both run in one
 * process; controllers (notification, headset, Auto) only ever see the
 * MediaSession.
 */
@Singleton
class PlayerState
@Inject
constructor() {
    val book = MutableStateFlow<LoadedBook?>(null)
    val sleep = MutableStateFlow<SleepTimer?>(null)
    val conflict = MutableStateFlow<PendingConflict?>(null)
    val events = MutableSharedFlow<PlayerEvent>(extraBufferCapacity = 8)
}
