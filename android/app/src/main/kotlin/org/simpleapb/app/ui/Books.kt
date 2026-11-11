// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Contrast
import androidx.compose.material.icons.filled.DownloadDone
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.SeriesRef
import org.simpleapb.app.player.Cover
import org.simpleapb.app.player.duration
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

/** The ids of the active account's finished downloads (badges on covers, brief §7.4). */
val LocalDownloaded = compositionLocalOf { emptySet<String>() }

/** A small "downloaded" mark for covers. */
@Composable
fun DownloadedBadge(modifier: Modifier = Modifier) {
    Icon(
        Icons.Filled.DownloadDone,
        contentDescription = null,
        tint = MaterialTheme.colorScheme.onSurface,
        modifier =
        modifier
            .background(MaterialTheme.colorScheme.surface, CircleShape)
            .border(1.dp, MaterialTheme.colorScheme.onSurface, CircleShape)
            .padding(3.dp)
            .size(16.dp)
    )
}

/** A cover address with a size the server has ready (128, 256, 512 or 1024 px). */
fun coverUrl(serverUrl: String, coverUrl: String, hasCover: Boolean, size: Int = 256): String? {
    if (!hasCover || coverUrl.isEmpty()) return null
    val abs = ServerUrl.resolve(serverUrl, coverUrl)
    return abs + (if ('?' in abs) "&" else "?") + "size=$size"
}

/** 0–100, rounded down so "100 %" only shows at the very end. */
fun percent(positionMs: Long, durationMs: Long): Int = if (durationMs <= 0) 0 else ((positionMs.coerceIn(0, durationMs) * 100) / durationMs).toInt()

val BookSummary.finished: Boolean get() = progress?.finishedAt != null

val BookSummary.startedPct: Int? get() = progress?.takeIf { !finished && it.positionMs > 0 }?.let { percent(it.positionMs, durationMs) }

@Composable
fun seriesLabel(s: SeriesRef): String = if (s.index.isNullOrBlank()) s.name else stringResource(UiR.string.book_series_position, s.name, s.index!!)

/** What a screen reader says for a book card or row. */
@Composable
private fun bookDescription(b: BookSummary): String {
    val parts = mutableListOf(b.title)
    if (b.authors.isNotEmpty()) parts += stringResource(UiR.string.app_book_by, b.authors.joinToString(", ") { it.name })
    b.startedPct?.let { parts += stringResource(UiR.string.book_progress_label, it.toString()) }
    if (b.finished) parts += stringResource(UiR.string.book_finished)
    if (b.id.toString() in LocalDownloaded.current) parts += stringResource(UiR.string.app_book_downloaded)
    return parts.joinToString(", ")
}

/** A screen title with an optional back button and actions on the right. */
@Composable
fun ScreenHeader(title: String, onBack: (() -> Unit)? = null, actions: @Composable () -> Unit = {}) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        if (onBack != null) {
            IconButton(onClick = onBack, modifier = Modifier.size(TouchTarget)) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(UiR.string.common_back))
            }
        }
        Text(
            title,
            style = MaterialTheme.typography.headlineSmall,
            modifier = Modifier.weight(1f).semantics { heading() }
        )
        actions()
        ScreenRefreshButton()
    }
}

/** Flashes the whole screen black, then white, to clear e-ink ghosting (provided by [AppRoot]). */
val LocalScreenRefresh = staticCompositionLocalOf<(() -> Unit)?> { null }

/** On e-ink: the "refresh the screen" button (brief §11). Elsewhere: nothing. */
@Composable
fun ScreenRefreshButton() {
    val refresh = LocalScreenRefresh.current ?: return
    if (!LocalEink.current) return
    IconButton(onClick = refresh, modifier = Modifier.size(TouchTarget)) {
        Icon(Icons.Filled.Contrast, contentDescription = stringResource(UiR.string.nav_refresh_screen))
    }
}

/** A shelf card's width: wider with large text (up to 1.6×, brief §11: 200 % text), so titles and buttons fit. */
@Composable
fun shelfCardWidth(eink: Boolean): Dp = (if (eink) 104.dp else 132.dp) * LocalDensity.current.fontScale.coerceIn(1f, 1.6f)

/** A section heading. */
@Composable
fun SectionTitle(text: String, modifier: Modifier = Modifier) {
    Text(text, style = MaterialTheme.typography.titleMedium, modifier = modifier.semantics { heading() })
}

/** Listening progress as a ring (static, so it is fine on e-ink). */
@Composable
fun ProgressRing(pct: Int, modifier: Modifier = Modifier) {
    // E-ink has no greys: an empty (white) track inside a black hairline instead.
    val eink = LocalEink.current
    val ring = if (eink) Modifier.border(1.dp, MaterialTheme.colorScheme.onSurface, CircleShape) else Modifier
    Box(modifier.size(36.dp).background(MaterialTheme.colorScheme.surface, CircleShape).then(ring).padding(3.dp)) {
        CircularProgressIndicator(
            progress = { pct / 100f },
            strokeWidth = 4.dp,
            trackColor = if (eink) MaterialTheme.colorScheme.surface else MaterialTheme.colorScheme.outlineVariant,
            gapSize = 0.dp
        )
    }
}

