// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.book

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.time.Instant
import java.util.UUID
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.model.BookDetail
import org.simpleapb.api.model.Bookmark
import org.simpleapb.api.model.Collection
import org.simpleapb.api.model.CollectionCreate
import org.simpleapb.api.model.Listener
import org.simpleapb.api.model.Me
import org.simpleapb.api.model.RecommendRequest
import org.simpleapb.api.model.RecommendResult
import org.simpleapb.api.model.ReportCreate
import org.simpleapb.api.model.ReportReason
import org.simpleapb.api.model.ReviewInput
import org.simpleapb.api.model.ReviewPage
import org.simpleapb.api.model.UserRef
import org.simpleapb.api.model.UserRole
import org.simpleapb.api.model.UserStatus
import org.simpleapb.app.data.MeSession
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.Api
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.player.PlaybackRepository
import org.simpleapb.core.player.PlayerConnection
import org.simpleapb.core.player.PlayerState

/** A short confirmation shown on the book page. */
enum class BookNotice { ARCHIVED, RESTORED, REMOVED, REVIEW_SAVED, REPORT_SENT, DETAILS_SAVED }

data class BookState(
    val book: BookDetail? = null,
    val error: ApiFailure? = null,
    val notice: BookNotice? = null,
    val busy: Boolean = false,
    val listeners: List<Listener> = emptyList(),
    val bookmarks: List<Bookmark> = emptyList(),
    val reviews: ReviewPage? = null,
    val collections: List<Collection>? = null,
    val people: List<UserRef> = emptyList(),
    val recommended: RecommendResult? = null
)

