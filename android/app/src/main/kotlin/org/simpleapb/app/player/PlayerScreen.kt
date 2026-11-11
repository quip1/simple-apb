// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.player

import android.os.SystemClock
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Bedtime
import androidx.compose.material.icons.filled.Bookmarks
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.DirectionsCar
import androidx.compose.material.icons.filled.Forward10
import androidx.compose.material.icons.filled.Forward30
import androidx.compose.material.icons.filled.Forward5
import androidx.compose.material.icons.filled.GraphicEq
import androidx.compose.material.icons.filled.Headphones
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Replay
import androidx.compose.material.icons.filled.Replay10
import androidx.compose.material.icons.filled.Replay30
import androidx.compose.material.icons.filled.Replay5
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material.icons.filled.SkipPrevious
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import kotlin.math.abs
import kotlin.math.roundToInt
import org.simpleapb.app.settings.quietTicks
import org.simpleapb.app.ui.AppAlertDialog
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenRefreshButton
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.player.LoadedBook
import org.simpleapb.core.player.PlaybackUi
import org.simpleapb.core.player.SleepChoice
import org.simpleapb.core.player.SleepTimer
import org.simpleapb.core.player.Speed
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

private enum class Sheet { NONE, SPEED, SLEEP, CHAPTERS, BOOKMARKS, AUDIO }

/** The full player (brief §7.4 screen 5). */
@Composable
fun PlayerScreen(vm: PlayerViewModel, onClose: () -> Unit, onCarMode: () -> Unit) {
    val book by vm.book.collectAsStateWithLifecycle()
    val ui by vm.ui.collectAsStateWithLifecycle()
    val sleep by vm.sleep.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    var sheet by rememberSaveable { mutableStateOf(Sheet.NONE) }
    val b = book

    Column(
        Modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp, vertical = 8.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onClose, modifier = Modifier.size(TouchTarget)) {
                Icon(Icons.Filled.KeyboardArrowDown, contentDescription = stringResource(UiR.string.app_player_collapse))
            }
            Text(
                stringResource(UiR.string.player_now_playing),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.weight(1f).semantics { heading() }
            )
            ScreenRefreshButton()
        }
        if (b == null) {
            Text(stringResource(UiR.string.player_nothing_playing), modifier = Modifier.padding(top = 24.dp))
            return@Column
        }
        Cover(b.coverUrl, Modifier.fillMaxWidth(0.8f).widthIn(max = 360.dp))
        Text(b.title, style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
        if (b.authors.isNotEmpty()) Text(b.authors, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center)

        // E-ink: the time display moves every few seconds, not every second (brief §11).
        val pos = shownPosition(ui, settings.einkTickS)
        val chapter = b.timeline.chapterAt(pos)
        if (chapter != null) {
            Text(chapter.title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
            Text(
                stringResource(
                    UiR.string.app_player_chapter_of,
                    (b.timeline.chapters.indexOf(chapter) + 1).toString(),
                    b.timeline.chapters.size.toString()
                ),
                style = MaterialTheme.typography.bodySmall
            )
        }
        ChapterSlider(b, pos, vm::seekTo)
        BookProgress(b, pos)

        if (ui.failed) Notice(stringResource(UiR.string.player_error_playback))

        // Transport: previous chapter · back · play/pause · forward · next chapter,
        // mirrored for the left-handed layout (brief §11).
        MirrorRow(
            settings.leftHanded,
            listOf(
                { ControlIcon(Icons.Filled.SkipPrevious, stringResource(UiR.string.player_previous_chapter), vm::previousChapter) },
                {
                    ControlIcon(
                        replayIcon(settings.skipBackS),
                        stringResource(UiR.string.player_skip_back, settings.skipBackS.toString()),
                        vm::skipBack
                    )
                },
                {
                    FilledIconButton(onClick = vm::togglePlay, modifier = Modifier.size(72.dp)) {
                        if (ui.buffering && ui.playing && !LocalEink.current) {
                            CircularProgressIndicator(Modifier.size(32.dp), color = MaterialTheme.colorScheme.onPrimary)
                        }
                        Icon(
                            if (ui.playing) Icons.Filled.Pause else Icons.Filled.PlayArrow,
                            contentDescription = stringResource(if (ui.playing) UiR.string.player_pause else UiR.string.player_play),
                            modifier = Modifier.size(40.dp)
                        )
                    }
                },
                {
                    ControlIcon(
                        forwardIcon(settings.skipForwardS),
                        stringResource(UiR.string.player_skip_forward, settings.skipForwardS.toString()),
                        vm::skipForward
                    )
                },
                { ControlIcon(Icons.Filled.SkipNext, stringResource(UiR.string.player_next_chapter), vm::nextChapter) }
            )
        )

        // Speed · sleep · chapters · bookmarks.
        MirrorRow(
            settings.leftHanded,
            listOf(
                { LabeledAction(Icons.Filled.Speed, speedText(ui.speed), stringResource(UiR.string.player_speed)) { sheet = Sheet.SPEED } },
                {
                    LabeledAction(Icons.Filled.Bedtime, sleepLabel(sleep, ui.sleepLeftMs), stringResource(UiR.string.player_sleep)) {
                        sheet =
                            Sheet.SLEEP
                    }
                },
                { LabeledAction(Icons.AutoMirrored.Filled.List, stringResource(UiR.string.app_player_chapters), null) { sheet = Sheet.CHAPTERS } },
                {
                    LabeledAction(Icons.Filled.Bookmarks, stringResource(UiR.string.bookmarks_title), null) {
                        vm.loadBookmarks()
                        sheet = Sheet.BOOKMARKS
                    }
                }
            )
        )

        // Audio effects · car mode.
        MirrorRow(
            settings.leftHanded,
            listOf(
                { LabeledAction(Icons.Filled.GraphicEq, stringResource(UiR.string.app_audio_title), null) { sheet = Sheet.AUDIO } },
                { LabeledAction(Icons.Filled.DirectionsCar, stringResource(UiR.string.app_car_title), null, onCarMode) }
            )
        )
    }

    val close = { sheet = Sheet.NONE }
    when (sheet) {
        Sheet.NONE -> Unit
        Sheet.SPEED -> SpeedDialog(ui.speed, vm::setSpeed, close)
        Sheet.SLEEP -> SleepDialog(sleep, ui.sleepLeftMs, settings.lastSleepMin, vm::setSleep, close)
        Sheet.CHAPTERS -> b?.let { ChaptersDialog(it, ui.positionMs, { ms -> vm.seekTo(ms) }, close) }
        Sheet.BOOKMARKS -> b?.let { BookmarksDialog(vm, ui.positionMs, close) }
        Sheet.AUDIO -> AudioDialog(settings.effects, vm::setEffects, close)
    }
}

