// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.settings

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonPrimitive
import org.simpleapb.api.model.Me
import org.simpleapb.api.model.MePatch
import org.simpleapb.api.model.UserRole
import org.simpleapb.app.UiSettings
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.ui.BusyButton
import org.simpleapb.app.ui.ChoicePicker
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.RadioGroup
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.Section
import org.simpleapb.app.ui.SwitchRow
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.player.PlaybackSettings
import org.simpleapb.core.player.PlaybackStore
import org.simpleapb.core.player.Quality
import org.simpleapb.core.ui.AppTheme
import org.simpleapb.core.ui.R as UiR

data class SettingsState(val busy: Boolean = false, val saved: Boolean = false, val error: ApiFailure? = null)

@HiltViewModel
class SettingsViewModel
@Inject
constructor(private val session: MeSession, private val store: PlaybackStore, private val ui: UiSettings) :
    ViewModel() {
    val me: StateFlow<Me?> = session.me
    val settings: StateFlow<PlaybackSettings?> = store.settings.stateIn(viewModelScope, SharingStarted.Eagerly, null)
    val theme: StateFlow<AppTheme> = ui.theme.stateIn(viewModelScope, SharingStarted.Eagerly, AppTheme.DARK)

    private val _state = MutableStateFlow(SettingsState())
    val state: StateFlow<SettingsState> = _state.asStateFlow()

    fun refresh() = viewModelScope.launch { session.refresh() }

    fun update(change: (PlaybackSettings) -> PlaybackSettings) = viewModelScope.launch { store.update(change) }

    fun setTheme(t: AppTheme) = viewModelScope.launch { ui.setTheme(t) }

    fun patch(body: MePatch, profile: Boolean = false) = viewModelScope.launch {
        _state.update { SettingsState(busy = profile) }
        val r = session.patch(body)
        _state.update { SettingsState(saved = profile && r is Outcome.Ok, error = (r as? Outcome.Failed)?.failure) }
    }
}

