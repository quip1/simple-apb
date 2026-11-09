// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.util.UUID
import javax.inject.Inject
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.endpoints.LibraryApi.ShelfListLibrary
import org.simpleapb.api.endpoints.LibraryApi.SortListLibrary
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.Recommendation
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.library.OfflineFilter
import org.simpleapb.app.library.summary
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.Shelf
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.Api
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.sync.LocalLibrary
import org.simpleapb.core.data.sync.SyncEngine
import org.simpleapb.core.ui.R as UiR

data class HomeState(
    val loaded: Boolean = false,
    val error: ApiFailure? = null,
    val continuing: List<BookSummary> = emptyList(),
    val recommended: List<Recommendation> = emptyList(),
    val nextUp: List<BookSummary> = emptyList(),
    val recent: List<BookSummary> = emptyList(),
    /** Offline: books downloaded to this device. */
    val downloaded: List<BookSummary> = emptyList(),
    val offline: Boolean = false
) {
    val empty get() =
        loaded && error == null && continuing.isEmpty() && recommended.isEmpty() && nextUp.isEmpty() && recent.isEmpty() && downloaded.isEmpty()
}

/** Home (brief §7.4 screen 2): continue listening, recommended, next in series, recently added. */
@HiltViewModel
class HomeViewModel
@Inject
constructor(
    private val session: MeSession,
    private val local: LocalLibrary,
    private val downloads: Downloads,
    engine: SyncEngine
) : ViewModel() {
    private val _state = MutableStateFlow(HomeState())
    val state: StateFlow<HomeState> = _state.asStateFlow()

    init {
        viewModelScope.launch { engine.changes.collect { if (_state.value.loaded) load() } }
    }

    fun load() = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val lib = api.library
        val r =
            coroutineScope {
                val c = async { call { lib.listLibrary(shelf = ShelfListLibrary.IN_PROGRESS, sort = SortListLibrary.RECENT_PLAYED, limit = ROW) } }
                val n = async { call { lib.listLibrary(shelf = ShelfListLibrary.ALL, sort = SortListLibrary.RECENT_ADDED, limit = ROW) } }
                val f = async { call { lib.listLibrary(shelf = ShelfListLibrary.FINISHED, sort = SortListLibrary.RECENT_PLAYED, limit = 20) } }
                val rec = async { call { api.social.listMyRecommendations() } }
                listOf(c, n, f, rec).awaitAll()
            }
        val failure = r.filterIsInstance<Outcome.Failed>().firstOrNull()?.failure
        if (failure == ApiFailure.Offline) {
            offline()
            return@launch
        }

        @Suppress("UNCHECKED_CAST")
        fun <T> ok(i: Int): T? = (r[i] as? Outcome.Ok<*>)?.value as T?
        val continuing = ok<org.simpleapb.api.model.LibraryPage>(0)?.items.orEmpty()
        val recent = ok<org.simpleapb.api.model.LibraryPage>(1)?.items.orEmpty()
        val finished = ok<org.simpleapb.api.model.LibraryPage>(2)?.items.orEmpty()
        // One card per book, newest recommendation first.
        val recs = ok<List<Recommendation>>(3).orEmpty().distinctBy { it.book.id }
        _state.value =
            HomeState(
                loaded = true,
                error = failure,
                continuing = continuing,
                recommended = recs,
                nextUp = nextInSeries(api, finished),
                recent = recent
            )
    }

    /** Offline: what this device knows — books in progress, and the downloads. */
    private suspend fun offline() {
        val account = session.active() ?: return
        val progress = local.allProgress(account.id)
        val all = local.books(account.id).map { b -> progress[b.id.toString()]?.let { b.copy(progress = it) } ?: b }
        val done = downloads.all.first().filter { it.accountId == account.id && it.state == DownloadState.DONE }.map { it.bookId }.toSet()
        val downloaded =
            done.mapNotNull { id -> all.firstOrNull { it.id.toString() == id } ?: local.detail(account.id, id)?.summary() }
        _state.value =
            HomeState(
                loaded = true,
                offline = true,
                continuing = OfflineFilter.apply(all, ShelfListLibrary.IN_PROGRESS, SortListLibrary.RECENT_PLAYED, "", null).take(ROW),
                downloaded = downloaded
            )
    }

    /** The next unstarted book in each series the user has finished a book of. */
    private suspend fun nextInSeries(api: Api, finished: List<BookSummary>): List<BookSummary> = coroutineScope {
        finished
            .mapNotNull { it.series }
            .distinctBy { it.id }
            .take(6)
            .map { s -> async { call { api.library.getSeries(s.id) } } }
            .awaitAll()
            .mapNotNull { r ->
                val books = (r as? Outcome.Ok)?.value?.books ?: return@mapNotNull null
                val lastFinished = books.indexOfLast { it.progress?.finishedAt != null }
                books.drop(lastFinished + 1).firstOrNull { it.progress?.finishedAt == null && (it.progress?.positionMs ?: 0) == 0L }
            }
    }

    fun dismiss(bookId: UUID) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val all = (call { api.social.listMyRecommendations() } as? Outcome.Ok)?.value.orEmpty()
        all.filter { it.book.id == bookId }.forEach { call { api.social.dismissRecommendation(it.id) } }
        _state.update { s -> s.copy(recommended = s.recommended.filter { it.book.id != bookId }) }
    }

    private companion object {
        const val ROW = 12
    }
}