/** The book page (brief §7.4 screen 4). */
@HiltViewModel
class BookViewModel
@Inject
constructor(
    private val session: MeSession,
    private val repo: PlaybackRepository,
    private val player: PlayerConnection,
    private val playerState: PlayerState
) : ViewModel() {
    private val _state = MutableStateFlow(BookState())
    val state: StateFlow<BookState> = _state.asStateFlow()
    val me: StateFlow<Me?> = session.me

    /** The book loaded in the player, to show its live position here. */
    val current = playerState.book
    val playback = player.ui

    private var id: UUID? = null

    fun load(bookId: String) = viewModelScope.launch {
        val uuid = runCatching { UUID.fromString(bookId) }.getOrNull() ?: return@launch
        id = uuid
        val api = session.api() ?: return@launch
        when (val r = call { api.library.getBook(uuid) }) {
            is Outcome.Ok -> _state.update { it.copy(book = r.value, error = null) }
            is Outcome.Failed -> {
                _state.update { it.copy(error = r.failure) }
                return@launch
            }
        }
        val listeners = (call { api.social.listListeners(uuid) } as? Outcome.Ok)?.value.orEmpty()
        val marks = (call { api.progress.listBookmarks(uuid) } as? Outcome.Ok)?.value.orEmpty().sortedBy { it.positionMs }
        val reviews = (call { api.social.listReviews(uuid, limit = REVIEW_PAGE) } as? Outcome.Ok)?.value
        _state.update { it.copy(listeners = listeners, bookmarks = marks, reviews = reviews) }
    }

    private fun reload() = id?.let { load(it.toString()) }

    fun clearNotice() = _state.update { it.copy(notice = null, error = null) }

    private suspend inline fun act(notice: BookNotice? = null, crossinline block: suspend (Api, UUID) -> Outcome<*>) {
        val api = session.api() ?: return
        val uuid = id ?: return
        _state.update { it.copy(busy = true, notice = null, error = null) }
        val r = block(api, uuid)
        _state.update { it.copy(busy = false, error = (r as? Outcome.Failed)?.failure, notice = if (r is Outcome.Ok) notice else null) }
        reload()
    }

    fun play() {
        val b = _state.value.book ?: return
        player.play(b.id.toString())
    }

    fun playFrom(ms: Long) {
        val b = _state.value.book ?: return
        player.playFrom(b.id.toString(), ms)
    }

    fun setFinished(on: Boolean) = viewModelScope.launch {
        val b = _state.value.book ?: return@launch
        val account = session.active() ?: return@launch
        _state.update { it.copy(busy = true, notice = null, error = null) }
        val r = repo.setFinished(account, b.id.toString(), b.durationMs, b.progress?.speed ?: 1f, on)
        // The player follows, so its next report agrees.
        if (r is Outcome.Ok<*> && playerState.book.value?.id == b.id.toString()) player.seekTo(if (on) b.durationMs else 0)
        _state.update { it.copy(busy = false, error = (r as? Outcome.Failed)?.failure) }
        reload()
    }

    fun archive(on: Boolean) = viewModelScope.launch {
        act(if (on) BookNotice.ARCHIVED else BookNotice.RESTORED) { api, id ->
            if (on) call { api.library.hideInMyLibrary(id) } else call { api.library.restoreToMyLibrary(id) }
        }
    }

    fun remove() = viewModelScope.launch {
        if (playerState.book.value?.id == id?.toString()) player.pause()
        act(BookNotice.REMOVED) { api, id -> call { api.library.removeFromMyLibrary(id) } }
    }

    fun restore() = viewModelScope.launch { act(BookNotice.RESTORED) { api, id -> call { api.library.restoreToMyLibrary(id) } } }

    // ── collections ──

    fun loadCollections() = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val r = call { api.progress.listCollections() }
        _state.update { it.copy(collections = (r as? Outcome.Ok)?.value ?: it.collections.orEmpty(), error = (r as? Outcome.Failed)?.failure) }
    }

    fun toggleCollection(c: Collection) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val uuid = id ?: return@launch
        val r = if (uuid in c.bookIds) call { api.progress.removeFromCollection(c.id, uuid) } else call { api.progress.addToCollection(c.id, uuid) }
        if (r is Outcome.Failed) _state.update { it.copy(error = r.failure) }
        loadCollections()
    }

    fun createCollection(name: String) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val uuid = id ?: return@launch
        val r =
            call { api.progress.createCollection(CollectionCreate(name = name.trim(), id = UUID.randomUUID(), clientTs = Instant.now().toString())) }
        if (r is Outcome.Ok) {
            call { api.progress.addToCollection(r.value.id, uuid) }
        } else if (r is Outcome.Failed) {
            _state.update { it.copy(error = r.failure) }
        }
        loadCollections()
    }

    // ── recommend ──

    fun searchPeople(q: String) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        if (q.isBlank()) {
            _state.update { it.copy(people = emptyList()) }
            return@launch
        }
        val r = call { api.social.searchPeople(q.trim()) }
        _state.update { it.copy(people = (r as? Outcome.Ok)?.value.orEmpty()) }
    }

    fun recommend(to: List<UserRef>, note: String) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val uuid = id ?: return@launch
        _state.update { it.copy(busy = true, error = null, recommended = null) }
        val r = call { api.social.recommendBook(uuid, RecommendRequest(to.map { it.id }, note.trim().ifEmpty { null })) }
        _state.update { it.copy(busy = false, recommended = (r as? Outcome.Ok)?.value, error = (r as? Outcome.Failed)?.failure) }
    }

    fun resetRecommend() = _state.update { it.copy(recommended = null, people = emptyList()) }

    // ── report ──

    fun report(type: ReportCreate.ObjectType, objectId: UUID, reason: ReportReason, details: String) = viewModelScope.launch {
        act(BookNotice.REPORT_SENT) { api, _ ->
            call { api.social.createReport(ReportCreate(type, objectId, reason, details.trim().ifEmpty { null })) }
        }
    }

    // ── reviews ──

    fun saveReview(stars: Int, text: String) = viewModelScope.launch {
        act(BookNotice.REVIEW_SAVED) { api, id -> call { api.social.putMyReview(id, ReviewInput(stars, text.trim().ifEmpty { null })) } }
    }

    fun deleteReview() = viewModelScope.launch { act { api, id -> call { api.social.deleteMyReview(id) } } }

    fun moreReviews() = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val uuid = id ?: return@launch
        val page = _state.value.reviews ?: return@launch
        val cursor = page.nextCursor ?: return@launch
        val r = call { api.social.listReviews(uuid, cursor = cursor, limit = REVIEW_PAGE) }
        if (r is Outcome.Ok) _state.update { it.copy(reviews = r.value.copy(items = page.items + r.value.items)) }
    }

    // ── bookmarks ──

    fun deleteBookmark(b: Bookmark) = viewModelScope.launch { act { api, _ -> call { api.progress.deleteBookmark(b.id) } } }

    companion object {
        const val REVIEW_PAGE = 20

        fun canRecommend(me: Me?) = me?.status == UserStatus.ACTIVE

        fun canReview(me: Me?) = me?.role == UserRole.ADMIN || me?.permissions?.canReview == true
    }
}
