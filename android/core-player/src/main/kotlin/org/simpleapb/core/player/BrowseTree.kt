// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.content.Context
import android.net.Uri
import android.os.Bundle
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import dagger.hilt.android.qualifiers.ApplicationContext
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.first
import org.simpleapb.api.endpoints.LibraryApi.KindListLibraryFacets
import org.simpleapb.api.endpoints.LibraryApi.ShelfListLibrary
import org.simpleapb.api.endpoints.LibraryApi.SortListLibrary
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.LibraryFacet
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.account.AccountStore
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.net.ApiFactory
import org.simpleapb.core.data.sync.LocalLibrary
import org.simpleapb.core.ui.R as UiR

/**
 * The Android Auto browse tree (brief §7.3): Continue listening · Recently
 * added · Downloaded · Collections · Series · Authors, for the active
 * account. Lists fall back to this device's copy when the server is out of
 * reach. Covers go through [CoverProvider], which the car can read.
 */
@Singleton
class BrowseTree
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val accounts: AccountStore,
    private val apis: ApiFactory,
    private val local: LocalLibrary,
    private val downloads: Downloads
) {
    fun root(): MediaItem = folder(ROOT, context.getString(UiR.string.app_name), MediaMetadata.MEDIA_TYPE_FOLDER_MIXED, grid = true)

    /** The top-level sections, or the books and folders under [parentId]. */
    suspend fun children(parentId: String): List<MediaItem> {
        val a = accounts.current().active ?: return emptyList()
        val api = apis.forAccount(a)
        return when {
            parentId == ROOT ->
                listOf(
                    folder(CONTINUE, context.getString(UiR.string.home_continue), MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS, grid = true),
                    folder(RECENT, context.getString(UiR.string.home_recently_added), MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS, grid = true),
                    folder(
                        DOWNLOADED,
                        context.getString(UiR.string.app_library_view_downloaded),
                        MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS,
                        grid = true
                    ),
                    folder(COLLECTIONS, context.getString(UiR.string.library_view_collections), MediaMetadata.MEDIA_TYPE_FOLDER_MIXED),
                    folder(SERIES, context.getString(UiR.string.library_view_series), MediaMetadata.MEDIA_TYPE_FOLDER_MIXED),
                    folder(AUTHORS, context.getString(UiR.string.library_view_authors), MediaMetadata.MEDIA_TYPE_FOLDER_MIXED)
                )
            parentId == CONTINUE -> shelf(a, ShelfListLibrary.IN_PROGRESS, SortListLibrary.RECENT_PLAYED)
            parentId == RECENT -> shelf(a, ShelfListLibrary.ALL, SortListLibrary.RECENT_ADDED)
            parentId == DOWNLOADED -> {
                val done = downloads.all.first().filter { it.accountId == a.id && it.state == DownloadState.DONE }
                done.mapNotNull { r -> local.book(a.id, r.bookId) }.map { book(a, it) }
            }
            parentId == COLLECTIONS ->
                (call { api.progress.listCollections() } as? Outcome.Ok)?.value.orEmpty().map {
                    folder(COLLECTION + it.id, it.name, MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS, grid = true)
                }
            parentId == SERIES -> facets(a, KindListLibraryFacets.SERIES, SERIES_ITEM)
            parentId == AUTHORS -> facets(a, KindListLibraryFacets.AUTHORS, AUTHOR_ITEM)
            parentId.startsWith(COLLECTION) -> uuid(parentId, COLLECTION)?.let { id ->
                (
                    call {
                        api.library.listLibrary(collection = id, sort = SortListLibrary.TITLE, limit = PAGE)
                    } as? Outcome.Ok
                    )?.value?.items.orEmpty().map { book(a, it) }
            }.orEmpty()
            parentId.startsWith(SERIES_ITEM) -> uuid(parentId, SERIES_ITEM)?.let { id ->
                (call { api.library.getSeries(id) } as? Outcome.Ok)?.value?.books.orEmpty().map { book(a, it) }
            }.orEmpty()
            parentId.startsWith(AUTHOR_ITEM) -> uuid(parentId, AUTHOR_ITEM)?.let { id ->
                (call { api.library.getAuthor(id) } as? Outcome.Ok)?.value?.books.orEmpty().map { book(a, it) }
            }.orEmpty()
            else -> emptyList()
        }
    }

    /** Voice and text search ("play The Hobbit"): the library's own search. */
    suspend fun search(query: String): List<MediaItem> {
        val a = accounts.current().active ?: return emptyList()
        val q = query.trim()
        if (q.isEmpty()) return emptyList()
        val online = call { apis.forAccount(a).library.listLibrary(q = q, sort = SortListLibrary.RECENT_PLAYED, limit = PAGE) }
        val books =
            (online as? Outcome.Ok)?.value?.items ?: local.books(a.id).filter { b ->
                b.title.contains(q, ignoreCase = true) ||
                    b.authors.any { it.name.contains(q, ignoreCase = true) } ||
                    b.series?.name?.contains(q, ignoreCase = true) == true
            }
        return books.map { book(a, it) }
    }

    private suspend fun shelf(a: Account, shelf: ShelfListLibrary, sort: SortListLibrary): List<MediaItem> {
        val r = call { apis.forAccount(a).library.listLibrary(shelf = shelf, sort = sort, limit = PAGE) }
        val books =
            (r as? Outcome.Ok)?.value?.items ?: run {
                // Offline: this device's copy.
                val progress = local.allProgress(a.id)
                val all = local.books(a.id).map { b -> progress[b.id.toString()]?.let { b.copy(progress = it) } ?: b }
                if (shelf == ShelfListLibrary.IN_PROGRESS) {
                    all.filter { it.progress?.finishedAt == null && (it.progress?.positionMs ?: 0) > 0 }.sortedByDescending { it.progress?.clientTs }
                } else {
                    all.sortedByDescending { it.addedAt ?: it.updatedAt }
                }
            }
        return books.map { book(a, it) }
    }

    private suspend fun facets(a: Account, kind: KindListLibraryFacets, prefix: String): List<MediaItem> =
        (call { apis.forAccount(a).library.listLibraryFacets(kind) } as? Outcome.Ok)?.value.orEmpty().map { f: LibraryFacet ->
            folder(prefix + f.id, f.name, MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS, grid = true)
        }

    private fun uuid(id: String, prefix: String) = runCatching { UUID.fromString(id.removePrefix(prefix)) }.getOrNull()

    private fun folder(id: String, title: String, type: Int, grid: Boolean = false): MediaItem = MediaItem
        .Builder()
        .setMediaId(id)
        .setMediaMetadata(
            MediaMetadata
                .Builder()
                .setTitle(title)
                .setIsBrowsable(true)
                .setIsPlayable(false)
                .setMediaType(type)
                .setExtras(Bundle().apply { putInt(CONTENT_STYLE_PLAYABLE, if (grid) STYLE_GRID else STYLE_LIST) })
                .build()
        ).build()

    /** A playable book, with its cover and how far the user is (the car shows a progress bar). */
    fun book(a: Account, b: BookSummary): MediaItem {
        val p = b.progress
        val extras =
            Bundle().apply {
                when {
                    p?.finishedAt != null -> putInt(PLAYBACK_STATUS, STATUS_FULLY_PLAYED)
                    p != null && p.positionMs > 0 -> {
                        putInt(PLAYBACK_STATUS, STATUS_PARTIALLY_PLAYED)
                        putDouble(COMPLETION, (p.positionMs.toDouble() / b.durationMs.coerceAtLeast(1)).coerceIn(0.0, 1.0))
                    }
                    else -> putInt(PLAYBACK_STATUS, STATUS_NOT_PLAYED)
                }
            }
        return MediaItem
            .Builder()
            .setMediaId(Commands.bookMediaId(b.id.toString()))
            .setMediaMetadata(
                MediaMetadata
                    .Builder()
                    .setTitle(b.title)
                    .setArtist(b.authors.joinToString(", ") { it.name })
                    .setArtworkUri(if (b.hasCover) CoverProvider.uri(context, a.id, b.id.toString()) else null)
                    .setIsBrowsable(false)
                    .setIsPlayable(true)
                    .setMediaType(MediaMetadata.MEDIA_TYPE_AUDIO_BOOK)
                    .setExtras(extras)
                    .build()
            ).build()
    }

    companion object {
        const val ROOT = "root"
        const val CONTINUE = "continue"
        const val RECENT = "recent"
        const val DOWNLOADED = "downloaded"
        const val COLLECTIONS = "collections"
        const val SERIES = "series"
        const val AUTHORS = "authors"
        const val COLLECTION = "collection:"
        const val SERIES_ITEM = "series:"
        const val AUTHOR_ITEM = "author:"
        private const val PAGE = 100

        // Android Auto's content-style and progress extras.
        const val CONTENT_STYLE_PLAYABLE = "android.media.browse.CONTENT_STYLE_PLAYABLE_HINT"
        const val STYLE_LIST = 1
        const val STYLE_GRID = 2
        const val PLAYBACK_STATUS = "android.media.extra.PLAYBACK_STATUS"
        const val STATUS_NOT_PLAYED = 0
        const val STATUS_PARTIALLY_PLAYED = 1
        const val STATUS_FULLY_PLAYED = 2
        const val COMPLETION = "androidx.media.MediaItem.Extras.COMPLETION_PERCENTAGE"

        /** Cover URIs in [items], to grant to the car (they're not exported). */
        fun artwork(items: List<MediaItem>): List<Uri> = items.mapNotNull { it.mediaMetadata.artworkUri }
    }
}
