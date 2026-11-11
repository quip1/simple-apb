// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.player

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.BookmarkAdd
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.Hyphens
import androidx.compose.ui.text.style.LineBreak
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlin.math.roundToInt
import org.simpleapb.app.settings.quietTicks
import org.simpleapb.app.ui.AppAlertDialog
import org.simpleapb.app.ui.SwitchRow
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.player.EffectsConfig
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

/**
 * Car mode (brief §7.2): five huge buttons, the title and the chapter, and
 * the screen kept on. Opened from the player, or by itself when a car's
 * Bluetooth connects (optional).
 */
@Composable
fun CarModeScreen(vm: PlayerViewModel, onClose: () -> Unit) {
    val book by vm.book.collectAsStateWithLifecycle()
    val ui by vm.ui.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val view = LocalView.current
    DisposableEffect(Unit) {
        view.keepScreenOn = true
        onDispose { view.keepScreenOn = false }
    }
    val b = book
    val pos = shownPosition(ui, settings.einkTickS)
    Column(
        Modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            OutlinedButton(onClick = onClose, modifier = Modifier.heightIn(min = TouchTarget)) {
                Icon(Icons.Filled.Close, contentDescription = null)
                Text(stringResource(UiR.string.app_car_leave), modifier = Modifier.padding(start = 8.dp))
            }
        }
        if (b == null) {
            Text(stringResource(UiR.string.player_nothing_playing), style = MaterialTheme.typography.headlineSmall)
            BigButton(
                Icons.Filled.PlayArrow,
                stringResource(UiR.string.app_car_resume),
                Modifier.weight(1f),
                vm::playOrResume,
                iconSize = 96.dp,
                label = true
            )
            return@Column
        }
        val chapter = b.timeline.chapterAt(pos)
        Column(Modifier.semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Polite }) {
            Text(b.title, style = MaterialTheme.typography.headlineMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            chapter?.let {
                Text(it.title, style = MaterialTheme.typography.titleLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    stringResource(UiR.string.app_car_chapter_left, clock((b.timeline.chapterEnd(pos) - pos).coerceAtLeast(0))),
                    style = MaterialTheme.typography.titleMedium
                )
            }
        }
        // Back · play/pause · forward, as big as the screen allows.
        Row(Modifier.fillMaxWidth().weight(1f), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            BigButton(
                replayIcon(settings.skipBackS),
                stringResource(UiR.string.player_skip_back, settings.skipBackS.toString()),
                Modifier.weight(1f),
                vm::skipBack
            )
            BigButton(
                if (ui.playing) Icons.Filled.Pause else Icons.Filled.PlayArrow,
                stringResource(if (ui.playing) UiR.string.player_pause else UiR.string.player_play),
                Modifier.weight(1.3f),
                vm::togglePlay,
                iconSize = 96.dp
            )
            BigButton(
                forwardIcon(settings.skipForwardS),
                stringResource(UiR.string.player_skip_forward, settings.skipForwardS.toString()),
                Modifier.weight(1f),
                vm::skipForward
            )
        }
        Row(Modifier.fillMaxWidth().weight(0.7f), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            BigButton(Icons.Filled.BookmarkAdd, stringResource(UiR.string.app_player_bookmark_add), Modifier.weight(1f), {
                vm.addBookmark(null)
            }, label = true)
            BigButton(Icons.Filled.SkipNext, stringResource(UiR.string.player_next_chapter), Modifier.weight(1f), vm::nextChapter, label = true)
        }
    }
}

@Composable
private fun BigButton(icon: ImageVector, description: String, modifier: Modifier, onClick: () -> Unit, iconSize: Dp = 72.dp, label: Boolean = false) {
    FilledTonalButton(
        onClick = onClick,
        shape = RoundedCornerShape(24.dp),
        contentPadding = PaddingValues(8.dp),
        // E-ink: the tonal fill is white, so an outline shows where each button is.
        border = if (LocalEink.current) BorderStroke(2.dp, MaterialTheme.colorScheme.onSurface) else null,
        modifier = modifier.fillMaxSize().semantics { contentDescription = description }
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Icon(icon, contentDescription = null, modifier = Modifier.size(iconSize))
            // Words wrap whole, also at 200 % text size.
            if (label) {
                Text(
                    description,
                    fontSize = 18.sp,
                    fontWeight = FontWeight.Bold,
                    textAlign = TextAlign.Center,
                    style = LocalTextStyle.current.copy(hyphens = Hyphens.None, lineBreak = LineBreak.Paragraph)
                )
            }
        }
    }
}

/** Boost, voice clarity, mono and balance (brief §7.2), changed live. */
@Composable
fun AudioDialog(cfg: EffectsConfig, onChange: (EffectsConfig) -> Unit, onClose: () -> Unit) {
    AppAlertDialog(
        onDismissRequest = onClose,
        title = { Text(stringResource(UiR.string.app_audio_title)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                val boost = stringResource(UiR.string.app_audio_db, cfg.boostDb.toString())
                Text(stringResource(UiR.string.app_audio_boost) + ": " + boost)
                Slider(
                    value = cfg.boostDb.toFloat(),
                    onValueChange = { onChange(cfg.copy(boostDb = it.roundToInt())) },
                    valueRange = 0f..12f,
                    steps = 11,
                    colors = quietTicks(),
                    modifier = Modifier.semantics { stateDescription = boost }
                )
                Text(stringResource(UiR.string.app_audio_boost_help), style = MaterialTheme.typography.bodySmall)
                SwitchRow(stringResource(UiR.string.app_audio_voice), cfg.voiceEq, {
                    onChange(cfg.copy(voiceEq = it))
                }, help = stringResource(UiR.string.app_audio_voice_help))
                SwitchRow(stringResource(UiR.string.app_audio_mono), cfg.mono, { onChange(cfg.copy(mono = it)) })
                val pct = (cfg.balance * 100).roundToInt()
                val balance =
                    when {
                        pct < 0 -> stringResource(UiR.string.app_audio_balance_left, (-pct).toString())
                        pct > 0 -> stringResource(UiR.string.app_audio_balance_right, pct.toString())
                        else -> stringResource(UiR.string.app_audio_balance_centre)
                    }
                Text(stringResource(UiR.string.app_audio_balance) + ": " + balance)
                Slider(
                    value = cfg.balance,
                    onValueChange = { onChange(cfg.copy(balance = (it * 10).roundToInt() / 10f)) },
                    valueRange = -1f..1f,
                    steps = 19,
                    colors = quietTicks(),
                    modifier = Modifier.semantics { stateDescription = balance }
                )
            }
        },
        confirmButton = {
            TextButton(onClick = onClose, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_done)) }
        },
        dismissButton = {
            TextButton(onClick = { onChange(EffectsConfig()) }, enabled = !cfg.neutral, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_audio_reset))
            }
        }
    )
}
