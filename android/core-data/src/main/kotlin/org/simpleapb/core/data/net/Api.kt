// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.net

import java.io.IOException
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.Authenticator
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.Route
import org.simpleapb.api.endpoints.AuthApi
import org.simpleapb.api.endpoints.InstanceApi
import org.simpleapb.api.endpoints.LibraryApi
import org.simpleapb.api.endpoints.MeApi
import org.simpleapb.api.endpoints.MediaApi
import org.simpleapb.api.endpoints.ProgressApi
import org.simpleapb.api.endpoints.SocialApi
import org.simpleapb.api.endpoints.UploadsApi
import org.simpleapb.api.infrastructure.ApiClient
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.Tokens

/** Facts about this app build that requests carry. */
data class AppInfo(val versionName: String, val deviceName: String)

/** The API services for one server, signed in or not. */
class Api(client: ApiClient) {
    val auth: AuthApi = client.createService(AuthApi::class.java)
    val instance: InstanceApi = client.createService(InstanceApi::class.java)
    val me: MeApi = client.createService(MeApi::class.java)
    val library: LibraryApi = client.createService(LibraryApi::class.java)
    val progress: ProgressApi = client.createService(ProgressApi::class.java)
    val media: MediaApi = client.createService(MediaApi::class.java)
    val social: SocialApi = client.createService(SocialApi::class.java)
    val uploads: UploadsApi = client.createService(UploadsApi::class.java)
}

/** Something about a session the UI must react to. */
sealed interface SessionEvent {
    /** The server refused the refresh token: the account must sign in again. */
    data class SignedOut(val accountId: String) : SessionEvent
}

/**
 * Builds API clients. Signed-in clients add the access token to every
 * request and, when the server answers 401, rotate the token pair once with
 * the refresh token (brief §6.6) and retry. A refused refresh signs the
 * account out; a network failure during refresh does not.
 */
class ApiFactory(private val base: OkHttpClient, private val store: AccountStore, private val app: AppInfo) {
    private val cache = ConcurrentHashMap<String, Api>()
    private val tokenCache = ConcurrentHashMap<String, Tokens>()
    private val events = MutableSharedFlow<SessionEvent>(extraBufferCapacity = 4)
    val sessionEvents: SharedFlow<SessionEvent> = events

    private val userAgent = "SimpleAPB-Android/${app.versionName}"

    private val agent =
        Interceptor { chain ->
            chain.proceed(
                chain
                    .request()
                    .newBuilder()
                    .header("User-Agent", userAgent)
                    .build()
            )
        }

    /** A client for a server without an account (instance info, sign-in, sign-up). */
    fun anonymous(serverUrl: String): Api {
        val http = base.newBuilder().addInterceptor(agent).build()
        return Api(ApiClient(ServerUrl.api(serverUrl), http.newBuilder()))
    }

    /** A signed-in client for an account (cached per account). */
    fun forAccount(account: Account): Api = cache.getOrPut(account.id) {
        Api(ApiClient(ServerUrl.api(account.serverUrl), http(account).newBuilder()))
    }

    private val clients = ConcurrentHashMap<String, OkHttpClient>()

    /**
     * The signed-in HTTP client for an account: for audio files and covers,
     * which the server also serves on the access token (brief §6.9).
     */
    fun http(account: Account): OkHttpClient = clients.getOrPut(account.id) {
        base
            .newBuilder()
            .addInterceptor(agent)
            .addInterceptor(BearerInterceptor(account.id))
            .authenticator(Refresher(account))
            .build()
    }

    /** Drops cached clients and tokens (sign-out, account removed). */
    fun forget(accountId: String) {
        cache.remove(accountId)
        clients.remove(accountId)
        tokenCache.remove(accountId)
    }

    /** Remembers freshly issued tokens (after sign-in) without a disk read. */
    fun remember(accountId: String, tokens: Tokens) {
        tokenCache[accountId] = tokens
    }

    private fun tokens(accountId: String): Tokens? =
        tokenCache[accountId] ?: runBlocking { store.tokens(accountId) }?.also { tokenCache[accountId] = it }

