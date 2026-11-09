// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import java.io.File
import java.io.FileNotFoundException
import java.io.FileOutputStream
import kotlinx.coroutines.runBlocking
import okhttp3.Request
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.download.DownloadWorker
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.data.sync.LocalLibrary

/**
 * Book covers as `content://` URIs for Android Auto, whose host can't fetch
 * the server's signed-in addresses. Not exported: the service grants each
 * cover to the car host it lists it for. Covers come from the download's
 * folder, a small cache, or the server (fetched signed in, then cached).
 */
class CoverProvider : ContentProvider() {
    @EntryPoint
    @InstallIn(SingletonComponent::class)
    interface Deps {
        fun accounts(): AccountStore

        fun apis(): ApiFactory

        fun local(): LocalLibrary

        fun downloads(): Downloads
    }

    override fun onCreate(): Boolean = true

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw SecurityException("read only")
        val (accountId, bookId) = parse(uri) ?: throw FileNotFoundException(uri.toString())
        val ctx = context ?: throw FileNotFoundException(uri.toString())
        val file = runBlocking { find(ctx, accountId, bookId) } ?: throw FileNotFoundException(uri.toString())
        return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
    }

    private suspend fun find(ctx: Context, accountId: String, bookId: String): File? {
        val deps = EntryPointAccessors.fromApplication(ctx, Deps::class.java)
        deps.downloads().completeDir(accountId, bookId)?.let { File(it, DownloadWorker.COVER) }?.takeIf { it.isFile }?.let { return it }
        val cached = File(File(ctx.cacheDir, "covers"), "$accountId-$bookId.jpg")
        if (cached.isFile) return cached
        val account = deps.accounts().current().accounts.firstOrNull { it.id == accountId } ?: return null
        val book = deps.local().book(accountId, bookId) ?: return null
        if (!book.hasCover) return null
        val url = ServerUrl.resolve(account.serverUrl, book.coverUrl).let { it + (if ('?' in it) "&" else "?") + "size=512" }
        return runCatching {
            deps.apis().http(account).newCall(Request.Builder().url(url).build()).execute().use { res ->
                if (!res.isSuccessful) return null
                cached.parentFile?.mkdirs()
                val tmp = File(cached.path + ".tmp")
                FileOutputStream(tmp).use { out ->
                    res.body.byteStream().copyTo(out)
                    out.fd.sync()
                }
                if (tmp.renameTo(cached)) cached else null
            }
        }.getOrNull()
    }

    override fun getType(uri: Uri): String = "image/jpeg"

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? =
        null

    override fun insert(uri: Uri, values: ContentValues?): Uri? = null

    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0

    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

    companion object {
        fun authority(context: Context) = "${context.packageName}.covers"

        fun uri(context: Context, accountId: String, bookId: String): Uri = Uri
            .Builder()
            .scheme("content")
            .authority(authority(context))
            .appendPath("cover")
            .appendPath(accountId)
            .appendPath(bookId)
            .build()

        /** (account id, book id) from a cover URI. */
        fun parse(uri: Uri): Pair<String, String>? {
            val p = uri.pathSegments
            if (p.size != 3 || p[0] != "cover") return null
            if (!p[1].all { it.isLetterOrDigit() || it == '-' } || !p[2].all { it.isLetterOrDigit() || it == '-' }) return null
            return p[1] to p[2]
        }
    }
}
