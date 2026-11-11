// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.library

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.time.Instant
import java.util.UUID
import javax.inject.Inject
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.endpoints.LibraryApi.KindListLibraryFacets
import org.simpleapb.api.endpoints.LibraryApi.ShelfListLibrary
import org.simpleapb.api.endpoints.LibraryApi.SortListLibrary
import org.simpleapb.api.model.BookDetail
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.Collection
import org.simpleapb.api.model.CollectionCreate
import org.simpleapb.api.model.CollectionPatch
import org.simpleapb.api.model.LibraryFacet
import org.simpleapb.api.model.SharedCollection
import org.simpleapb.app.UiSettings
import org.simpleapb.app.data.MeSession
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.sync.LocalLibrary
import org.simpleapb.core.data.sync.SyncEngine

enum class LibraryView { BOOKS, DOWNLOADED, LOCAL, SERIES, AUTHORS, NARRATORS, GENRES, COLLECTIONS }

data class LibraryState(
    val view: LibraryView = LibraryView.BOOKS,
    val shelf: ShelfListLibrary = ShelfListLibrary.ALL,
    val sort: SortListLibrary = SortListLibrary.RECENT_ADDED,
    val query: String = "",
    val tag: LibraryFacet? = null,
    val collectionId: UUID? = null,
    val books: List<BookSummary> = emptyList(),
    val nextCursor: String? = null,
    /** E-ink: 1-based page, and the cursor each page started from. */
    val page: Int = 1,
    val cursors: List<String?> = listOf(null),
    val facets: List<LibraryFacet> = emptyList(),
    val facetPage: Int = 1,
    val collections: List<Collection> = emptyList(),
    val shared: List<SharedCollection> = emptyList(),
    val loading: Boolean = false,
    val loaded: Boolean = false,
    val error: ApiFailure? = null,
    /** The server is out of reach: the list comes from this device's copy. */
    val offline: Boolean = false
) {
    val ownCollection get() = collections.firstOrNull { it.id == collectionId }
    val sharedCollection get() = shared.firstOrNull { it.id == collectionId }
}