/** A row of controls, in reverse order for the left-handed layout (text and icons stay as they are). */
@Composable
private fun MirrorRow(mirrored: Boolean, items: List<@Composable () -> Unit>) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly, verticalAlignment = Alignment.CenterVertically) {
        (if (mirrored) items.asReversed() else items).forEach { it() }
    }
}

/**
 * The position to draw. On e-ink it only moves every [tickS] seconds while
 * playing (less ghosting); seeks and pauses show at once.
 */
@Composable
fun shownPosition(ui: PlaybackUi, tickS: Int): Long {
    val holder = remember { ShownPosition() }
    if (!LocalEink.current) return ui.positionMs
    val now = SystemClock.elapsedRealtime()
    val expected = holder.shown + ((now - holder.at) * ui.speed).toLong()
    val jumped = abs(ui.positionMs - expected) > JUMP_MS
    if (!ui.playing || jumped || now - holder.at >= tickS * 1_000L) {
        holder.shown = ui.positionMs
        holder.at = now
    }
    return holder.shown
}

private class ShownPosition {
    var shown = 0L
    var at = 0L
}

private const val JUMP_MS = 3_000L

@Composable
fun Cover(url: String?, modifier: Modifier = Modifier) {
    Box(
        modifier
            .aspectRatio(1f)
            .clip(RoundedCornerShape(12.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .then(if (LocalEink.current) Modifier.border(1.dp, MaterialTheme.colorScheme.onSurface, RoundedCornerShape(12.dp)) else Modifier),
        contentAlignment = Alignment.Center
    ) {
        Icon(Icons.Filled.Headphones, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(48.dp))
        if (url != null) AsyncImage(model = url, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.matchParentSize())
    }
}

@Composable
private fun ChapterSlider(b: LoadedBook, positionMs: Long, onSeek: (Long) -> Unit) {
    val ch = b.timeline.chapterAt(positionMs)
    val start = ch?.startMs ?: 0
    val end = (ch?.endMs?.takeIf { it > start } ?: b.timeline.durationMs).coerceAtLeast(start + 1)
    var dragging by remember { mutableStateOf(false) }
    var dragValue by remember { mutableFloatStateOf(0f) }
    val shown = if (dragging) dragValue else (positionMs - start).coerceIn(0, end - start).toFloat()
    val valueText = stringResource(UiR.string.player_position_valuetext, clock(shown.toLong()), clock(end - start))
    val label = stringResource(UiR.string.player_chapter_position)
    Column(Modifier.fillMaxWidth()) {
        Slider(
            value = shown,
            valueRange = 0f..(end - start).toFloat(),
            onValueChange = {
                dragging = true
                dragValue = it
            },
            onValueChangeFinished = {
                dragging = false
                onSeek(start + dragValue.toLong())
            },
            modifier =
            Modifier.semantics {
                contentDescription = label
                stateDescription = valueText
            }
        )
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(clock(shown.toLong()), style = MaterialTheme.typography.labelMedium)
            Text("-" + clock((end - start) - shown.toLong()), style = MaterialTheme.typography.labelMedium)
        }
    }
}

@Composable
private fun BookProgress(b: LoadedBook, positionMs: Long) {
    val total = b.timeline.durationMs.coerceAtLeast(1)
    val fraction = (positionMs.toFloat() / total).coerceIn(0f, 1f)
    val text = stringResource(UiR.string.player_book_left, (fraction * 100).roundToInt().toString(), duration(total - positionMs))
    val label = stringResource(UiR.string.player_book_progress)
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        LinearProgressIndicator(
            progress = { fraction },
            modifier =
            Modifier.fillMaxWidth().semantics {
                contentDescription = label
                stateDescription = text
            }
        )
        Text(text, style = MaterialTheme.typography.bodySmall)
    }
}

