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
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.endpoints.LibraryApi.ShelfListLibrary
import org.simpleapb.api.endpoints.LibraryApi.SortListLibrary
import org.simpleapb.api.model.BookSummary
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.library.OfflineFilter
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.Api
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.millis
import org.simpleapb.core.data.sync.LocalLibrary
import org.simpleapb.core.data.sync.SyncEngine
import org.simpleapb.core.ui.R as UiR

data class PlacesState(
    val loaded: Boolean = false,
    val error: ApiFailure? = null,
    val offline: Boolean = false,
    val books: List<BookSummary> = emptyList(),
    val unsent: Set<String> = emptySet(),
    val sending: Boolean = false
)

/** Your places (ADR-0056): every book you've started, at its own place. */
@HiltViewModel
class PlacesViewModel
@Inject
constructor(private val session: MeSession, private val local: LocalLibrary, private val engine: SyncEngine) :
    ViewModel() {
    private val _state = MutableStateFlow(PlacesState())
    val state: StateFlow<PlacesState> = _state.asStateFlow()

    private val accountId = MutableStateFlow<String?>(null)

    /** Places saved here and waiting to be sent. */
    @OptIn(ExperimentalCoroutinesApi::class)
    val waiting: StateFlow<Int> =
        accountId
            .flatMapLatest { id -> if (id == null) flowOf(0) else local.unsentCount(id) }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), 0)

    init {
        viewModelScope.launch { engine.changes.collect { if (_state.value.loaded) load() } }
    }

    fun load() = viewModelScope.launch {
        val account = session.active() ?: return@launch
        accountId.value = account.id
        val api = session.api() ?: return@launch
        val r = coroutineScope {
            val going = async { all(api, ShelfListLibrary.IN_PROGRESS) }
            val done = async { all(api, ShelfListLibrary.FINISHED) }
            going.await() to done.await()
        }
        val failure = (r.first as? Outcome.Failed)?.failure ?: (r.second as? Outcome.Failed)?.failure
        val books =
            if (failure == ApiFailure.Offline) {
                val progress = local.allProgress(account.id)
                val mine = local.books(account.id).map { b -> progress[b.id.toString()]?.let { b.copy(progress = it) } ?: b }
                OfflineFilter.apply(mine, ShelfListLibrary.IN_PROGRESS, SortListLibrary.RECENT_PLAYED, "", null) +
                    OfflineFilter.apply(mine, ShelfListLibrary.FINISHED, SortListLibrary.RECENT_PLAYED, "", null)
            } else {
                (r.first as? Outcome.Ok)?.value.orEmpty() + (r.second as? Outcome.Ok)?.value.orEmpty()
            }
        val (shown, unsent) = withUnsent(books, local.unsentPlaces(account.id))
        _state.value =
            PlacesState(
                loaded = true,
                error = failure?.takeIf { it != ApiFailure.Offline },
                offline = failure == ApiFailure.Offline,
                books = shown.sortedByDescending { b -> b.progress?.updatedAt?.let { millis(it) } ?: 0L },
                unsent = unsent
            )
    }

    /** Every page of a shelf, most recently played first. */
    private suspend fun all(api: Api, shelf: ShelfListLibrary): Outcome<List<BookSummary>> {
        val out = mutableListOf<BookSummary>()
        var cursor: String? = null
        do {
            when (val r = call { api.library.listLibrary(shelf = shelf, sort = SortListLibrary.RECENT_PLAYED, limit = PAGE, cursor = cursor) }) {
                is Outcome.Ok -> {
                    out += r.value.items
                    cursor = r.value.nextCursor
                }
                is Outcome.Failed -> return r
            }
        } while (cursor != null && out.size < MAX)
        return Outcome.Ok(out)
    }

    /** Sends the waiting places now. */
    fun sendNow() = viewModelScope.launch {
        val account = session.active() ?: return@launch
        _state.update { it.copy(sending = true) }
        engine.sync(account)
        _state.update { it.copy(sending = false) }
        load()
    }

    private companion object {
        const val PAGE = 200
        const val MAX = 2_000
    }
}

@Composable
fun PlacesScreen(account: Account, onBook: (String) -> Unit, onPlay: (String) -> Unit) {
    val vm: PlacesViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    val waiting by vm.waiting.collectAsStateWithLifecycle()
    LaunchedEffect(account.id) { vm.load() }
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        ScreenHeader(stringResource(UiR.string.sync_title))
        Text(stringResource(UiR.string.sync_intro))
        if (waiting > 0) {
            Notice(pluralStringResource(UiR.plurals.sync_waiting, waiting, waiting), error = false)
            OutlinedButton(onClick = { vm.sendNow() }, enabled = !s.sending, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.sync_send_now))
            }
        } else if (s.loaded && s.error == null && !s.offline) {
            Text(stringResource(UiR.string.sync_all_sent))
        }
        s.error?.let { Notice(errorText(it)) }
        if (s.offline) Notice(stringResource(UiR.string.app_offline_notice), error = false)
        when {
            !s.loaded -> Text(stringResource(UiR.string.common_loading))
            s.books.isEmpty() -> Text(stringResource(UiR.string.sync_empty))
            else -> BookPlaces(
                s.books,
                s.unsent,
                account.serverUrl,
                details = true,
                onOpen = { onBook(it.id.toString()) },
                onPlay = { onPlay(it.id.toString()) }
            )
        }
    }
}
