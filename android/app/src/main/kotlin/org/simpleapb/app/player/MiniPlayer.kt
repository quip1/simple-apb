// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.player

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

/** The bar above the bottom edge while a book is loaded; tap it for the full player. */
@Composable
fun MiniPlayer(vm: PlayerViewModel, onOpen: () -> Unit) {
    val book by vm.book.collectAsStateWithLifecycle()
    val ui by vm.ui.collectAsStateWithLifecycle()
    val b = book ?: return
    val eink = LocalEink.current
    Surface(tonalElevation = if (eink) 0.dp else 3.dp, modifier = Modifier.fillMaxWidth()) {
        Column {
            if (eink) HorizontalDivider(thickness = 2.dp)
            LinearProgressIndicator(
                progress = { (ui.positionMs.toFloat() / b.timeline.durationMs.coerceAtLeast(1)).coerceIn(0f, 1f) },
                modifier = Modifier.fillMaxWidth()
            )
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(horizontal = 12.dp, vertical = 6.dp)) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier =
                    Modifier
                        .weight(1f)
                        .heightIn(min = TouchTarget)
                        .clickable(role = Role.Button, onClickLabel = stringResource(UiR.string.app_player_open), onClick = onOpen)
                        .semantics(mergeDescendants = true) {}
                ) {
                    Cover(b.coverUrl, Modifier.size(44.dp))
                    Column(Modifier.padding(start = 12.dp).weight(1f)) {
                        Text(b.title, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        val chapter = b.timeline.chapterAt(ui.positionMs)?.title ?: b.authors
                        Text(chapter, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
                IconButton(onClick = vm::togglePlay, modifier = Modifier.size(TouchTarget)) {
                    Icon(
                        if (ui.playing) Icons.Filled.Pause else Icons.Filled.PlayArrow,
                        contentDescription = stringResource(if (ui.playing) UiR.string.player_pause else UiR.string.player_play)
                    )
                }
            }
        }
    }
}