@Composable
private fun ControlIcon(icon: ImageVector, label: String, onClick: () -> Unit) {
    IconButton(onClick = onClick, modifier = Modifier.size(56.dp)) {
        Icon(icon, contentDescription = label, modifier = Modifier.size(32.dp))
    }
}

@Composable
private fun LabeledAction(icon: ImageVector, text: String, description: String?, onClick: () -> Unit) {
    Column(
        Modifier
            .clip(RoundedCornerShape(8.dp))
            .clickable(role = Role.Button, onClick = onClick)
            .heightIn(min = TouchTarget)
            .padding(8.dp)
            .semantics(mergeDescendants = true) { if (description != null) contentDescription = "$description, $text" },
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Icon(icon, contentDescription = null)
        Text(text, style = MaterialTheme.typography.labelMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

internal fun replayIcon(s: Int) = when (s) {
    5 -> Icons.Filled.Replay5
    10 -> Icons.Filled.Replay10
    30 -> Icons.Filled.Replay30
    else -> Icons.Filled.Replay
}

internal fun forwardIcon(s: Int) = when (s) {
    5 -> Icons.Filled.Forward5
    10 -> Icons.Filled.Forward10
    30 -> Icons.Filled.Forward30
    else -> Icons.Filled.Forward30
}

@Composable
private fun sleepLabel(t: SleepTimer?, leftMs: Long?): String = when {
    t == null -> stringResource(UiR.string.player_sleep_off)
    t is SleepTimer.EndOfChapter -> stringResource(UiR.string.player_sleep_end_of_chapter)
    else -> clock(leftMs ?: 0)
}

// ── dialogs ──

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun SpeedDialog(speed: Float, onSpeed: (Float) -> Unit, onClose: () -> Unit) {
    var value by remember { mutableFloatStateOf(speed) }
    fun set(s: Float) {
        value = Speed.clamp(s)
        onSpeed(value)
    }
    AppAlertDialog(
        onDismissRequest = onClose,
        confirmButton = { TextButton(onClick = onClose) { Text(stringResource(UiR.string.common_done)) } },
        title = { Text(stringResource(UiR.string.player_speed)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    speedText(value),
                    style = MaterialTheme.typography.headlineMedium,
                    modifier = Modifier.fillMaxWidth().semantics { liveRegion = LiveRegionMode.Polite },
                    textAlign = TextAlign.Center
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    val slower = stringResource(UiR.string.app_player_speed_slower)
                    TextButton(
                        onClick = { set(value - Speed.STEP) },
                        modifier = Modifier.heightIn(min = TouchTarget).semantics { contentDescription = slower }
                    ) {
                        Text("−", style = MaterialTheme.typography.titleLarge)
                    }
                    val sliderLabel = stringResource(UiR.string.player_speed)
                    val sliderValue = speedText(value)
                    Slider(
                        value = value,
                        onValueChange = { value = Speed.clamp(it) },
                        onValueChangeFinished = { onSpeed(value) },
                        valueRange = Speed.MIN..Speed.MAX,
                        steps = ((Speed.MAX - Speed.MIN) / Speed.STEP).roundToInt() - 1,
                        colors = quietTicks(),
                        modifier =
                        Modifier.weight(1f).semantics {
                            contentDescription = sliderLabel
                            stateDescription = sliderValue
                        }
                    )
                    val faster = stringResource(UiR.string.app_player_speed_faster)
                    TextButton(
                        onClick = { set(value + Speed.STEP) },
                        modifier = Modifier.heightIn(min = TouchTarget).semantics { contentDescription = faster }
                    ) {
                        Text("+", style = MaterialTheme.typography.titleLarge)
                    }
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf(0.75f, 1f, 1.25f, 1.5f, 1.75f, 2f, 2.5f, 3f).forEach { s ->
                        OutlinedButton(onClick = { set(s) }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(speedText(s)) }
                    }
                }
            }
        }
    )
}

@Composable
private fun SleepDialog(current: SleepTimer?, leftMs: Long?, lastMinutes: Int, onSet: (SleepChoice, Int) -> Unit, onClose: () -> Unit) {
    var custom by rememberSaveable { mutableStateOf(lastMinutes.toString()) }
    val presets = listOf(5, 10, 15, 30, 45, 60)
    AppAlertDialog(
        onDismissRequest = onClose,
        confirmButton = { TextButton(onClick = onClose) { Text(stringResource(UiR.string.common_close)) } },
        title = { Text(stringResource(UiR.string.player_sleep)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                when (current) {
                    null -> Unit
                    is SleepTimer.EndOfChapter -> Text(stringResource(UiR.string.app_player_sleep_chapter_active))
                    is SleepTimer.Until -> {
                        val m = (((leftMs ?: 0) + 59_999) / 60_000).toInt()
                        Text(pluralStringResource(UiR.plurals.player_sleep_left, m, m))
                    }
                }
                Column(Modifier.selectableGroup()) {
                    SleepOption(stringResource(UiR.string.player_sleep_off), current == null) {
                        onSet(SleepChoice.OFF, 0)
                        onClose()
                    }
                    presets.forEach { m ->
                        SleepOption(stringResource(UiR.string.player_sleep_minutes, m.toString()), false) {
                            onSet(SleepChoice.MINUTES, m)
                            onClose()
                        }
                    }
                    SleepOption(stringResource(UiR.string.player_sleep_end_of_chapter), current is SleepTimer.EndOfChapter) {
                        onSet(SleepChoice.END_OF_CHAPTER, 0)
                        onClose()
                    }
                }
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        value = custom,
                        onValueChange = { v -> custom = v.filter { it.isDigit() }.take(3) },
                        label = { Text(stringResource(UiR.string.app_player_sleep_custom)) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.weight(1f)
                    )
                    val minutes = custom.toIntOrNull() ?: 0
                    Button(
                        onClick = {
                            onSet(SleepChoice.MINUTES, minutes)
                            onClose()
                        },
                        enabled = minutes in 1..600,
                        modifier = Modifier.heightIn(min = TouchTarget)
                    ) { Text(stringResource(UiR.string.app_player_sleep_set)) }
                }
            }
        }
    )
}

@Composable
private fun SleepOption(label: String, selected: Boolean, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = TouchTarget)
            .selectable(selected = selected, role = Role.RadioButton, onClick = onClick),
        verticalAlignment = Alignment.CenterVertically
    ) {
        RadioButton(selected = selected, onClick = null)
        Text(label, modifier = Modifier.padding(start = 8.dp))
    }
}