/** Settings (brief §7.4 screen 8). Theme and quality are per device; the rest follows the account. */
@Composable
fun SettingsScreen(
    account: Account,
    onPlayback: () -> Unit,
    onSecurity: () -> Unit,
    onAccounts: () -> Unit,
    onSignOut: () -> Unit,
    onFolders: () -> Unit,
    onNotifications: () -> Unit
) {
    val vm: SettingsViewModel = hiltViewModel()
    val me by vm.me.collectAsStateWithLifecycle()
    val loaded by vm.settings.collectAsStateWithLifecycle()
    val theme by vm.theme.collectAsStateWithLifecycle()
    val st by vm.state.collectAsStateWithLifecycle()
    LaunchedEffect(account.id) { vm.refresh() }
    val s = loaded ?: return
    FormColumn {
        ScreenHeader(stringResource(UiR.string.nav_settings))
        st.error?.let { Notice(errorText(it)) }

        val m = me
        if (m != null) {
            Section(stringResource(UiR.string.settings_profile)) {
                var name by rememberSaveable(m.displayName) { mutableStateOf(m.displayName) }
                var email by rememberSaveable(m.email) { mutableStateOf(m.email.orEmpty()) }
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text(stringResource(UiR.string.settings_display_name)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )
                OutlinedTextField(
                    value = email,
                    onValueChange = { email = it },
                    label = { Text(stringResource(UiR.string.settings_email)) },
                    supportingText = { Text(stringResource(UiR.string.settings_email_hint)) },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email),
                    modifier = Modifier.fillMaxWidth()
                )
                if (st.saved) Notice(stringResource(UiR.string.settings_saved), error = false)
                BusyButton(
                    stringResource(UiR.string.common_save),
                    st.busy,
                    onClick = { vm.patch(MePatch(displayName = name.trim(), email = email.trim()), profile = true) },
                    enabled = name.isNotBlank()
                )
            }
        }

        Section(stringResource(UiR.string.settings_appearance)) {
            Text(stringResource(UiR.string.settings_theme), style = MaterialTheme.typography.labelLarge)
            RadioGroup(theme, AppTheme.entries, {
                stringResource(
                    if (it ==
                        AppTheme.EINK
                    ) {
                        UiR.string.settings_theme_eink
                    } else {
                        UiR.string.settings_theme_dark
                    }
                )
            }, vm::setTheme)
            Text(stringResource(UiR.string.settings_this_device), style = MaterialTheme.typography.bodySmall)
            ChoicePicker(
                stringResource(UiR.string.app_settings_eink_tick),
                s.einkTickS,
                listOf(10, 15, 20, 30),
                { stringResource(UiR.string.app_settings_seconds, it.toString()) },
                { v -> vm.update { it.copy(einkTickS = v) } }
            )
            Text(stringResource(UiR.string.settings_eink_tick_help), style = MaterialTheme.typography.bodySmall)
            SwitchRow(stringResource(UiR.string.settings_left_handed), s.leftHanded, { v -> vm.update { it.copy(leftHanded = v) } })
        }

        Section(stringResource(UiR.string.settings_playback)) {
            OutlinedButton(onClick = onPlayback, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_settings_playback_title))
            }
            OutlinedButton(onClick = onFolders, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_local_folders))
            }
            OutlinedButton(onClick = onNotifications, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_push_title))
            }
            Text(stringResource(UiR.string.settings_quality), style = MaterialTheme.typography.labelLarge)
            RadioGroup(s.quality, Quality.entries, { qualityLabel(it) }) { q -> vm.update { it.copy(quality = q) } }
            Text(
                stringResource(UiR.string.app_settings_quality_help) + " " + stringResource(UiR.string.settings_this_device),
                style = MaterialTheme.typography.bodySmall
            )
            if (m != null) {
                Text(stringResource(UiR.string.settings_conflict_threshold), style = MaterialTheme.typography.labelLarge)
                ChoicePicker(
                    stringResource(UiR.string.app_settings_conflict),
                    m.conflictThresholdMinutes,
                    (listOf(1, 2, 3, 5, 10, 15, 30) + m.conflictThresholdMinutes).distinct().sorted(),
                    { pluralStringResource(UiR.plurals.settings_minutes, it, it) },
                    { vm.patch(MePatch(conflictThresholdMinutes = it)) }
                )
                Text(stringResource(UiR.string.settings_conflict_threshold_help), style = MaterialTheme.typography.bodySmall)
            }
        }

        if (m != null) {
            Section(stringResource(UiR.string.settings_privacy)) {
                SwitchRow(stringResource(UiR.string.settings_listening_visible), m.listeningVisible, { vm.patch(MePatch(listeningVisible = it)) })
            }
            if (m.role == UserRole.ADMIN) {
                Section(stringResource(UiR.string.settings_alerts)) {
                    val on = (m.prefs["email_alerts"] as? JsonPrimitive)?.content == "true"
                    SwitchRow(
                        stringResource(UiR.string.settings_email_alerts),
                        on,
                        { vm.patch(MePatch(prefs = m.prefs + ("email_alerts" to JsonPrimitive(it)))) },
                        enabled = m.email != null,
                        help = stringResource(
                            if (m.email !=
                                null
                            ) {
                                UiR.string.settings_email_alerts_hint
                            } else {
                                UiR.string.settings_email_alerts_no_email
                            }
                        )
                    )
                }
            }
        }

        Section(stringResource(UiR.string.settings_account)) {
            Text(stringResource(UiR.string.settings_signed_in_as, account.displayName, account.username) + " " + account.serverName)
            OutlinedButton(onClick = onSecurity, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.security_title))
            }
            OutlinedButton(onClick = onAccounts, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_account_title))
            }
            OutlinedButton(onClick = onSignOut, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.auth_sign_out))
            }
        }
    }
}

@Composable
private fun qualityLabel(q: Quality) = stringResource(
    when (q) {
        Quality.AUTO -> UiR.string.settings_quality_auto
        Quality.ORIGINAL -> UiR.string.settings_quality_original
        Quality.MEDIUM -> UiR.string.settings_quality_medium
        Quality.LOW -> UiR.string.settings_quality_low
    }
)
