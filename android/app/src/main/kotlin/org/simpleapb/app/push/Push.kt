// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.push

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import dagger.hilt.android.AndroidEntryPoint
import dagger.hilt.android.qualifiers.ApplicationContext
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.PushRegistration
import org.simpleapb.app.MainActivity
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppScope
import org.simpleapb.core.data.sync.SyncEngine
import org.simpleapb.core.ui.R as UiR
import org.unifiedpush.android.connector.FailedReason
import org.unifiedpush.android.connector.PushService
import org.unifiedpush.android.connector.UnifiedPush
import org.unifiedpush.android.connector.data.PushEndpoint
import org.unifiedpush.android.connector.data.PushMessage

private val Context.pushStore: DataStore<Preferences> by preferencesDataStore(name = "push")

/** How this device learns about changes (brief §6.14). */
sealed interface PushStatus {
    /** Push through a UnifiedPush distributor app (ntfy or another). */
    data class Active(val distributor: String) : PushStatus

    /** Distributors are installed but none is chosen yet. */
    data object Choose : PushStatus

    /** No distributor: live while open, and a check every 15 minutes. */
    data object Fallback : PushStatus
}

/** A push payload: an event type and an id, never content (§6.14). */
@Serializable
data class PushEvent(val type: String = "", val id: String = "")

/**
 * Push notifications (brief §6.14): each signed-in account registers its own
 * UnifiedPush endpoint (the account id is the UnifiedPush "instance") with
 * its server. A message triggers a sync and, for events worth it, a
 * notification. Without a distributor the app falls back to live hints
 * while open and the 15-minute sync, and tells about new books itself.
 */
