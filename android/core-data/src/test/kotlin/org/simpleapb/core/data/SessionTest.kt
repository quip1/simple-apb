// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.account.SecretBox
import org.simpleapb.core.data.account.Tokens
import org.simpleapb.core.data.auth.AuthRepository
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.SignInResult
import org.simpleapb.core.data.auth.Versions
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.data.net.SessionEvent

/** Reversible stand-in for the Keystore (Robolectric has no AndroidKeyStore). */
private class XorBox : SecretBox {
    override fun seal(plain: ByteArray) = plain.map { (it.toInt() xor 0x5a).toByte() }.toByteArray()

    override fun open(sealed: ByteArray) = seal(sealed)
}

@RunWith(RobolectricTestRunner::class)
class SessionTest {
    @get:Rule val tmp = TemporaryFolder()

    private lateinit var server: MockWebServer
    private lateinit var store: AccountStore
    private lateinit var apis: ApiFactory
    private val app = AppInfo("0.3.0", "Test phone")

    private val instanceJson =
        """{"name":"Test shelf","version":"1.0.0","api_version":"v1","signup_open":true,"setup_required":false,
        "oidc_providers":[],"min_client_version":{"android":{"required":"0.2.0","recommended":"0.4.0"}}}"""

    private fun tokens(n: Int) =
        """{"access_token":"access-$n","access_expires_at":"2030-01-01T00:00:00Z","refresh_token":"refresh-$n","refresh_expires_at":"2030-02-01T00:00:00Z"}"""