/** The library (brief §7.4 screen 3). */
@HiltViewModel
class LibraryViewModel
@Inject
constructor(
    private val session: MeSession,
    private val ui: UiSettings,
    private val local: LocalLibrary,
    private val downloads: Downloads,
    engine: SyncEngine
) : ViewModel() {
    init {
        viewModelScope.launch { engine.changes.collect { if (started) reload() } }
    }

    private val _state = MutableStateFlow(LibraryState())
    val state: StateFlow<LibraryState> = _state.asStateFlow()

    val listLayout: StateFlow<Boolean> = ui.libraryList.stateIn(viewModelScope, SharingStarted.Eagerly, false)

    private var eink = false
    private var job: Job? = null
    private var started = false

    private val pageSize get() = if (eink) EINK_PAGE else PAGE

    /** Called once with the screen's starting filter (from Home or a book's genre). */
    fun start(shelf: String?, tagId: String?, tagName: String?, eink: Boolean) {
        this.eink = eink
        if (started) return
        started = true
        val s = ShelfListLibrary.entries.firstOrNull { it.value == shelf } ?: ShelfListLibrary.ALL
        val tag = tagId?.let { id -> runCatching { UUID.fromString(id) }.getOrNull() }?.let { LibraryFacet(it, tagName.orEmpty(), 0) }
        _state.update { it.copy(shelf = s, tag = tag, sort = if (s == ShelfListLibrary.IN_PROGRESS) SortListLibrary.RECENT_PLAYED else it.sort) }
        reload()
    }

    fun reload() {
        when (_state.value.view) {
            LibraryView.BOOKS -> fetchBooks(null, append = false, page = 1, cursors = listOf(null))
            LibraryView.DOWNLOADED -> loadDownloaded()
            // Books in folders on this device come straight from the scan.
            LibraryView.LOCAL -> _state.update {
                it.copy(books = emptyList(), nextCursor = null, loading = false, loaded = true, error = null, offline = false)
            }
            LibraryView.COLLECTIONS -> loadCollections()
            else -> loadFacets()
        }
        if (_state.value.collectionId != null) loadCollections()
    }

    fun setView(v: LibraryView) {
        _state.update { it.copy(view = v, tag = null, collectionId = null, facets = emptyList(), facetPage = 1, error = null) }
        reload()
    }

    fun setShelf(s: ShelfListLibrary) {
        _state.update { it.copy(shelf = s) }
        reload()
    }

    fun setSort(s: SortListLibrary) {
        _state.update { it.copy(sort = s) }
        reload()
    }

    private var searchJob: Job? = null

    /** Instant search, debounced while typing. Searching always shows books. */
    fun setQuery(q: String) {
        _state.update { it.copy(query = q) }
        searchJob?.cancel()
        searchJob =
            viewModelScope.launch {
                delay(SEARCH_DEBOUNCE_MS)
                if (_state.value.view != LibraryView.BOOKS) _state.update { it.copy(view = LibraryView.BOOKS, tag = null, collectionId = null) }
                reload()
            }
    }

    fun showGenre(f: LibraryFacet) {
        _state.update { it.copy(view = LibraryView.BOOKS, tag = f, collectionId = null) }
        reload()
    }

    fun openCollection(id: UUID) {
        _state.update { it.copy(view = LibraryView.BOOKS, collectionId = id, tag = null, shelf = ShelfListLibrary.ALL) }
        reload()
    }

    fun clearFilter() {
        _state.update { it.copy(tag = null, collectionId = null) }
        reload()
    }

    fun setListLayout(list: Boolean) = viewModelScope.launch { ui.setLibraryList(list) }

    private fun fetchBooks(cursor: String?, append: Boolean, page: Int, cursors: List<String?>) {
        job?.cancel()
        _state.update { it.copy(loading = true) }
        job =
            viewModelScope.launch {
                val api = session.api() ?: return@launch
                val s = _state.value
                val r =
                    call {
                        api.library.listLibrary(
                            shelf = s.shelf,
                            sort = s.sort,
                            q = s.query.trim().ifEmpty { null },
                            tag = s.tag?.id,
                            collection = s.collectionId,
                            cursor = cursor,
                            limit = pageSize
                        )
                    }
                _state.update {
                    when (r) {
                        is Outcome.Ok ->
                            it.copy(
                                books = if (append) it.books + r.value.items else r.value.items,
                                nextCursor = r.value.nextCursor,
                                page = page,
                                cursors = cursors,
                                loading = false,
                                loaded = true,
                                error = null,
                                offline = false
                            )
                        is Outcome.Failed -> it.copy(loading = false, loaded = true, error = r.failure)
                    }
                }
                if (r is Outcome.Failed && r.failure == ApiFailure.Offline && cursor == null) offlineBooks()
            }
    }

    /** Offline: the mirror, filtered and sorted here (the first page only on e-ink). */
    private suspend fun offlineBooks() {
        val account = session.active() ?: return
        val s = _state.value
        val progress = local.allProgress(account.id)
        val all = local.books(account.id).map { b -> progress[b.id.toString()]?.let { b.copy(progress = it) } ?: b }
        val books = OfflineFilter.apply(all, s.shelf, s.sort, s.query, s.tag?.id?.toString())
        _state.update {
            it.copy(
                books = if (eink) books.take(EINK_PAGE) else books,
                nextCursor = null,
                page = 1,
                loading = false,
                loaded = true,
                error = null,
                offline = true
            )
        }
    }

    /** Books downloaded to this device, newest download first. */
    private fun loadDownloaded() {
        job?.cancel()
        job =
            viewModelScope.launch {
                val account = session.active() ?: return@launch
                val rows = downloads.all.first().filter { it.accountId == account.id && it.state == DownloadState.DONE }
                val progress = local.allProgress(account.id)
                val books =
                    rows.mapNotNull { r ->
                        val b = local.book(account.id, r.bookId) ?: local.detail(account.id, r.bookId)?.summary() ?: return@mapNotNull null
                        progress[r.bookId]?.let { b.copy(progress = it) } ?: b
                    }
                _state.update { it.copy(books = books, nextCursor = null, page = 1, loading = false, loaded = true, error = null, offline = false) }
            }
    }

    /** Dark theme: endless scrolling. */
    fun loadMore() {
        val s = _state.value
        if (s.loading || s.nextCursor == null || eink) return
        fetchBooks(s.nextCursor, append = true, page = 1, cursors = s.cursors)
    }

    /** E-ink: explicit pages. */
    fun nextPage() {
        val s = _state.value
        val next = s.nextCursor ?: return
        fetchBooks(next, append = false, page = s.page + 1, cursors = s.cursors.take(s.page) + next)
    }

    fun previousPage() {
        val s = _state.value
        if (s.page <= 1) return
        fetchBooks(s.cursors[s.page - 2], append = false, page = s.page - 1, cursors = s.cursors)
    }

    fun setFacetPage(p: Int) = _state.update { it.copy(facetPage = p) }

    private fun loadFacets() {
        val kind =
            when (_state.value.view) {
                LibraryView.SERIES -> KindListLibraryFacets.SERIES
                LibraryView.AUTHORS -> KindListLibraryFacets.AUTHORS
                LibraryView.NARRATORS -> KindListLibraryFacets.NARRATORS
                LibraryView.GENRES -> KindListLibraryFacets.TAGS
                else -> return
            }
        job?.cancel()
        _state.update { it.copy(loading = true) }
        job =
            viewModelScope.launch {
                val api = session.api() ?: return@launch
                val r = call { api.library.listLibraryFacets(kind) }
                _state.update {
                    when (r) {
                        is Outcome.Ok -> it.copy(facets = r.value, loading = false, loaded = true, error = null)
                        is Outcome.Failed -> it.copy(loading = false, loaded = true, error = r.failure)
                    }
                }
            }
    }

    private fun loadCollections() = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val own = call { api.progress.listCollections() }
        val other = call { api.social.listSharedCollections() }
        _state.update {
            it.copy(
                collections = (own as? Outcome.Ok)?.value ?: it.collections,
                shared = (other as? Outcome.Ok)?.value ?: it.shared,
                loading = false,
                loaded = true,
                error = (own as? Outcome.Failed)?.failure
            )
        }
    }

    fun createCollection(name: String) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val r =
            call { api.progress.createCollection(CollectionCreate(name = name.trim(), id = UUID.randomUUID(), clientTs = Instant.now().toString())) }
        if (r is Outcome.Failed) _state.update { it.copy(error = r.failure) }
        loadCollections()
    }

    fun renameCollection(name: String) = viewModelScope.launch {
        val id = _state.value.collectionId ?: return@launch
        val api = session.api() ?: return@launch
        val r = call { api.progress.updateCollection(id, CollectionPatch(name = name.trim(), clientTs = Instant.now().toString())) }
        if (r is Outcome.Failed) _state.update { it.copy(error = r.failure) }
        loadCollections()
    }

    fun deleteCollection() = viewModelScope.launch {
        val id = _state.value.collectionId ?: return@launch
        val api = session.api() ?: return@launch
        val r = call { api.progress.deleteCollection(id) }
        if (r is Outcome.Failed) {
            _state.update { it.copy(error = r.failure) }
        } else {
            setView(LibraryView.COLLECTIONS)
        }
    }

    companion object {
        const val PAGE = 48
        const val EINK_PAGE = 12
        const val FACET_PAGE = 15
        const val SEARCH_DEBOUNCE_MS = 250L
    }
}

