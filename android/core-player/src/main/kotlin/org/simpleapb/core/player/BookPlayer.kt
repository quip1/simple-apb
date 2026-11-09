// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import androidx.annotation.OptIn
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi

/**
 * The player every controller sees. Audiobooks don't have "tracks": next and
 * previous (Bluetooth, car, watch) skip forward and back by the configured
 * amount, and resuming applies auto-rewind first.
 */
@OptIn(UnstableApi::class)
internal class BookPlayer(player: Player, private val hooks: Hooks) : ForwardingPlayer(player) {
    interface Hooks {
        fun beforePlay()

        fun skipBack()

        fun skipForward()
    }

    override fun play() {
        hooks.beforePlay()
        super.play()
    }

    override fun setPlayWhenReady(playWhenReady: Boolean) {
        if (playWhenReady && !wrappedPlayer.playWhenReady) hooks.beforePlay()
        super.setPlayWhenReady(playWhenReady)
    }

    override fun seekToNext() = hooks.skipForward()

    override fun seekToNextMediaItem() = hooks.skipForward()

    override fun seekForward() = hooks.skipForward()

    override fun seekToPrevious() = hooks.skipBack()

    override fun seekToPreviousMediaItem() = hooks.skipBack()

    override fun seekBack() = hooks.skipBack()

    override fun getAvailableCommands(): Player.Commands {
        val base = super.getAvailableCommands()
        if (wrappedPlayer.currentTimeline.isEmpty) return base
        return base.buildUpon().addAll(*SKIPS).build()
    }

    override fun isCommandAvailable(command: Int): Boolean =
        (command in SKIPS && !wrappedPlayer.currentTimeline.isEmpty) || super.isCommandAvailable(command)

    private companion object {
        val SKIPS =
            intArrayOf(
                COMMAND_SEEK_TO_NEXT,
                COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
                COMMAND_SEEK_TO_PREVIOUS,
                COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                COMMAND_SEEK_FORWARD,
                COMMAND_SEEK_BACK
            )
    }
}
