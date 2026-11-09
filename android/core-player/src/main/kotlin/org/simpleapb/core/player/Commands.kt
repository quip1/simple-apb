// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.os.Bundle
import androidx.media3.session.SessionCommand

/** Custom session commands: the notification buttons and the app's own controls. */
object Commands {
    const val SKIP_BACK = "org.simpleapb.player.SKIP_BACK"
    const val SKIP_FORWARD = "org.simpleapb.player.SKIP_FORWARD"
    const val PREVIOUS_CHAPTER = "org.simpleapb.player.PREVIOUS_CHAPTER"
    const val NEXT_CHAPTER = "org.simpleapb.player.NEXT_CHAPTER"
    const val CYCLE_SPEED = "org.simpleapb.player.CYCLE_SPEED"
    const val TOGGLE_SLEEP = "org.simpleapb.player.TOGGLE_SLEEP"

    /** Extras: [EXTRA_MINUTES] (Int), or [EXTRA_CHAPTER] true; neither turns the timer off. */
    const val SET_SLEEP = "org.simpleapb.player.SET_SLEEP"

    /** Extras: optional [EXTRA_NOTE]. */
    const val ADD_BOOKMARK = "org.simpleapb.player.ADD_BOOKMARK"

    /** Extras: [EXTRA_JUMP] — true to take the other device's position. */
    const val RESOLVE_CONFLICT = "org.simpleapb.player.RESOLVE_CONFLICT"

    /** Extras: [EXTRA_POSITION] (Long, book-global ms). */
    const val SEEK_BOOK = "org.simpleapb.player.SEEK_BOOK"

    const val EXTRA_MINUTES = "minutes"
    const val EXTRA_CHAPTER = "chapter"
    const val EXTRA_NOTE = "note"
    const val EXTRA_JUMP = "jump"
    const val EXTRA_POSITION = "position"

    val ALL =
        listOf(
            SKIP_BACK,
            SKIP_FORWARD,
            PREVIOUS_CHAPTER,
            NEXT_CHAPTER,
            CYCLE_SPEED,
            TOGGLE_SLEEP,
            SET_SLEEP,
            ADD_BOOKMARK,
            RESOLVE_CONFLICT,
            SEEK_BOOK
        ).map { SessionCommand(it, Bundle.EMPTY) }

    /** The media id a controller sets to ask the service to load a book. */
    const val BOOK_PREFIX = "book:"

    fun bookMediaId(bookId: String) = BOOK_PREFIX + bookId
}