@Composable
private fun ChaptersDialog(b: LoadedBook, positionMs: Long, onSeek: (Long) -> Unit, onClose: () -> Unit) {
    val current = b.timeline.chapterAt(positionMs)
    val list = rememberLazyListState(initialFirstVisibleItemIndex = b.timeline.chapters.indexOf(current).coerceAtLeast(0))
    AppAlertDialog(
        onDismissRequest = onClose,
        confirmButton = { TextButton(onClick = onClose) { Text(stringResource(UiR.string.common_close)) } },
        title = { Text(pluralStringResource(UiR.plurals.chapters_title, b.timeline.chapters.size, b.timeline.chapters.size)) },
        text = {
            LazyColumn(state = list) {
                items(b.timeline.chapters, key = { it.index }) { ch ->
                    val isCurrent = ch == current
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .heightIn(min = TouchTarget)
                            .selectable(selected = isCurrent, role = Role.Button) {
                                onSeek(ch.startMs)
                                onClose()
                            }.padding(vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text(
                            ch.title,
                            modifier = Modifier.weight(1f),
                            fontWeight = if (isCurrent) FontWeight.Bold else FontWeight.Normal
                        )
                        Text(clock(ch.startMs), style = MaterialTheme.typography.labelMedium)
                    }
                    HorizontalDivider()
                }
            }
        }
    )
}

