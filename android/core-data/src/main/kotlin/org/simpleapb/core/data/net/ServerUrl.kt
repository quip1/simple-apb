// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.net

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Server address handling. No address is ever built in: the user types or scans it. */
object ServerUrl {
    /**
     * Normalizes what someone typed into a base URL: adds https:// when no
     * scheme is given, keeps a sub-path (servers behind a path prefix), and
     * drops the query, fragment, a trailing slash and a pasted "/api/v1".
     * Returns null when it isn't an http(s) address.
     */
    fun normalize(input: String): String? {
        var s = input.trim()
        if (s.isEmpty() || s.any { it.isWhitespace() }) return null
        if (!s.contains("://")) s = "https://$s"
        val url = s.toHttpUrlOrNull() ?: return null
        if (url.host.isEmpty()) return null
        var path = url.encodedPath.trimEnd('/')
        if (path.endsWith("/api/v1")) path = path.removeSuffix("/api/v1")
        val port = if (url.port == defaultPort(url.scheme)) "" else ":${url.port}"
        return "${url.scheme}://${url.host.let { if (it.contains(':')) "[$it]" else it }}$port$path"
    }

    /** The API base for a server ("…/api/v1/"), as Retrofit wants it. */
    fun api(base: String): String = "$base/api/v1/"

    /**
     * A URL the server returned as an absolute path ("/api/v1/books/…/cover")
     * resolved against the server address, keeping a sub-path.
     */
    fun resolve(base: String, path: String): String = when {
        path.startsWith("http://") || path.startsWith("https://") -> path
        path.startsWith("/") -> base + path
        else -> "$base/$path"
    }

    /** True for plain http (the UI warns: passwords would travel unencrypted). */
    fun isInsecure(base: String): Boolean = base.startsWith("http://")

    private fun defaultPort(scheme: String) = if (scheme == "https") 443 else 80
}
