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
import org.simpleapb.api.model.Me
import org.simpleapb.app.UiSettings
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.push.PushManager
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.AccountsState
import org.simpleapb.core.data.auth.AuthRepository
import org.simpleapb.core.data.db.DownloadRow
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.SessionEvent
import org.simpleapb.core.data.sync.SyncEngine
import org.simpleapb.core.player.PlayerConnection
import org.simpleapb.core.ui.AppTheme

/** App-wide state: accounts, the active one, the theme, and sign-out notices. */
@HiltViewModel
class AppViewModel
@Inject
constructor(
    private val store: AccountStore,
    private val auth: AuthRepository,
    private val apis: ApiFactory,
    private val ui: UiSettings,
    session: MeSession,
    private val sync: SyncEngine,
    downloadsRepo: Downloads,
    private val push: PushManager,
    private val player: PlayerConnection
) : ViewModel() {
    /** Every download on the device (badges, the Downloaded view). */
    val downloads: StateFlow<List<DownloadRow>> = downloadsRepo.all.stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    /** The active account's details (also starts syncing the shared preferences). */
    val me: StateFlow<Me?> = session.me

    /** null until loaded from disk. */
    val accounts: StateFlow<AccountsState?> = store.state.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** null until read from disk, so an e-ink screen never flashes the dark theme first. */
    val theme: StateFlow<AppTheme?> = ui.theme.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    private val _signedOut = MutableStateFlow<String?>(null)

    /** Name of a server whose session was refused (shown once). */
    val signedOut: StateFlow<String?> = _signedOut.asStateFlow()

    init {
        viewModelScope.launch {
            apis.sessionEvents.collect { e ->
                if (e is SessionEvent.SignedOut) {
                    val acc = store.current().accounts.firstOrNull { it.id == e.accountId }
                    player.closeAccount(e.accountId)
                    apis.forget(e.accountId)
                    store.remove(e.accountId)
                    sync.forget(e.accountId)
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

    private val _openPlayer = MutableStateFlow(false)

    /** The playback notification was tapped: show the player. */
    val openPlayer: StateFlow<Boolean> = _openPlayer.asStateFlow()

    fun requestPlayer() {
        _openPlayer.value = true
    }

    fun playerOpened() {
        _openPlayer.value = false
    }

    private val _openBook = MutableStateFlow<String?>(null)

    /** A notification about a book was tapped: show its page (after switching to its account). */
    val openBook: StateFlow<String?> = _openBook.asStateFlow()

    fun requestBook(accountId: String?, bookId: String) = viewModelScope.launch {
        if (accountId != null &&
            store.current().activeId != accountId &&
            store.current().accounts.any { it.id == accountId }
        ) {
            store.setActive(accountId)
        }
        _openBook.value = bookId
    }

    fun bookOpened() {
        _openBook.value = null
    }

    /** "Just play files on this device": the app without a server (brief §7.4 onboarding). */
    val localMode: StateFlow<Boolean?> = ui.localMode.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    fun useLocal(on: Boolean) = viewModelScope.launch { ui.setLocalMode(on) }

    fun setTheme(theme: AppTheme) = viewModelScope.launch { ui.setTheme(theme) }

    fun switchTo(account: Account) = viewModelScope.launch { store.setActive(account.id) }

    /** Signs out and forgets the account's mirror, queued writes and downloads on this device. */
    fun signOut(account: Account) = viewModelScope.launch {
        sync.sync(account) // send queued progress first, if the server is reachable
        push.unregister(account)
        player.closeAccount(account.id)
        auth.signOut(account)
        sync.forget(account.id)
    }
}
