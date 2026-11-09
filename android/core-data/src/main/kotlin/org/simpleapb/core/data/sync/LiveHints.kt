// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.sync

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppScope

/**
 * Live sync hints (brief §6.8): while the app is in the foreground, a
 * WebSocket to the active account's `/ws` says "something changed", and the
 * engine syncs at once. The same happens when the socket (re)connects, so
 * nothing is missed while it was down. Hints right after this device's own
 * writes are echoes and are dropped; bursts are coalesced.
 */
@Singleton
class LiveHints
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val accounts: AccountStore,
    private val apis: ApiFactory,
    private val engine: SyncEngine,
    @AppScope private val scope: CoroutineScope
) {
    private val foreground = MutableStateFlow(false)
    private val _connected = MutableStateFlow(false)

    /** The live socket is open (the app is up to date within a second or so). */
    val connected: StateFlow<Boolean> = _connected

    private var hint: Job? = null

    // A network came back: stop waiting out the backoff and reconnect now.
    private val networkBack = MutableSharedFlow<Unit>(extraBufferCapacity = 1)

    fun start() {
        context.getSystemService(ConnectivityManager::class.java)?.registerDefaultNetworkCallback(
            object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) {
                    networkBack.tryEmit(Unit)
                }
            }
        )
        scope.launch(Dispatchers.Main) {
            ProcessLifecycleOwner.get().lifecycle.addObserver(
                object : DefaultLifecycleObserver {
                    override fun onStart(owner: LifecycleOwner) {
                        foreground.value = true
                    }

                    override fun onStop(owner: LifecycleOwner) {
                        foreground.value = false
                    }
                }
            )
        }
        scope.launch {
            combine(accounts.state.map { it.active }.distinctUntilChanged { a, b -> a?.id == b?.id }, foreground) { a, fg -> if (fg) a else null }
                .distinctUntilChanged { a, b -> a?.id == b?.id }
                .collectLatest { a -> if (a != null) keepConnected(a) }
        }
    }

    /** Connects, and reconnects with backoff, until cancelled (background or account switch). */
    private suspend fun keepConnected(account: Account) {
        var retry = 0
        try {
            while (true) {
                val closed = kotlinx.coroutines.CompletableDeferred<Unit>()
                val ws = open(account, onOpen = { retry = 0 }, onClosed = { closed.complete(Unit) })
                try {
                    closed.await()
                } finally {
                    ws.cancel()
                    _connected.value = false
                }
                withTimeoutOrNull(minOf(MAX_BACKOFF_MS, 1_000L shl minOf(retry++, 5))) { networkBack.first() }
            }
        } finally {
            _connected.value = false
        }
    }

    private suspend fun open(account: Account, onOpen: () -> Unit, onClosed: () -> Unit): WebSocket = withContext(Dispatchers.IO) {
        val url = wsUrl(account.serverUrl)
        // The account's own client: it adds the token and refreshes it on a 401.
        apis.http(account).newWebSocket(
            Request.Builder().url(url).build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    onOpen()
                    _connected.value = true
                    engine.syncSoon(account) // catch up on anything missed while closed
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    if (!text.contains("\"changed\"")) return
                    if (engine.isEcho()) return
                    hint?.cancel()
                    hint =
                        scope.launch {
                            delay(COALESCE_MS)
                            engine.syncSoon(account)
                        }
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = onClosed()

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = onClosed()
            }
        )
    }

    companion object {
        private const val COALESCE_MS = 300L
        private const val MAX_BACKOFF_MS = 30_000L

        /** `https://host/sub` → `wss://host/sub/ws` (OkHttp takes ws(s) URLs as http(s)). */
        fun wsUrl(serverUrl: String): String = serverUrl.trimEnd('/') + "/ws"
    }
}