/** A book's details as a library entry (for downloads the mirror has no entry for). */
fun BookDetail.summary() = BookSummary(
    id = id,
    title = title,
    authors = authors,
    narrators = narrators,
    durationMs = durationMs,
    coverUrl = coverUrl,
    hasCover = hasCover,
    source = source,
    updatedAt = updatedAt,
    subtitle = subtitle,
    series = series,
    progress = progress,
    addedAt = addedAt,
    hidden = hidden,
    removedAt = removedAt
)

/** The library's shelves, sort and search, done on this device for the offline list. */
object OfflineFilter {
    fun apply(all: List<BookSummary>, shelf: ShelfListLibrary, sort: SortListLibrary, query: String, tagId: String?): List<BookSummary> {
        val q = query.trim().lowercase()
        val shown =
            all.filter { b ->
                val p = b.progress
                val onShelf =
                    when (shelf) {
                        ShelfListLibrary.ALL -> b.hidden != true && b.removedAt == null
                        ShelfListLibrary.IN_PROGRESS -> p != null && p.finishedAt == null && p.positionMs > 0 && b.hidden != true
                        ShelfListLibrary.NOT_STARTED -> (p == null || (p.positionMs == 0L && p.finishedAt == null)) && b.hidden != true
                        ShelfListLibrary.FINISHED -> p?.finishedAt != null
                        ShelfListLibrary.ARCHIVED -> b.hidden == true
                        ShelfListLibrary.RECENTLY_REMOVED -> b.removedAt != null
                    }
                val matches =
                    q.isEmpty() ||
                        b.title.lowercase().contains(q) ||
                        b.authors.any { it.name.lowercase().contains(q) } ||
                        b.narrators.any { it.name.lowercase().contains(q) } ||
                        b.series?.name?.lowercase()?.contains(q) == true
                // Summaries carry no tags, so a genre filter can't apply offline: show nothing rather than everything.
                onShelf && matches && tagId == null
            }
        return when (sort) {
            SortListLibrary.TITLE -> shown.sortedBy { it.title.lowercase() }
            SortListLibrary.AUTHOR -> shown.sortedBy { it.authors.firstOrNull()?.name?.lowercase() ?: "" }
            SortListLibrary.SERIES -> shown.sortedWith(
                compareBy({ it.series?.name?.lowercase() ?: "\uffff" }, {
                    it.series?.index?.toDoubleOrNull()
                        ?: 0.0
                })
            )
            SortListLibrary.DURATION -> shown.sortedBy { it.durationMs }
            SortListLibrary.RECENT_PLAYED -> shown.sortedByDescending { it.progress?.clientTs ?: "" }
            SortListLibrary.RECENT_ADDED -> shown.sortedByDescending { it.addedAt ?: it.updatedAt }
        }
    }
}
