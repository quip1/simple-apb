// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.auth

import java.io.IOException
import java.util.UUID
import org.simpleapb.api.model.DeviceInfo
import org.simpleapb.api.model.Instance
import org.simpleapb.api.model.LoginRequest
import org.simpleapb.api.model.LoginResponse
import org.simpleapb.api.model.Me
import org.simpleapb.api.model.Platform
import org.simpleapb.api.model.SignupRequest
import org.simpleapb.api.model.TotpVerifyRequest
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.Tokens
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.data.net.millis
import retrofit2.Response

/** A server the user typed in, checked. */
data class ServerInfo(
    val url: String,
    val instance: Instance,
    /** This app is older than the server's minimum for Android (brief §6.21). */
    val updateRequired: Boolean,
    val updateRecommended: Boolean
)

sealed interface Outcome<out T> {
    data class Ok<T>(val value: T) : Outcome<T>

    data class Failed(val failure: ApiFailure) : Outcome<Nothing>
}

sealed interface SignInResult {
    data class SignedIn(val account: Account, val me: Me) : SignInResult

    /** The password was right; a TOTP or recovery code is needed (valid 5 minutes). */
    data class NeedsCode(val challenge: String) : SignInResult

    data class Failed(val failure: ApiFailure) : SignInResult
}

/** Connecting, signing in and out (brief §6.6, §7.4 onboarding). */
class AuthRepository(private val apis: ApiFactory, private val store: AccountStore, private val app: AppInfo) {
    /** Checks that an address is a Simple APB server and reads its public info. */
    suspend fun probe(input: String): Outcome<ServerInfo> {
        val url = ServerUrl.normalize(input) ?: return Outcome.Failed(ApiFailure("invalid_url", "", 0))
        return call { apis.anonymous(url).instance.getInstance() }.map { inst ->
            val guard = inst.minClientVersion.android
            ServerInfo(
                url = url,
                instance = inst,
                updateRequired = guard != null && Versions.older(app.versionName, guard.required),
                updateRecommended = guard != null && Versions.older(app.versionName, guard.recommended)
            )
        }
    }

    suspend fun signIn(server: ServerInfo, username: String, password: String): SignInResult {
        val device = DeviceInfo(UUID.fromString(store.deviceId()), app.deviceName, Platform.ANDROID, app.versionName)
        val res = call { apis.anonymous(server.url).auth.login(LoginRequest(username.trim(), password, device)) }
        return finish(server, res)
    }

    suspend fun verifyCode(server: ServerInfo, challenge: String, code: String, recovery: Boolean): SignInResult {
        val body =
            if (recovery) {
                TotpVerifyRequest(challenge, recoveryCode = code.trim())
            } else {
                TotpVerifyRequest(challenge, code = code.filter { it.isDigit() })
            }
        return finish(server, call { apis.anonymous(server.url).auth.verifyTotp(body) })
    }

    suspend fun requestAccount(server: ServerInfo, request: SignupRequest): Outcome<Unit> =
        call { apis.anonymous(server.url).auth.signup(request) }.map { }

    /** Signs out on the server (best effort) and forgets the account here. */
    suspend fun signOut(account: Account) {
        runCatching { apis.forAccount(account).auth.logout() }
        apis.forget(account.id)
        store.remove(account.id)
    }

    private suspend fun finish(server: ServerInfo, res: Outcome<LoginResponse>): SignInResult {
        val r =
            when (res) {
                is Outcome.Failed -> return SignInResult.Failed(res.failure)
                is Outcome.Ok -> res.value
            }
        if (r.status == LoginResponse.Status.TOTP_REQUIRED) {
            return SignInResult.NeedsCode(r.totpChallenge.orEmpty())
        }
        val pair = r.tokens ?: return SignInResult.Failed(ApiFailure("internal", "no tokens", 500))
        val me = r.user ?: return SignInResult.Failed(ApiFailure("internal", "no user", 500))
        val tokens = Tokens(pair.accessToken, millis(pair.accessExpiresAt), pair.refreshToken, millis(pair.refreshExpiresAt))
        val account =
            store.add(
                Account(
                    id = UUID.randomUUID().toString(),
                    serverUrl = server.url,
                    serverName = server.instance.name,
                    userId = me.id.toString(),
                    username = me.username,
                    displayName = me.displayName,
                    role = me.role.value
                ),
                tokens
            )
        apis.forget(account.id)
        apis.remember(account.id, tokens)
        return SignInResult.SignedIn(account, me)
    }
}

/** Runs an API call, mapping HTTP errors and network failures to [ApiFailure]. */
suspend fun <T> call(block: suspend () -> Response<T>): Outcome<T> = try {
    val res = block()
    @Suppress("UNCHECKED_CAST")
    if (res.isSuccessful) Outcome.Ok(res.body() ?: Unit as T) else Outcome.Failed(ApiFailure.from(res))
} catch (_: IOException) {
    Outcome.Failed(ApiFailure.Offline)
} catch (e: kotlinx.serialization.SerializationException) {
    Outcome.Failed(ApiFailure("bad_response", e.message.orEmpty(), 0))
}

inline fun <T, R> Outcome<T>.map(f: (T) -> R): Outcome<R> = when (this) {
    is Outcome.Ok -> Outcome.Ok(f(value))
    is Outcome.Failed -> this
}

/** Semver comparison for the version guard ("1.2.3", pre-release ignored). */
object Versions {
    fun older(have: String, need: String): Boolean {
        val a = parts(have)
        val b = parts(need)
        for (i in 0 until 3) {
            if (a[i] != b[i]) return a[i] < b[i]
        }
        return false
    }

    private fun parts(v: String): List<Int> = v.substringBefore('-').substringBefore('+').split('.').map { it.toIntOrNull() ?: 0 }.let {
        it +
            List(3 - it.size.coerceAtMost(3)) { 0 }
    }
}
