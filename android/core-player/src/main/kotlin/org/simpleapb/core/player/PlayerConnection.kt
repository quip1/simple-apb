// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.content.ComponentName
import android.content.Context
import android.os.Bundle
import android.os.SystemClock
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionToken
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.guava.await
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** What the player screens draw. Positions are book-global. */
data class PlaybackUi(
    val playing: Boolean = false,
    val buffering: Boolean = false,
    val positionMs: Long = 0,
    val speed: Float = 1f,
    val sleepLeftMs: Long? = null,
    val failed: Boolean = false
)

enum class SleepChoice { OFF, END_OF_CHAPTER, MINUTES }

/**
 * The app's controller for the playback service: a MediaController (so the
 * app goes through the same session as the notification and headset), plus
 * book-level helpers.
 */
@Singleton
class PlayerConnection
@Inject
constructor(@ApplicationContext private val context: Context, private val state: PlayerState) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var controller: Deferred<MediaController>? = null
    private var ticker: Job? = null

    private val _ui = MutableStateFlow(PlaybackUi())
    val ui: StateFlow<PlaybackUi> = _ui.asStateFlow()

    init {
        // A book is loaded (by us, a headset or the system): follow it.
        scope.launch {
            state.book.filterNotNull().first()
            controller()
        }
    }

    private suspend fun controller(): MediaController {
        val c =
            controller ?: scope
                .async {
                    val token = SessionToken(context, ComponentName(context, PlaybackService::class.java))
                    MediaController.Builder(context, token).buildAsync().await().also { it.addListener(listener) }
                }.also { controller = it }
        return c.await()
    }

    private val listener =
        object : Player.Listener {
            override fun onEvents(player: Player, events: Player.Events) {
                if (events.contains(Player.EVENT_PLAYER_ERROR)) _ui.value = _ui.value.copy(failed = true)
                update(player)
            }
        }

    private fun update(p: Player) {
        val b = state.book.value
        val pos = b?.timeline?.global(p.currentMediaItemIndex, p.currentPosition) ?: p.currentPosition
        val sleep =
            state.sleep.value?.let {
                SleepTimer.remainingMs(it, SystemClock.elapsedRealtime(), pos, p.playbackParameters.speed)
            }
        _ui.value =
            PlaybackUi(
                playing = p.playWhenReady && p.playbackState != Player.STATE_ENDED,
                buffering = p.playbackState == Player.STATE_BUFFERING,
                positionMs = pos,
                speed = p.playbackParameters.speed,
                sleepLeftMs = sleep,
                failed = _ui.value.failed && p.playerError != null
            )
        val tick = p.isPlaying || state.sleep.value != null
        if (tick && ticker?.isActive != true) {
            ticker =
                scope.launch {
                    while (isActive) {
                        delay(TICK_MS)
                        update(p)
                        if (!p.isPlaying && state.sleep.value == null) break
                    }
                }
        }
    }

    private fun with(block: (MediaController) -> Unit) {
        scope.launch { block(controller()) }
    }

    /** Plays a book: resumes it if it's the one loaded, else loads it at its saved position. */
    fun play(bookId: String) = with { c ->
        if (state.book.value?.id == bookId && c.mediaItemCount > 0) {
            if (c.playbackState == Player.STATE_IDLE) c.prepare()
            c.play()
            return@with
        }
        _ui.value = PlaybackUi(buffering = true)
        c.setMediaItem(MediaItem.Builder().setMediaId(Commands.bookMediaId(bookId)).build())
        c.prepare()
        c.play()
    }

    fun togglePlay() = with { c ->
        val ended = c.playbackState == Player.STATE_ENDED
        if (c.playWhenReady && !ended) {
            c.pause()
        } else {
            // Play on a finished book starts it again from the beginning.
            if (ended) c.sendCustomCommand(SessionCommand(Commands.SEEK_BOOK, Bundle.EMPTY), Bundle().apply { putLong(Commands.EXTRA_POSITION, 0) })
            if (c.playbackState == Player.STATE_IDLE) c.prepare()
            c.play()
        }
    }

    private fun command(action: String, extras: Bundle = Bundle.EMPTY) = with { c ->
        c.sendCustomCommand(SessionCommand(action, Bundle.EMPTY), extras)
    }

    fun skipBack() = command(Commands.SKIP_BACK)

    fun skipForward() = command(Commands.SKIP_FORWARD)

    fun previousChapter() = command(Commands.PREVIOUS_CHAPTER)

    fun nextChapter() = command(Commands.NEXT_CHAPTER)

    fun seekTo(bookMs: Long) {
        _ui.value = _ui.value.copy(positionMs = bookMs)
        command(Commands.SEEK_BOOK, Bundle().apply { putLong(Commands.EXTRA_POSITION, bookMs) })
    }

    fun setSpeed(speed: Float) = with { it.setPlaybackSpeed(Speed.clamp(speed)) }

    fun setSleep(choice: SleepChoice, minutes: Int = 0) = command(
        Commands.SET_SLEEP,
        Bundle().apply {
            if (choice == SleepChoice.MINUTES) putInt(Commands.EXTRA_MINUTES, minutes)
            if (choice == SleepChoice.END_OF_CHAPTER) putBoolean(Commands.EXTRA_CHAPTER, true)
        }
    ).also { scope.launch { refresh() } }

    fun addBookmark(note: String?) = command(Commands.ADD_BOOKMARK, Bundle().apply { putString(Commands.EXTRA_NOTE, note) })

    fun resolveConflict(jump: Boolean) = command(Commands.RESOLVE_CONFLICT, Bundle().apply { putBoolean(Commands.EXTRA_JUMP, jump) })

    private suspend fun refresh() {
        delay(REFRESH_DELAY_MS)
        update(controller())
    }

    private companion object {
        const val TICK_MS = 500L
        const val REFRESH_DELAY_MS = 100L
    }
}

@Module
@InstallIn(SingletonComponent::class)
object PlayerModule {
    @Provides
    @Singleton
    fun playbackStore(@ApplicationContext context: Context): PlaybackStore = PlaybackStore(context.playbackDataStore)
}