/** A cover with the title and author under it. */
@Composable
fun BookCard(book: BookSummary, serverUrl: String, onClick: () -> Unit, modifier: Modifier = Modifier, width: Dp = Dp.Unspecified) {
    val description = bookDescription(book)
    Column(
        modifier
            .then(if (width == Dp.Unspecified) Modifier.fillMaxWidth() else Modifier.width(width))
            .clickable(role = Role.Button, onClick = onClick)
            .clearAndSetSemantics {
                contentDescription = description
                role = Role.Button
            }.padding(bottom = 4.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        Box {
            Cover(coverUrl(serverUrl, book.coverUrl, book.hasCover), Modifier.fillMaxWidth())
            if (book.id.toString() in LocalDownloaded.current) DownloadedBadge(Modifier.align(Alignment.TopStart).padding(6.dp))
            val pct = book.startedPct
            if (pct != null) {
                ProgressRing(pct, Modifier.align(Alignment.BottomEnd).padding(6.dp))
            } else if (book.finished) {
                Icon(
                    Icons.Filled.CheckCircle,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary,
                    modifier =
                    Modifier
                        .align(Alignment.BottomEnd)
                        .padding(6.dp)
                        .background(MaterialTheme.colorScheme.surface, CircleShape)
                )
            }
        }
        Text(book.title, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
        if (book.authors.isNotEmpty()) {
            Text(
                book.authors.joinToString(", ") { it.name },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}

/** A book as a list row: small cover, title, people, series and state. */
@Composable
fun BookRow(book: BookSummary, serverUrl: String, onClick: () -> Unit) {
    val description = bookDescription(book)
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = TouchTarget)
            .clickable(role = Role.Button, onClick = onClick)
            .clearAndSetSemantics {
                contentDescription = description
                role = Role.Button
            }.padding(vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Box {
            Cover(coverUrl(serverUrl, book.coverUrl, book.hasCover, 128), Modifier.size(64.dp))
            if (book.id.toString() in LocalDownloaded.current) DownloadedBadge(Modifier.align(Alignment.TopStart).padding(2.dp))
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(book.title, style = MaterialTheme.typography.bodyLarge, maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (book.authors.isNotEmpty()) {
                Text(
                    book.authors.joinToString(", ") {
                        it.name
                    },
                    style = MaterialTheme.typography.bodySmall,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
            val s = book.series
            val meta = listOfNotNull(s?.let { seriesLabel(it) }, duration(book.durationMs)).joinToString(" · ")
            Text(
                meta,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
            val pct = book.startedPct
            when {
                pct != null -> Text(stringResource(UiR.string.book_progress_label, pct.toString()), style = MaterialTheme.typography.labelMedium)
                book.finished -> Text(stringResource(UiR.string.book_finished), style = MaterialTheme.typography.labelMedium)
            }
        }
    }
}

/**
 * A titled row of books. It scrolls sideways in the dark theme; on e-ink the
 * first six books sit in a fixed grid (no scrolling, brief §11).
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun Shelf(
    title: String,
    books: List<BookSummary>,
    serverUrl: String,
    onBook: (BookSummary) -> Unit,
    onSeeAll: (() -> Unit)? = null,
    below: @Composable (BookSummary) -> Unit = {}
) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            SectionTitle(title, Modifier.weight(1f))
            if (onSeeAll != null) {
                TextButton(onClick = onSeeAll, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.home_see_all)) }
            }
        }
        if (LocalEink.current) {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                books.take(6).forEach { b ->
                    Column(Modifier.width(shelfCardWidth(eink = true))) {
                        BookCard(b, serverUrl, { onBook(b) }, width = shelfCardWidth(eink = true))
                        below(b)
                    }
                }
            }
        } else {
            LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                items(books, key = { it.id }) { b ->
                    Column(Modifier.width(shelfCardWidth(eink = false))) {
                        BookCard(b, serverUrl, { onBook(b) }, width = shelfCardWidth(eink = false))
                        below(b)
                    }
                }
            }
        }
    }
}

/** Previous / next page buttons for the paged e-ink lists. */
@Composable
fun Pager(page: Int, hasPrevious: Boolean, hasNext: Boolean, onPrevious: () -> Unit, onNext: () -> Unit) {
    if (!hasPrevious && !hasNext) return
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        OutlinedButton(onClick = onPrevious, enabled = hasPrevious, modifier = Modifier.heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.common_previous_page))
        }
        Text(stringResource(UiR.string.common_page_n, page.toString()))
        OutlinedButton(onClick = onNext, enabled = hasNext, modifier = Modifier.heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.common_next_page))
        }
    }
}
