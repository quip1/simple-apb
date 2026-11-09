// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.simpleapb.app.UiSettings
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.AccountsState
import org.simpleapb.core.data.auth.AuthRepository
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.SessionEvent
import org.simpleapb.core.ui.AppTheme

/** App-wide state: accounts, the active one, the theme, and sign-out notices. */
@HiltViewModel
class AppViewModel
@Inject
constructor(
    private val store: AccountStore,
    private val auth: AuthRepository,
    private val apis: ApiFactory,
    private val ui: UiSettings
) : ViewModel() {
    /** null until loaded from disk. */
    val accounts: StateFlow<AccountsState?> = store.state.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    val theme: StateFlow<AppTheme> = ui.theme.stateIn(viewModelScope, SharingStarted.Eagerly, AppTheme.DARK)

    private val _signedOut = MutableStateFlow<String?>(null)

    /** Name of a server whose session was refused (shown once). */
    val signedOut: StateFlow<String?> = _signedOut.asStateFlow()

    init {
        viewModelScope.launch {
            apis.sessionEvents.collect { e ->
                if (e is SessionEvent.SignedOut) {
                    val acc = store.current().accounts.firstOrNull { it.id == e.accountId }
                    apis.forget(e.accountId)
                    store.remove(e.accountId)
                    _signedOut.value = acc?.serverName
                }
            }
        }
    }

    private val _tour = MutableStateFlow(false)

    /** Show the 3-screen tour (after the first sign-in on this device). */
    val tour: StateFlow<Boolean> = _tour.asStateFlow()

    private val _adding = MutableStateFlow(false)

    /** The user is adding another server while signed in. */
    val adding: StateFlow<Boolean> = _adding.asStateFlow()

    fun startAdding() {
        _adding.value = true
    }

    fun stopAdding() {
        _adding.value = false
    }

    /** Called when onboarding finishes with a new active account. */
    fun signedIn(firstAccount: Boolean) {
        _adding.value = false
        if (firstAccount) _tour.value = true
    }

    fun tourDone() {
        _tour.value = false
    }

    fun dismissSignedOut() {
        _signedOut.value = null
    }

    fun setTheme(theme: AppTheme) = viewModelScope.launch { ui.setTheme(theme) }

    fun switchTo(account: Account) = viewModelScope.launch { store.setActive(account.id) }

    fun signOut(account: Account) = viewModelScope.launch { auth.signOut(account) }
}
