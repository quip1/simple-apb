// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.net

import javax.inject.Qualifier
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.AccountsState

/** The app-wide coroutine scope: work that must outlive a screen or the playback service. */
@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class AppScope

/**
 * An OkHttp [Call.Factory] for media (audio files, covers) that signs each
 * request in as the account whose server it goes to. Several servers can be
 * signed in at once, and a book keeps streaming from its own server after
 * the user switches the active account.
 */
class AccountCalls(store: AccountStore, private val apis: ApiFactory, private val base: OkHttpClient, scope: CoroutineScope) : Call.Factory {
    val accounts: StateFlow<AccountsState> = store.state.stateIn(scope, SharingStarted.Eagerly, AccountsState())

    override fun newCall(request: Request): Call = clientFor(request.url.toString()).newCall(request)

    /** The signed-in client for a URL, or the plain client when no account matches. */
    fun clientFor(url: String): OkHttpClient = accountFor(accounts.value.accounts, url)?.let(apis::http) ?: base

    companion object {
        /** The account with the longest server address that [url] starts with. */
        fun accountFor(accounts: List<Account>, url: String): Account? = accounts
            .filter { url == it.serverUrl || url.startsWith(it.serverUrl + "/") }
            .maxByOrNull { it.serverUrl.length }
    }
}
