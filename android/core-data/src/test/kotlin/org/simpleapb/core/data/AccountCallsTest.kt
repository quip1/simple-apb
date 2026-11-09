// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.net.AccountCalls
import org.simpleapb.core.data.net.ServerUrl

class AccountCallsTest {
    private fun acc(id: String, url: String) = Account(id, url, "S", "u", "user", "User", "user")

    @Test
    fun mediaRequestsUseTheAccountOfTheirServer() {
        val root = acc("a", "https://books.example.org")
        val sub = acc("b", "https://books.example.org/family")
        val other = acc("c", "http://192.0.2.7:8080")
        val all = listOf(root, sub, other)
        assertEquals("b", AccountCalls.accountFor(all, "https://books.example.org/family/api/v1/books/x/files/y")?.id)
        assertEquals("a", AccountCalls.accountFor(all, "https://books.example.org/api/v1/books/x/cover?v=1")?.id)
        assertEquals("c", AccountCalls.accountFor(all, "http://192.0.2.7:8080/api/v1/books/x")?.id)
        // A look-alike host is not a prefix match.
        assertNull(AccountCalls.accountFor(all, "https://books.example.org.evil.test/api/v1/books/x"))
        assertNull(AccountCalls.accountFor(all, "https://elsewhere.test/a.png"))
    }

    @Test
    fun resolvesServerPaths() {
        assertEquals("https://h.test/sub/api/v1/books/1/cover?v=a", ServerUrl.resolve("https://h.test/sub", "/api/v1/books/1/cover?v=a"))
        assertEquals("https://cdn.test/x.jpg", ServerUrl.resolve("https://h.test", "https://cdn.test/x.jpg"))
    }
}