@Composable
fun HomeScreen(account: Account, onBook: (String) -> Unit, onLibrary: (String?) -> Unit) {
    val vm: HomeViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    LaunchedEffect(account.id) { vm.load() }
    val open = { b: BookSummary -> onBook(b.id.toString()) }
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp, vertical = 16.dp),
        verticalArrangement = Arrangement.spacedBy(24.dp)
    ) {
        ScreenHeader(stringResource(UiR.string.home_welcome, account.displayName)) {
            IconButton(onClick = { vm.load() }, modifier = Modifier.heightIn(min = TouchTarget)) {
                Icon(Icons.Filled.Refresh, contentDescription = stringResource(UiR.string.app_common_refresh))
            }
        }
        s.error?.let { Notice(errorText(it)) }
        if (s.offline) Notice(stringResource(UiR.string.app_offline_notice), error = false)
        when {
            !s.loaded -> Text(stringResource(UiR.string.common_loading))
            s.empty -> {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(stringResource(UiR.string.home_empty_title), style = MaterialTheme.typography.titleMedium)
                    Text(stringResource(UiR.string.home_empty_body))
                }
            }
            else -> {
                if (s.continuing.isNotEmpty()) {
                    Shelf(stringResource(UiR.string.home_continue), s.continuing, account.serverUrl, open, onSeeAll = { onLibrary("in_progress") })
                }
                if (s.recommended.isNotEmpty()) {
                    val byBook = s.recommended.associateBy { it.book.id }
                    Shelf(stringResource(UiR.string.home_recommended), s.recommended.map { it.book }, account.serverUrl, open) { b ->
                        byBook[b.id]?.let { r ->
                            Text(stringResource(UiR.string.home_recommended_by, r.from.displayName), style = MaterialTheme.typography.bodySmall)
                            r.note?.let { Text("“$it”", style = MaterialTheme.typography.bodySmall, fontStyle = FontStyle.Italic, maxLines = 3) }
                            TextButton(onClick = { vm.dismiss(b.id) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                                Text(stringResource(UiR.string.home_dismiss))
                            }
                        }
                    }
                }
                if (s.downloaded.isNotEmpty()) Shelf(stringResource(UiR.string.app_library_view_downloaded), s.downloaded, account.serverUrl, open)
                if (s.nextUp.isNotEmpty()) Shelf(stringResource(UiR.string.home_next_in_series), s.nextUp, account.serverUrl, open)
                if (s.recent.isNotEmpty()) {
                    Shelf(stringResource(UiR.string.home_recently_added), s.recent, account.serverUrl, open, onSeeAll = { onLibrary(null) })
                }
                OutlinedButton(onClick = { onLibrary(null) }, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.home_browse_library))
                }
            }
        }
    }
}