    private val meJson =
        """{"id":"0190c3b4-0000-7000-8000-000000000009","username":"sam","display_name":"Sam","role":"user","status":"active",
        "totp_enabled":false,"totp_required":false,"quota_bytes":0,"used_bytes":0,"locale":"en",
        "permissions":{"can_upload":true,"can_edit_metadata":true,"can_delete_own":true,"can_share":false,"can_review":true,"can_post_public":false},
        "conflict_threshold_minutes":10,"listening_visible":true,"prefs":{}}"""

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val ds =
            PreferenceDataStoreFactory.create(
                scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
            ) { tmp.newFile("a.preferences_pb") }
        store = AccountStore(ds, XorBox())
        apis = ApiFactory(OkHttpClient(), store, app)
    }

    @After
    fun tearDown() = server.close()

    private fun base() = server.url("/").toString().trimEnd('/')

    private fun json(body: String, code: Int = 200) = MockResponse
        .Builder()
        .code(code)
        .addHeader("Content-Type", "application/json")
        .body(body)
        .build()

    @Test
    fun normalizesServerAddresses() {
        assertEquals("https://books.example.org", ServerUrl.normalize(" books.example.org/ "))
        assertEquals("https://books.example.org/sub", ServerUrl.normalize("https://books.example.org/sub/api/v1"))
        assertEquals("http://192.0.2.4:8080", ServerUrl.normalize("http://192.0.2.4:8080/?x=1#y"))
        assertEquals("https://books.example.org", ServerUrl.normalize("https://books.example.org:443"))
        assertNull(ServerUrl.normalize("ftp://books.example.org"))
        assertNull(ServerUrl.normalize("not a url"))
        assertNull(ServerUrl.normalize(""))
        assertTrue(ServerUrl.isInsecure("http://x"))
        assertFalse(ServerUrl.isInsecure("https://x"))
    }

    @Test
    fun comparesVersions() {
        assertTrue(Versions.older("0.3.0", "0.4.0"))
        assertTrue(Versions.older("0.3.9", "1.0"))
        assertFalse(Versions.older("1.2.0", "1.2.0"))
        assertFalse(Versions.older("1.10.0", "1.9.9"))
        assertFalse(Versions.older("2.0.0-beta", "1.0.0"))
    }

    @Test
    fun signsInWithTotpAndRemembersTheAccount() = runBlocking {
        val auth = AuthRepository(apis, store, app)
        server.enqueue(json(instanceJson))
        val info = (auth.probe(base()) as Outcome.Ok).value
        assertEquals("Test shelf", info.instance.name)
        assertFalse(info.updateRequired)
        assertTrue(info.updateRecommended)

        server.enqueue(json("""{"status":"totp_required","totp_challenge":"ch-1"}"""))
        val step = auth.signIn(info, " sam ", "pw") as SignInResult.NeedsCode
        assertEquals("ch-1", step.challenge)
        assertEquals("/api/v1/instance", server.takeRequest().url.encodedPath)
        val login = server.takeRequest()
        assertEquals("/api/v1/auth/login", login.url.encodedPath)
        assertTrue(login.body!!.utf8().contains("\"username\":\"sam\""))
        assertTrue(login.body!!.utf8().contains("\"platform\":\"android\""))

        server.enqueue(json("""{"status":"ok","tokens":${tokens(1)},"user":$meJson}"""))
        val done = auth.verifyCode(info, "ch-1", "123 456", recovery = false) as SignInResult.SignedIn
        assertTrue(
            server
                .takeRequest()
                .body!!
                .utf8()
                .contains("\"code\":\"123456\"")
        )
        val state = store.state.first()
        assertEquals(done.account.id, state.activeId)
        assertEquals("access-1", store.tokens(done.account.id)?.access)
    }

    @Test
    fun wrongPasswordIsReported() = runBlocking {
        val auth = AuthRepository(apis, store, app)
        server.enqueue(json(instanceJson))
        val info = (auth.probe(base()) as Outcome.Ok).value
        server.enqueue(json("""{"code":"invalid_credentials","message":"nope"}""", 401))
        val r = auth.signIn(info, "sam", "bad") as SignInResult.Failed
        assertEquals("invalid_credentials", r.failure.code)
        assertNull(store.state.first().active)
    }

    @Test
    fun refreshesAnExpiredAccessTokenOnce() = runBlocking {
        val acc = store.add(Account("a1", base(), "Test", "u1", "sam", "Sam", "user"), Tokens("old", 0, "refresh-0", 0))
        server.enqueue(json("""{"code":"invalid_token","message":"expired"}""", 401))
        server.enqueue(json(tokens(1)))
        server.enqueue(json(meJson))
        val r = call { apis.forAccount(acc).me.getMe() }
        assertTrue(r is Outcome.Ok)
        assertEquals("Bearer old", server.takeRequest().headers["Authorization"])
        val refresh = server.takeRequest()
        assertEquals("/api/v1/auth/refresh", refresh.url.encodedPath)
        assertTrue(refresh.body!!.utf8().contains("refresh-0"))
        assertEquals("Bearer access-1", server.takeRequest().headers["Authorization"])
        assertEquals("refresh-1", store.tokens(acc.id)?.refresh)
    }

    @Test
    fun refusedRefreshSignsOutButOfflineDoesNot() = runBlocking {
        val acc = store.add(Account("a1", base(), "Test", "u1", "sam", "Sam", "user"), Tokens("old", 0, "refresh-0", 0))
        server.enqueue(json("""{"code":"invalid_token"}""", 401))
        server.enqueue(json("""{"code":"invalid_token"}""", 503)) // refresh endpoint unavailable
        assertTrue(call { apis.forAccount(acc).me.getMe() } is Outcome.Failed)
        assertNotNull("tokens kept while the server is unavailable", store.tokens(acc.id))

        server.enqueue(json("""{"code":"invalid_token"}""", 401))
        server.enqueue(json("""{"code":"invalid_token"}""", 401)) // refresh refused
        val events = apis.sessionEvents
        val got = async(Dispatchers.Default) { withTimeout(5000) { events.first() } }
        delay(100)
        assertTrue(call { apis.forAccount(acc).me.getMe() } is Outcome.Failed)
        assertEquals(SessionEvent.SignedOut(acc.id), got.await())
    }
}
