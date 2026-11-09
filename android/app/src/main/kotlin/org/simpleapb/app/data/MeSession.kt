// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.data

import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.floatOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive
import org.simpleapb.api.model.Me
import org.simpleapb.api.model.MePatch
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.Api
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.AppScope
import org.simpleapb.core.player.PlaybackSettings
import org.simpleapb.core.player.PlaybackStore

/**
 * The active account's own details (`/me`), and the playback preferences
 * shared with the account's other devices (ADR-0030, ADR-0041): skip lengths,
 * the e-ink refresh interval, the left-handed layout and the speed for new
 * books. Last write wins; keys written by other clients are kept.
 */
@Singleton
class MeSession
@Inject
constructor(
    private val accounts: AccountStore,
    private val apis: ApiFactory,
    private val playback: PlaybackStore,
    @AppScope private val scope: CoroutineScope
) {
    private val _me = MutableStateFlow<Me?>(null)

    /** The active account's details; null until loaded (or while offline). */
    val me: StateFlow<Me?> = _me.asStateFlow()

    // The synced values last adopted from or sent to the server.
    private var lastSynced: Map<String, JsonElement>? = null
    private var push: Job? = null

    init {
        scope.launch {
            accounts.state.map { it.active?.id }.distinctUntilChanged().collect { id ->
                _me.value = null
                lastSynced = null
                push?.cancel()
                if (id != null) refresh()
            }
        }
        scope.launch { playback.settings.collect { onLocalChange(it) } }
    }

    suspend fun active(): Account? = accounts.current().active

    suspend fun api(): Api? = active()?.let { apis.forAccount(it) }

    /** Re-reads `/me` and adopts the account's synced preferences. */
    suspend fun refresh(): Outcome<Me> {
        val account = active() ?: return Outcome.Failed(SIGNED_OUT)
        val r = call { apis.forAccount(account).me.getMe() }
        if (r is Outcome.Ok && active()?.id == account.id) {
            _me.value = r.value
            adopt(r.value.prefs)
        }
        return r
    }

    suspend fun patch(body: MePatch): Outcome<Me> {
        val account = active() ?: return Outcome.Failed(SIGNED_OUT)
        val r = call { apis.forAccount(account).me.updateMe(body) }
        if (r is Outcome.Ok && active()?.id == account.id) _me.value = r.value
        return r
    }

    private suspend fun adopt(prefs: Map<String, JsonElement>) {
        val next = SyncedPrefs.apply(playback.settings(), prefs)
        // Set first, so the change we are about to write isn't sent back.
        lastSynced = SyncedPrefs.extract(next)
        playback.update { SyncedPrefs.apply(it, prefs) }
    }

    private fun onLocalChange(s: PlaybackSettings) {
        val last = lastSynced ?: return // not adopted yet: nothing to compare with
        val now = SyncedPrefs.extract(s)
        if (now == last) return
        push?.cancel()
        push =
            scope.launch {
                delay(PUSH_DELAY_MS)
                val me = _me.value ?: return@launch
                if (patch(MePatch(prefs = me.prefs + now)) is Outcome.Ok) lastSynced = now
            }
    }

    private companion object {
        const val PUSH_DELAY_MS = 800L
        val SIGNED_OUT = ApiFailure("signed_out", "", 401)
    }
}

/** The preferences every client shares, with their names on the wire (as on the web). */
object SyncedPrefs {
    fun extract(s: PlaybackSettings): Map<String, JsonElement> = mapOf(
        "skip_back_s" to JsonPrimitive(s.skipBackS),
        "skip_forward_s" to JsonPrimitive(s.skipForwardS),
        "eink_tick_s" to JsonPrimitive(s.einkTickS),
        "left_handed" to JsonPrimitive(s.leftHanded),
        "default_speed" to JsonPrimitive(s.defaultSpeed)
    )

    /** [s] with the values the account has; missing or mistyped values are left alone. */
    fun apply(s: PlaybackSettings, prefs: Map<String, JsonElement>): PlaybackSettings {
        fun int(k: String) = runCatching { prefs[k]?.jsonPrimitive?.takeUnless { it.isString }?.intOrNull }.getOrNull()

        fun bool(k: String) = runCatching { prefs[k]?.jsonPrimitive?.takeUnless { it.isString }?.booleanOrNull }.getOrNull()

        fun float(k: String) = runCatching { prefs[k]?.jsonPrimitive?.takeUnless { it.isString }?.floatOrNull }.getOrNull()
        return s
            .copy(
                skipBackS = int("skip_back_s") ?: s.skipBackS,
                skipForwardS = int("skip_forward_s") ?: s.skipForwardS,
                einkTickS = int("eink_tick_s") ?: s.einkTickS,
                leftHanded = bool("left_handed") ?: s.leftHanded,
                defaultSpeed = float("default_speed") ?: s.defaultSpeed
            ).sanitized()
    }
}
