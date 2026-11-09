// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.home

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.simpleapb.api.endpoints.LibraryApi
import org.simpleapb.api.model.BookSummary
import org.simpleapb.app.ui.AppViewModel
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.ui.AppTheme
import org.simpleapb.core.ui.R as UiR

data class HomeState(val books: List<BookSummary> = emptyList(), val error: ApiFailure? = null, val loaded: Boolean = false)

/** Interim home: recently added books (tap to play), settings and accounts. The real Home comes in milestone 3. */
@HiltViewModel
class HomeViewModel
@Inject
constructor(private val apis: ApiFactory) : ViewModel() {
    private val _state = MutableStateFlow(HomeState())
    val state: StateFlow<HomeState> = _state.asStateFlow()

    fun load(account: Account) = viewModelScope.launch {
        val api = apis.forAccount(account).library
        when (val r = call { api.listLibrary(sort = LibraryApi.SortListLibrary.RECENT_ADDED, limit = 10) }) {
            is Outcome.Ok -> _state.value = HomeState(r.value.items, null, true)
            is Outcome.Failed -> _state.value = HomeState(emptyList(), r.failure, true)
        }
    }
}

@Composable
fun HomeScreen(app: AppViewModel, account: Account, accounts: List<Account>, onPlay: (String) -> Unit, onPlaybackSettings: () -> Unit) {
    val vm: HomeViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    val theme by app.theme.collectAsStateWithLifecycle()
    LaunchedEffect(account.id) { vm.load(account) }
    FormColumn {
        Text(
            stringResource(UiR.string.home_welcome, account.displayName),
            style = MaterialTheme.typography.headlineSmall,
            modifier = Modifier.semantics { heading() }
        )
        Notice(stringResource(UiR.string.app_home_coming), error = false)

        Text(
            stringResource(UiR.string.app_home_recent, account.serverName),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.semantics { heading() }
        )
        s.error?.let { Notice(errorText(it)) }
        if (s.loaded && s.books.isEmpty() && s.error == null) Text(stringResource(UiR.string.home_empty_body))
        Column {
            s.books.forEach { b ->
                val label = stringResource(UiR.string.app_home_play, b.title)
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier =
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = TouchTarget)
                        .clickable(role = Role.Button, onClickLabel = label) { onPlay(b.id.toString()) }
                        .padding(vertical = 12.dp)
                ) {
                    Text(b.title, modifier = Modifier.weight(1f))
                    Icon(Icons.Filled.PlayArrow, contentDescription = null)
                }
                HorizontalDivider()
            }
        }

        OutlinedButton(onClick = onPlaybackSettings, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.app_settings_playback_title))
        }

        Text(
            stringResource(UiR.string.settings_theme),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.semantics { heading() }
        )
        Column(Modifier.selectableGroup()) {
            listOf(AppTheme.DARK to UiR.string.settings_theme_dark, AppTheme.EINK to UiR.string.settings_theme_eink).forEach { (t, label) ->
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier =
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = TouchTarget)
                        .selectable(selected = theme == t, role = Role.RadioButton, onClick = { app.setTheme(t) })
                ) {
                    RadioButton(selected = theme == t, onClick = null)
                    Text(stringResource(label), modifier = Modifier.padding(vertical = 12.dp))
                }
            }
        }

        Text(
            stringResource(UiR.string.app_account_title),
            style = MaterialTheme.typography.titleMedium,
            modifier =
            Modifier.semantics {
                heading()
            }
        )
        accounts.forEach { a ->
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("${a.displayName} (@${a.username})", style = MaterialTheme.typography.bodyLarge)
                Text(a.serverName + " · " + a.serverUrl, style = MaterialTheme.typography.bodySmall)
                if (a.id == account.id) {
                    Text(stringResource(UiR.string.app_account_active), style = MaterialTheme.typography.labelMedium)
                } else {
                    TextButton(onClick = { app.switchTo(a) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                        Text(stringResource(UiR.string.app_account_switch, a.displayName, a.serverName))
                    }
                }
                TextButton(onClick = { app.signOut(a) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.auth_sign_out))
                }
            }
            HorizontalDivider()
        }
        OutlinedButton(onClick = app::startAdding, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.app_account_add))
        }
    }
}
