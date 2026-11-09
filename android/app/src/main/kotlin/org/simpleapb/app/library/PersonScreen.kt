// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.library

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.util.UUID
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.simpleapb.api.model.BookSummary
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.ui.BookRow
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.Pager
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.auth.map
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

data class PersonState(val name: String = "", val books: List<BookSummary> = emptyList(), val loaded: Boolean = false, val error: ApiFailure? = null)

@HiltViewModel
class PersonViewModel
@Inject
constructor(private val session: MeSession) : ViewModel() {
    private val _state = MutableStateFlow(PersonState())
    val state: StateFlow<PersonState> = _state.asStateFlow()

    fun load(kind: PersonKind, id: String) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val uuid = runCatching { UUID.fromString(id) }.getOrNull() ?: return@launch
        val r =
            when (kind) {
                PersonKind.AUTHOR -> call { api.library.getAuthor(uuid) }.map { it.name to it.books }
                PersonKind.NARRATOR -> call { api.library.getNarrator(uuid) }.map { it.name to it.books }
                PersonKind.SERIES -> call { api.library.getSeries(uuid) }.map { it.name to it.books }
            }
        _state.value =
            when (r) {
                is Outcome.Ok -> PersonState(r.value.first, r.value.second, true)
                is Outcome.Failed -> PersonState(loaded = true, error = r.failure)
            }
    }
}

/** An author's, narrator's or series' books (brief §7.4). */
@Composable
fun PersonScreen(account: Account, kind: PersonKind, id: String, onBack: () -> Unit, onBook: (String) -> Unit) {
    val vm: PersonViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    val eink = LocalEink.current
    var page by rememberSaveable { mutableIntStateOf(1) }
    val list = rememberLazyListState()
    LaunchedEffect(kind, id) { vm.load(kind, id) }
    LaunchedEffect(page) { if (eink) list.scrollToItem(0) }
    val pages = (s.books.size + PAGE - 1) / PAGE
    val shown = if (eink) s.books.drop((page - 1) * PAGE).take(PAGE) else s.books
    LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(16.dp)) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                ScreenHeader(s.name.ifEmpty { stringResource(UiR.string.common_loading) }, onBack = onBack)
                Text(
                    stringResource(
                        when (kind) {
                            PersonKind.AUTHOR -> UiR.string.person_kind_author
                            PersonKind.NARRATOR -> UiR.string.person_kind_narrator
                            PersonKind.SERIES -> UiR.string.person_kind_series
                        }
                    ) + if (s.loaded) " · " + pluralStringResource(UiR.plurals.library_book_count, s.books.size, s.books.size) else "",
                    style = MaterialTheme.typography.bodyMedium
                )
                s.error?.let { Notice(errorText(it)) }
            }
        }
        items(shown, key = { it.id }) { b ->
            Column {
                BookRow(b, account.serverUrl) { onBook(b.id.toString()) }
                HorizontalDivider()
            }
        }
        if (eink) item { Pager(page, page > 1, page < pages, { page-- }, { page++ }) }
    }
}

private const val PAGE = 12
