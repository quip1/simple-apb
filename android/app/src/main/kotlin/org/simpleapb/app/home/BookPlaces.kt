// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.home

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlin.math.roundToInt
import org.simpleapb.api.model.BookSummary
import org.simpleapb.api.model.Progress
import org.simpleapb.app.book.ago
import org.simpleapb.app.player.Cover
import org.simpleapb.app.player.clock
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.coverUrl
import org.simpleapb.core.data.net.millis
import org.simpleapb.core.ui.R as UiR

/**
 * Lays this device's unsent places over books from the server, where they
 * are newer (ADR-0056): the list shows the place the player will start from.
 * Returns the books and the ids whose place is still waiting to be sent.
 */
fun withUnsent(books: List<BookSummary>, unsent: Map<String, Progress>): Pair<List<BookSummary>, Set<String>> {
    val waiting = mutableSetOf<String>()
    val out =
        books.map { b ->
            val mine = unsent[b.id.toString()] ?: return@map b
            val theirs = b.progress
            if (theirs != null && millis(theirs.clientTs) >= millis(mine.clientTs)) return@map b
            waiting += b.id.toString()
            b.copy(progress = mine.copy(deviceName = null))
        }
    return out to waiting
}

/**
 * Books with the place each one is at and a button to carry on (ADR-0056):
 * Home's "your other books" and the Your places screen ([details] adds who
 * saved the place and when).
 */
@Composable
fun BookPlaces(
    books: List<BookSummary>,
    unsent: Set<String>,
    serverUrl: String,
    details: Boolean,
    onOpen: (BookSummary) -> Unit,
    onPlay: (BookSummary) -> Unit
) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        for (b in books) BookPlace(b, b.id.toString() in unsent, serverUrl, details, onOpen, onPlay)
    }
}

@Composable
private fun BookPlace(
    b: BookSummary,
    unsent: Boolean,
    serverUrl: String,
    details: Boolean,
    onOpen: (BookSummary) -> Unit,
    onPlay: (BookSummary) -> Unit
) {
    val p = b.progress
    val finished = p?.finishedAt != null
    val pos = p?.positionMs ?: 0L
    val total = b.durationMs.coerceAtLeast(1)
    val fraction = if (finished) 1f else (pos.toFloat() / total).coerceIn(0f, 1f)
    OutlinedCard(Modifier.fillMaxWidth()) {
        Row(
            Modifier.padding(8.dp).fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Row(
                Modifier
                    .weight(1f)
                    .clickable(role = Role.Button, onClickLabel = b.title) { onOpen(b) },
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Cover(coverUrl(serverUrl, b.coverUrl, b.hasCover, 256), Modifier.width(64.dp))
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(b.title, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text(
                        b.authors.joinToString(", ") { it.name },
                        style = MaterialTheme.typography.bodySmall,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                    LinearProgressIndicator(
                        progress = { fraction },
                        modifier = Modifier.fillMaxWidth(),
                        trackColor = MaterialTheme.colorScheme.outlineVariant,
                        drawStopIndicator = {}
                    )
                    val at =
                        if (finished) {
                            stringResource(UiR.string.sync_finished)
                        } else {
                            stringResource(UiR.string.places_position, clock(pos), clock(b.durationMs))
                        }
                    Text("$at · ${(fraction * 100).roundToInt()}%", style = MaterialTheme.typography.bodySmall)
                    if (details) {
                        when {
                            unsent -> Text(
                                stringResource(UiR.string.places_unsent),
                                style = MaterialTheme.typography.bodySmall,
                                fontStyle = FontStyle.Italic
                            )
                            p != null && p.deviceName != null ->
                                Text(
                                    stringResource(UiR.string.places_saved_on, p.deviceName!!, ago(p.updatedAt)),
                                    style = MaterialTheme.typography.bodySmall
                                )
                            p != null -> Text(stringResource(UiR.string.places_saved, ago(p.updatedAt)), style = MaterialTheme.typography.bodySmall)
                        }
                    }
                }
            }
            FilledIconButton(onClick = { onPlay(b) }, modifier = Modifier.heightIn(min = TouchTarget).width(TouchTarget)) {
                Icon(Icons.Filled.PlayArrow, contentDescription = stringResource(UiR.string.places_resume, b.title))
            }
        }
    }
}
