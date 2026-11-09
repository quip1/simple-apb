// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.account

import android.content.Context
import android.util.Base64
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import java.util.UUID
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** One signed-in account on one server (brief §7.4: several servers, switchable). */
@Serializable
data class Account(
    val id: String,
    val serverUrl: String,
    val serverName: String,
    val userId: String,
    val username: String,
    val displayName: String,
    val role: String
)

@Serializable
data class AccountsState(val accounts: List<Account> = emptyList(), val activeId: String? = null) {
    val active: Account? get() = accounts.firstOrNull { it.id == activeId }
}

/** An access/refresh token pair; times are epoch milliseconds. */
@Serializable
data class Tokens(val access: String, val accessExpiresAt: Long, val refresh: String, val refreshExpiresAt: Long)

internal val Context.accountsDataStore: DataStore<Preferences> by preferencesDataStore(name = "accounts")

/**
 * Accounts and their tokens. Account metadata is plain; tokens are sealed
 * with [SecretBox] (an Android Keystore key) before they touch the disk.
 */
class AccountStore(private val store: DataStore<Preferences>, private val box: SecretBox) {
    private val json = Json { ignoreUnknownKeys = true }
    private val stateKey = stringPreferencesKey("state")
    private val deviceKey = stringPreferencesKey("device_id")

    private fun tokensKey(id: String) = stringPreferencesKey("tokens.$id")

    val state: Flow<AccountsState> =
        store.data.map { prefs ->
            prefs[stateKey]?.let { runCatching { json.decodeFromString<AccountsState>(it) }.getOrNull() } ?: AccountsState()
        }

    suspend fun current(): AccountsState = state.first()

    /** Adds (or replaces, for the same user on the same server) an account and makes it active. */
    suspend fun add(account: Account, tokens: Tokens): Account {
        var saved = account
        store.edit { prefs ->
            val now = prefs[stateKey]?.let { json.decodeFromString<AccountsState>(it) } ?: AccountsState()
            val existing = now.accounts.firstOrNull { it.serverUrl == account.serverUrl && it.userId == account.userId }
            saved = if (existing != null) account.copy(id = existing.id) else account
            val accounts = now.accounts.filterNot { it.id == saved.id } + saved
            prefs[stateKey] = json.encodeToString(AccountsState.serializer(), AccountsState(accounts, saved.id))
            prefs[tokensKey(saved.id)] = seal(tokens)
        }
        return saved
    }

    suspend fun setActive(id: String) = edit { it.copy(activeId = id.takeIf { x -> it.accounts.any { a -> a.id == x } }) }

    /** Forgets an account and its tokens; another account becomes active if there is one. */
    suspend fun remove(id: String) {
        store.edit { prefs ->
            val now = prefs[stateKey]?.let { json.decodeFromString<AccountsState>(it) } ?: AccountsState()
            val accounts = now.accounts.filterNot { it.id == id }
            val active = if (now.activeId == id) accounts.lastOrNull()?.id else now.activeId
            prefs[stateKey] = json.encodeToString(AccountsState.serializer(), AccountsState(accounts, active))
            prefs.remove(tokensKey(id))
        }
    }

    suspend fun tokens(id: String): Tokens? {
        val sealed = store.data.first()[tokensKey(id)] ?: return null
        return runCatching { json.decodeFromString<Tokens>(String(box.open(Base64.decode(sealed, Base64.NO_WRAP)))) }.getOrNull()
    }

    suspend fun saveTokens(id: String, tokens: Tokens) {
        store.edit { it[tokensKey(id)] = seal(tokens) }
    }

    /** A random id for this installation (brief §3 "device"), created once. */
    suspend fun deviceId(): String {
        store.data.first()[deviceKey]?.let { return it }
        var id = ""
        store.edit { prefs ->
            id = prefs[deviceKey] ?: UUID.randomUUID().toString()
            prefs[deviceKey] = id
        }
        return id
    }

    private fun seal(t: Tokens): String = Base64.encodeToString(box.seal(json.encodeToString(Tokens.serializer(), t).toByteArray()), Base64.NO_WRAP)

    private suspend fun edit(change: (AccountsState) -> AccountsState) {
        store.edit { prefs ->
            val now = prefs[stateKey]?.let { json.decodeFromString<AccountsState>(it) } ?: AccountsState()
            prefs[stateKey] = json.encodeToString(AccountsState.serializer(), change(now))
        }
    }
}