@Singleton
class PushManager
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val accounts: AccountStore,
    private val apis: ApiFactory,
    private val engine: SyncEngine,
    @AppScope private val scope: CoroutineScope
) {
    private val store = context.pushStore
    private val json = Json { ignoreUnknownKeys = true }
    private val _status = MutableStateFlow<PushStatus>(PushStatus.Fallback)
    val status: StateFlow<PushStatus> = _status

    fun start() {
        createChannel()
        refresh()
        // A new or removed account (re)registers.
        scope.launch {
            accounts.state.map { s -> s.accounts.map { it.id }.toSet() }.distinctUntilChanged().collect { registerAll() }
        }
        // Fallback: the sync engine tells about books new in a library.
        scope.launch {
            engine.newBooks.collect { (accountId, books) ->
                if (_status.value !is PushStatus.Active) books.forEach { notifyNewBook(accountId, it) }
            }
        }
    }

    /** Re-reads which distributor is in use. */
    fun refresh() {
        val ack = UnifiedPush.getAckDistributor(context)
        _status.value =
            when {
                ack != null -> PushStatus.Active(label(ack))
                UnifiedPush.getDistributors(context).isNotEmpty() -> PushStatus.Choose
                else -> PushStatus.Fallback
            }
    }

    fun distributors(): List<String> = UnifiedPush.getDistributors(context)

    fun label(pkg: String): String = runCatching {
        val pm = context.packageManager
        pm.getApplicationLabel(pm.getApplicationInfo(pkg, 0)).toString()
    }.getOrDefault(pkg)

    /** Uses the chosen distributor (from the settings screen) and registers every account. */
    fun use(distributor: String) {
        UnifiedPush.saveDistributor(context, distributor)
        scope.launch { registerAll() }
        refresh()
    }

    /** Stops push: back to the fallback. */
    fun stop() = scope.launch {
        for (a in accounts.current().accounts) unregister(a)
        UnifiedPush.removeDistributor(context)
        refresh()
    }

    private suspend fun registerAll() {
        if (UnifiedPush.getAckDistributor(context) == null && UnifiedPush.getSavedDistributor(context) == null) {
            // One distributor installed: use it without asking.
            val all = UnifiedPush.getDistributors(context)
            if (all.size == 1) UnifiedPush.saveDistributor(context, all.first()) else return refresh()
        }
        for (a in accounts.current().accounts) runCatching { UnifiedPush.register(context, a.id, context.getString(UiR.string.app_name), null) }
        refresh()
    }

    /** Called when the account signs out (before it is forgotten). */
    suspend fun unregister(a: Account) {
        runCatching { UnifiedPush.unregister(context, a.id) }
        runCatching { apis.forAccount(a).me.unregisterPush(UUID.fromString(accounts.deviceId())) }
        store.edit { it.remove(endpointKey(a.id)) }
    }

    private fun endpointKey(accountId: String) = stringPreferencesKey("endpoint.$accountId")

    internal fun onNewEndpoint(accountId: String, url: String) = scope.launch {
        val a = accounts.current().accounts.firstOrNull { it.id == accountId } ?: return@launch
        if (store.data.first()[endpointKey(a.id)] == url) return@launch
        val r = call { apis.forAccount(a).me.registerPush(PushRegistration(UUID.fromString(accounts.deviceId()), url)) }
        if (r is Outcome.Ok) store.edit { it[endpointKey(a.id)] = url }
        refresh()
    }

    internal fun onUnregistered(accountId: String) = scope.launch {
        store.edit { it.remove(endpointKey(accountId)) }
        refresh()
    }

    internal fun onMessage(accountId: String, content: ByteArray) = scope.launch {
        val a = accounts.current().accounts.firstOrNull { it.id == accountId } ?: return@launch
        val e = runCatching { json.decodeFromString(PushEvent.serializer(), content.decodeToString()) }.getOrNull() ?: return@launch
        engine.syncSoon(a)
        when (e.type) {
            "library_added" -> {
                val id = runCatching { UUID.fromString(e.id) }.getOrNull()
                val title = id?.let { (call { apis.forAccount(a).library.getBook(it) } as? Outcome.Ok)?.value?.title }
                notify(a, context.getString(UiR.string.app_push_library_added), title, e.id.takeIf { id != null })
            }
            else -> eventText(e.type)?.let { notify(a, it, null, null) }
        }
    }

    private fun eventText(type: String): String? = when (type) {
        "upload_done" -> context.getString(UiR.string.app_push_upload_done)
        "upload_failed" -> context.getString(UiR.string.app_push_upload_failed)
        "signup_request" -> context.getString(UiR.string.app_push_signup_request)
        "report" -> context.getString(UiR.string.app_push_report)
        "review_request" -> context.getString(UiR.string.app_push_review_request)
        "backup_failed" -> context.getString(UiR.string.app_push_backup_failed)
        "integrity_failed" -> context.getString(UiR.string.app_push_integrity_failed)
        "unclean_shutdown" -> context.getString(UiR.string.app_push_unclean_shutdown)
        else -> null
    }

    private suspend fun notifyNewBook(accountId: String, b: BookSummary) {
        val a = accounts.current().accounts.firstOrNull { it.id == accountId } ?: return
        notify(a, context.getString(UiR.string.app_push_library_added), b.title, b.id.toString())
    }

    private fun createChannel() {
        context.getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, context.getString(UiR.string.app_push_channel), NotificationManager.IMPORTANCE_DEFAULT)
        )
    }

    private fun notify(a: Account, title: String, text: String?, bookId: String?) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            return
        }
        val intent =
            Intent(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_NEW_TASK)
                .putExtra(EXTRA_ACCOUNT, a.id)
                .apply { if (bookId != null) putExtra(EXTRA_BOOK, bookId) }
        val n =
            NotificationCompat
                .Builder(context, CHANNEL)
                .setSmallIcon(android.R.drawable.stat_notify_more)
                .setContentTitle(title)
                .setContentText(listOfNotNull(text, a.serverName).joinToString(" · "))
                .setAutoCancel(true)
                .setContentIntent(
                    PendingIntent.getActivity(
                        context,
                        (bookId ?: title).hashCode(),
                        intent,
                        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
                    )
                )
                .build()
        context.getSystemService(NotificationManager::class.java).notify((a.id + (bookId ?: title)).hashCode(), n)
    }

    companion object {
        const val CHANNEL = "updates"
        const val EXTRA_ACCOUNT = "org.simpleapb.push.ACCOUNT"
        const val EXTRA_BOOK = "org.simpleapb.push.BOOK"
    }
}

/** Receives UnifiedPush endpoints and messages from the distributor app. */
@AndroidEntryPoint
class PushReceiver : PushService() {
    @Inject lateinit var push: PushManager

    override fun onNewEndpoint(endpoint: PushEndpoint, instance: String) {
        push.onNewEndpoint(instance, endpoint.url)
    }

    override fun onMessage(message: PushMessage, instance: String) {
        push.onMessage(instance, message.content)
    }

    override fun onRegistrationFailed(reason: FailedReason, instance: String) {
        push.refresh()
    }

    override fun onUnregistered(instance: String) {
        push.onUnregistered(instance)
    }
}
