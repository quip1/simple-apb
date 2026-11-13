// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.home

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlin.math.roundToInt
import org.simpleapb.api.model.BookSummary
import org.simpleapb.app.player.Cover
import org.simpleapb.app.player.PlayerViewModel
import org.simpleapb.app.player.duration
import org.simpleapb.app.player.shownPosition
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.coverUrl
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

/**
 * The top of Home (ADR-0051): the book you're on, large, with its place and
 * one button to carry on. It's the book in the player (live), or else the
 * book you played last ([fallback]). Tapping the card opens the player.
 */
@Composable
fun NowListening(fallback: BookSummary?, serverUrl: String, onOpenPlayer: () -> Unit) {
    val vm: PlayerViewModel = hiltViewModel()
    val loaded by vm.book.collectAsStateWithLifecycle()
    val ui by vm.ui.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val b = loaded
    when {
        b != null -> {
            val pos = shownPosition(ui, settings.einkTickS)
            Hero(
                label = stringResource(UiR.string.home_now_listening),
                title = b.title,
                subtitle = b.timeline.chapterAt(pos)?.title ?: b.authors,
                cover = b.coverUrl,
                positionMs = pos,
                durationMs = b.timeline.durationMs,
                playing = ui.playing,
                onCard = onOpenPlayer,
                onButton = vm::togglePlay
            )
        }
        fallback != null -> {
            val pos = fallback.progress?.positionMs ?: 0
            Hero(
                label = stringResource(UiR.string.home_pick_up),
                title = fallback.title,
                subtitle = fallback.authors.joinToString(", ") { it.name },
                cover = coverUrl(serverUrl, fallback.coverUrl, fallback.hasCover, 512),
                positionMs = pos,
                durationMs = fallback.durationMs,
                playing = false,
                onCard = {
                    vm.play(fallback.id.toString())
                    onOpenPlayer()
                },
                onButton = { vm.play(fallback.id.toString()) }
            )
        }
    }
}

@Composable
private fun Hero(
    label: String,
    title: String,
    subtitle: String,
    cover: String?,
    positionMs: Long,
    durationMs: Long,
    playing: Boolean,
    onCard: () -> Unit,
    onButton: () -> Unit
) {
    val eink = LocalEink.current
    val total = durationMs.coerceAtLeast(1)
    val fraction = (positionMs.toFloat() / total).coerceIn(0f, 1f)
    Card(
        colors = CardDefaults.cardColors(),
        border = if (eink) BorderStroke(2.dp, MaterialTheme.colorScheme.outline) else null,
        modifier = Modifier.fillMaxWidth()
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(16.dp),
                verticalAlignment = Alignment.CenterVertically,
                modifier =
                Modifier
                    .fillMaxWidth()
                    .clickable(role = Role.Button, onClickLabel = stringResource(UiR.string.app_player_open), onClick = onCard)
            ) {
                Cover(cover, Modifier.width(112.dp))
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(label, style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
                    Text(title, style = MaterialTheme.typography.titleLarge, maxLines = 3, overflow = TextOverflow.Ellipsis)
                    Text(subtitle, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            LinearProgressIndicator(
                progress = { fraction },
                modifier = Modifier.fillMaxWidth(),
                trackColor = MaterialTheme.colorScheme.outlineVariant,
                drawStopIndicator = {}
            )
            Text(
                stringResource(UiR.string.player_book_left, (fraction * 100).roundToInt().toString(), duration(total - positionMs)),
                style = MaterialTheme.typography.bodySmall
            )
            Button(onClick = onButton, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Icon(if (playing) Icons.Filled.Pause else Icons.Filled.PlayArrow, contentDescription = null)
                Text(
                    stringResource(
                        if (playing) {
                            UiR.string.player_pause
                        } else if (positionMs > 0) {
                            UiR.string.book_resume
                        } else {
                            UiR.string.book_play
                        }
                    ),
                    modifier = Modifier.padding(start = 8.dp)
                )
            }
        }
    }
}