@Composable
private fun BookmarksDialog(vm: PlayerViewModel, positionMs: Long, onClose: () -> Unit) {
    val s by vm.bookmarks.collectAsStateWithLifecycle()
    var note by rememberSaveable { mutableStateOf("") }
    val eink = LocalEink.current
    AppAlertDialog(
        onDismissRequest = onClose,
        confirmButton = { TextButton(onClick = onClose) { Text(stringResource(UiR.string.common_close)) } },
        title = { Text(stringResource(UiR.string.bookmarks_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = note,
                    onValueChange = { note = it.take(500) },
                    label = { Text(stringResource(UiR.string.bookmarks_note_label)) },
                    placeholder = { Text(stringResource(UiR.string.bookmarks_note_placeholder)) },
                    modifier = Modifier.fillMaxWidth()
                )
                Button(
                    onClick = {
                        vm.addBookmark(note)
                        note = ""
                    },
                    modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)
                ) { Text(stringResource(UiR.string.bookmarks_add_here, clock(positionMs))) }
                s.error?.let { Notice(errorText(it)) }
                if (s.loading && s.items.isEmpty()) {
                    Text(stringResource(UiR.string.common_loading))
                } else if (s.items.isEmpty()) {
                    Text(stringResource(UiR.string.bookmarks_none))
                }
                LazyColumn(Modifier.heightIn(max = 320.dp)) {
                    items(s.items, key = { it.id.toString() }) { bm ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Column(
                                Modifier
                                    .weight(1f)
                                    .heightIn(min = TouchTarget)
                                    .clickable(role = Role.Button) {
                                        vm.seekTo(bm.positionMs)
                                        onClose()
                                    }.padding(vertical = 8.dp)
                                    .semantics(mergeDescendants = true) {}
                            ) {
                                Text(stringResource(UiR.string.bookmarks_jump, clock(bm.positionMs)), style = MaterialTheme.typography.bodyLarge)
                                bm.note?.let { Text(it, style = MaterialTheme.typography.bodySmall) }
                            }
                            IconButton(onClick = { vm.deleteBookmark(bm.id) }, modifier = Modifier.size(TouchTarget)) {
                                Icon(Icons.Filled.Delete, contentDescription = stringResource(UiR.string.bookmarks_delete))
                            }
                        }
                        if (!eink) HorizontalDivider() else HorizontalDivider(thickness = 2.dp)
                    }
                }
            }
        }
    )
}

/** "Continue where you left off?" (brief §6.8). The user must choose. */
@Composable
fun ConflictDialog(vm: PlayerViewModel) {
    val c by vm.conflict.collectAsStateWithLifecycle()
    val conflict = c ?: return
    // Playback continues while the user decides: show where this device is now.
    val ui by vm.ui.collectAsStateWithLifecycle()
    val mine = ui.positionMs
    val device = conflict.serverDevice ?: stringResource(UiR.string.player_another_device)
    AppAlertDialog(
        onDismissRequest = {},
        title = { Text(stringResource(UiR.string.player_conflict_title)) },
        text = {
            Text(stringResource(UiR.string.player_conflict_body, clock(conflict.serverPositionMs), device, clock(mine)))
        },
        confirmButton = {
            Button(onClick = { vm.resolveConflict(jump = true) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.player_conflict_jump, clock(conflict.serverPositionMs)))
            }
        },
        dismissButton = {
            TextButton(onClick = { vm.resolveConflict(jump = false) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.player_conflict_stay, clock(mine)))
            }
        }
    )
}