    private inner class BearerInterceptor(private val accountId: String) : Interceptor {
        override fun intercept(chain: Interceptor.Chain): Response {
            val req = chain.request()
            val t = tokens(accountId) ?: return chain.proceed(req)
            return chain.proceed(req.newBuilder().header("Authorization", "Bearer ${t.access}").build())
        }
    }

    private inner class Refresher(private val account: Account) : Authenticator {
        private val lock = Any()

        override fun authenticate(route: Route?, response: Response): Request? {
            if (response.priorResponse != null) return null // already retried once
            val used = response.request.header("Authorization")?.removePrefix("Bearer ") ?: return null
            synchronized(lock) {
                val current = tokens(account.id) ?: return null
                // Another request refreshed while we waited: just retry with the new token.
                if (current.access != used) return retry(response.request, current.access)
                val fresh =
                    when (val r = refresh(current.refresh)) {
                        is RefreshResult.Ok -> r.tokens
                        RefreshResult.Refused -> {
                            events.tryEmit(SessionEvent.SignedOut(account.id))
                            return null
                        }
                        RefreshResult.Offline -> return null
                    }
                tokenCache[account.id] = fresh
                runBlocking { store.saveTokens(account.id, fresh) }
                return retry(response.request, fresh.access)
            }
        }

        private fun retry(req: Request, access: String) = req.newBuilder().header("Authorization", "Bearer $access").build()

        private fun refresh(refreshToken: String): RefreshResult {
            val body = json.encodeToString(RefreshBody.serializer(), RefreshBody(refreshToken)).toRequestBody(jsonType)
            val req =
                Request
                    .Builder()
                    .url(
                        ServerUrl.api(account.serverUrl) + "auth/refresh"
                    ).post(body)
                    .header("User-Agent", userAgent)
                    .build()
            return try {
                base.newBuilder().callTimeout(30, TimeUnit.SECONDS).build().newCall(req).execute().use { res ->
                    when {
                        res.isSuccessful ->
                            RefreshResult.Ok(
                                json.decodeFromString(TokenPairBody.serializer(), res.body.string()).toTokens()
                            )
                        res.code == 401 || res.code == 403 -> RefreshResult.Refused
                        else -> RefreshResult.Offline
                    }
                }
            } catch (_: IOException) {
                RefreshResult.Offline
            }
        }
    }

    private sealed interface RefreshResult {
        data class Ok(val tokens: Tokens) : RefreshResult

        data object Refused : RefreshResult

        data object Offline : RefreshResult
    }

    @Serializable
    private data class RefreshBody(@kotlinx.serialization.SerialName("refresh_token") val refreshToken: String)

    @Serializable
    private data class TokenPairBody(
        @kotlinx.serialization.SerialName("access_token") val accessToken: String,
        @kotlinx.serialization.SerialName("access_expires_at") val accessExpiresAt: String,
        @kotlinx.serialization.SerialName("refresh_token") val refreshToken: String,
        @kotlinx.serialization.SerialName("refresh_expires_at") val refreshExpiresAt: String
    ) {
        fun toTokens() = Tokens(accessToken, millis(accessExpiresAt), refreshToken, millis(refreshExpiresAt))
    }

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
        val jsonType = "application/json".toMediaType()
    }
}

/** RFC 3339 → epoch ms (0 if unparseable). */
fun millis(rfc3339: String): Long = runCatching { Instant.parse(rfc3339).toEpochMilli() }.getOrDefault(0)

/** A failed API call: the server's stable error code, or "offline". */
data class ApiFailure(val code: String, val message: String, val status: Int) {
    companion object {
        private val json = Json { ignoreUnknownKeys = true }

        @Serializable
        private data class Body(val code: String = "", val message: String = "")

        fun from(res: retrofit2.Response<*>): ApiFailure {
            val raw = runCatching { res.errorBody()?.string() }.getOrNull().orEmpty()
            val b = runCatching { json.decodeFromString(Body.serializer(), raw) }.getOrNull()
            return ApiFailure(b?.code?.ifEmpty { null } ?: "http_${res.code()}", b?.message.orEmpty(), res.code())
        }

        val Offline = ApiFailure("offline", "", 0)
    }
}
