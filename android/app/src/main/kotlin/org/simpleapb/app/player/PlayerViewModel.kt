// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.player

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.util.UUID
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.simpleapb.api.model.Bookmark
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.player.EffectsConfig
import org.simpleapb.core.player.LoadedBook
import org.simpleapb.core.player.PendingConflict
import org.simpleapb.core.player.PlaybackRepository
import org.simpleapb.core.player.PlaybackSettings
import org.simpleapb.core.player.PlaybackStore
import org.simpleapb.core.player.PlaybackUi
import org.simpleapb.core.player.PlayerConnection
import org.simpleapb.core.player.PlayerEvent
import org.simpleapb.core.player.PlayerState
import org.simpleapb.core.player.SleepChoice
import org.simpleapb.core.player.SleepTimer

data class BookmarksState(val items: List<Bookmark> = emptyList(), val loading: Boolean = false, val error: ApiFailure? = null)

/** The player screen, the mini player and the conflict prompt. */
@HiltViewModel
class PlayerViewModel
@Inject
constructor(
    private val conn: PlayerConnection,
    private val state: PlayerState,
    private val repo: PlaybackRepository,
    private val store: PlaybackStore
) : ViewModel() {
    val ui: StateFlow<PlaybackUi> = conn.ui
    val book: StateFlow<LoadedBook?> = state.book
    val sleep: StateFlow<SleepTimer?> = state.sleep
    val conflict: StateFlow<PendingConflict?> = state.conflict
    val events: SharedFlow<PlayerEvent> = state.events
    val settings: StateFlow<PlaybackSettings> = store.settings.stateIn(viewModelScope, SharingStarted.Eagerly, PlaybackSettings())

    private val _bookmarks = MutableStateFlow(BookmarksState())
    val bookmarks: StateFlow<BookmarksState> = _bookmarks.asStateFlow()

    init {
        viewModelScope.launch {
            state.events.collect { if (it is PlayerEvent.Bookmarked) loadBookmarks() }
        }
    }

    fun play(bookId: String) = conn.play(bookId)

    fun setEffects(e: EffectsConfig) = viewModelScope.launch { store.update { it.copy(effects = e) } }

    fun togglePlay() = conn.togglePlay()

    fun playOrResume() = conn.playOrResume()

    fun skipBack() = conn.skipBack()

    fun skipForward() = conn.skipForward()

    fun previousChapter() = conn.previousChapter()

    fun nextChapter() = conn.nextChapter()

    fun seekTo(ms: Long) = conn.seekTo(ms)

    fun setSpeed(s: Float) = conn.setSpeed(s)

    fun setSleep(choice: SleepChoice, minutes: Int = 0) = conn.setSleep(choice, minutes)

    fun addBookmark(note: String?) = conn.addBookmark(note)

    fun resolveConflict(jump: Boolean) = conn.resolveConflict(jump)

    fun loadBookmarks() {
        val b = state.book.value ?: return
        _bookmarks.value = _bookmarks.value.copy(loading = true, error = null)
        viewModelScope.launch {
            _bookmarks.value =
                when (val r = repo.bookmarks(b)) {
                    is Outcome.Ok -> BookmarksState(r.value.sortedBy { it.positionMs })
                    is Outcome.Failed -> _bookmarks.value.copy(loading = false, error = r.failure)
                }
        }
    }

    fun deleteBookmark(id: UUID) {
        val b = state.book.value ?: return
        viewModelScope.launch {
            when (val r = repo.deleteBookmark(b, id)) {
                is Outcome.Ok -> _bookmarks.value = _bookmarks.value.copy(items = _bookmarks.value.items.filterNot { it.id == id })
                is Outcome.Failed -> _bookmarks.value = _bookmarks.value.copy(error = r.failure)
            }
        }
    }
}
