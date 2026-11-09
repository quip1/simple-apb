// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.serialization.json.Json

internal val Context.playbackDataStore: DataStore<Preferences> by preferencesDataStore(name = "playback")

/** Where this device last was in a book, and when (epoch ms). */
data class LocalPosition(val positionMs: Long, val atMs: Long)

/** The book to resume when a headset or the system asks to resume playback. */
data class LastBook(val accountId: String, val bookId: String)

/**
 * Playback settings and small per-book records on this device: speed, the
 * last position (so nothing is lost when a progress report can't reach the
 * server), and when progress last synced. The Room mirror takes over the
 * per-book records with the offline milestone.
 */
class PlaybackStore(private val store: DataStore<Preferences>) {
    private val json =
        Json {
            ignoreUnknownKeys = true
            encodeDefaults = true
        }
    private val settingsKey = stringPreferencesKey("settings")
    private val lastBookKey = stringPreferencesKey("last_book")

    private fun speedKey(bookId: String) = stringPreferencesKey("speed.$bookId")

    private fun positionKey(bookId: String) = stringPreferencesKey("pos.$bookId")

    private fun syncedKey(bookId: String) = stringPreferencesKey("synced.$bookId")

    val settings: Flow<PlaybackSettings> =
        store.data.map { prefs ->
            prefs[settingsKey]
                ?.let { runCatching { json.decodeFromString(PlaybackSettings.serializer(), it) }.getOrNull() }
                ?.sanitized() ?: PlaybackSettings()
        }

    suspend fun settings(): PlaybackSettings = settings.first()

    suspend fun update(change: (PlaybackSettings) -> PlaybackSettings) {
        store.edit { prefs ->
            val now =
                prefs[settingsKey]
                    ?.let { runCatching { json.decodeFromString(PlaybackSettings.serializer(), it) }.getOrNull() }
                    ?: PlaybackSettings()
            prefs[settingsKey] = json.encodeToString(PlaybackSettings.serializer(), change(now).sanitized())
        }
    }

    suspend fun speed(bookId: String): Float? = store.data.first()[speedKey(bookId)]?.toFloatOrNull()

    suspend fun setSpeed(bookId: String, speed: Float) {
        store.edit { it[speedKey(bookId)] = Speed.clamp(speed).toString() }
    }

    suspend fun localPosition(bookId: String): LocalPosition? {
        val raw = store.data.first()[positionKey(bookId)] ?: return null
        val (pos, at) = raw.split(',').mapNotNull { it.toLongOrNull() }.takeIf { it.size == 2 } ?: return null
        return LocalPosition(pos, at)
    }

    suspend fun saveLocalPosition(bookId: String, positionMs: Long, atMs: Long) {
        store.edit { it[positionKey(bookId)] = "$positionMs,$atMs" }
    }

    suspend fun syncedAt(bookId: String): String? = store.data.first()[syncedKey(bookId)]

    suspend fun markSynced(bookId: String, at: String) {
        store.edit { it[syncedKey(bookId)] = at }
    }

    suspend fun lastBook(): LastBook? {
        val raw = store.data.first()[lastBookKey] ?: return null
        val parts = raw.split('|')
        return if (parts.size == 2) LastBook(parts[0], parts[1]) else null
    }

    suspend fun setLastBook(accountId: String, bookId: String) {
        store.edit { it[lastBookKey] = "$accountId|$bookId" }
    }
}
