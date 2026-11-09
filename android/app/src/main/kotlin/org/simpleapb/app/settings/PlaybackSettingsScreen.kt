// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlin.math.roundToInt
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.simpleapb.app.player.speedText
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.player.ControlButton
import org.simpleapb.core.player.FocusMode
import org.simpleapb.core.player.Gesture
import org.simpleapb.core.player.HeadsetAction
import org.simpleapb.core.player.PlaybackSettings
import org.simpleapb.core.player.PlaybackStore
import org.simpleapb.core.player.Speed
import org.simpleapb.core.ui.R as UiR

@HiltViewModel
class PlaybackSettingsViewModel
@Inject
constructor(private val store: PlaybackStore) : ViewModel() {
    val settings: StateFlow<PlaybackSettings?> = store.settings.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    fun update(change: (PlaybackSettings) -> PlaybackSettings) = viewModelScope.launch { store.update(change) }
}

/** Playback, notification buttons and headset settings (brief §7.2, §7.4 screen 8). */
@Composable
fun PlaybackSettingsScreen(onBack: () -> Unit) {
    val vm: PlaybackSettingsViewModel = hiltViewModel()
    val loaded by vm.settings.collectAsStateWithLifecycle()
    val s = loaded ?: return
    FormColumn {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack, modifier = Modifier.size(TouchTarget)) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(UiR.string.common_back))
            }
            Text(
                stringResource(UiR.string.app_settings_playback_title),
                style = MaterialTheme.typography.headlineSmall,
                modifier = Modifier.semantics { heading() }
            )
        }

        Section(stringResource(UiR.string.settings_playback))
        IntSlider(stringResource(UiR.string.settings_skip_back), s.skipBackS, PlaybackSettings.SKIP_RANGE, 5) { v ->
            vm.update { it.copy(skipBackS = v) }
        }
        IntSlider(stringResource(UiR.string.settings_skip_forward), s.skipForwardS, PlaybackSettings.SKIP_RANGE, 5) { v ->
            vm.update { it.copy(skipForwardS = v) }
        }
        IntSlider(stringResource(UiR.string.app_settings_auto_rewind), s.autoRewindS, PlaybackSettings.REWIND_RANGE, 1) { v ->
            vm.update { it.copy(autoRewindS = v) }
        }
        Text(stringResource(UiR.string.app_settings_auto_rewind_help), style = MaterialTheme.typography.bodySmall)
        SpeedSlider(s.defaultSpeed) { v -> vm.update { it.copy(defaultSpeed = v) } }

        Section(stringResource(UiR.string.app_settings_buttons))
        Text(stringResource(UiR.string.app_settings_buttons_help), style = MaterialTheme.typography.bodySmall)
        ButtonsEditor(s) { list -> vm.update { it.copy(buttons = list) } }

        Section(stringResource(UiR.string.app_settings_headset))
        Gesture.entries.forEach { g ->
            ActionPicker(gestureLabel(g), s.action(g), s) { a ->
                vm.update {
                    when (g) {
                        Gesture.SINGLE -> it.copy(singlePress = a)
                        Gesture.DOUBLE -> it.copy(doublePress = a)
                        Gesture.TRIPLE -> it.copy(triplePress = a)
                        Gesture.LONG -> it.copy(longPress = a)
                    }
                }
            }
        }

        Section(stringResource(UiR.string.app_settings_focus))
        Column(Modifier.selectableGroup()) {
            listOf(
                FocusMode.PAUSE to UiR.string.app_settings_focus_pause,
                FocusMode.DUCK to UiR.string.app_settings_focus_duck
            ).forEach { (mode, label) ->
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier =
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = TouchTarget)
                        .selectable(selected = s.focus == mode, role = Role.RadioButton) { vm.update { it.copy(focus = mode) } }
                ) {
                    RadioButton(selected = s.focus == mode, onClick = null)
                    Text(stringResource(label), modifier = Modifier.padding(start = 8.dp))
                }
            }
        }
        Text(stringResource(UiR.string.app_settings_focus_help), style = MaterialTheme.typography.bodySmall)

        Section(stringResource(UiR.string.player_sleep))
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier =
            Modifier
                .fillMaxWidth()
                .heightIn(min = TouchTarget)
                .toggleable(value = s.shakeToExtend, role = Role.Switch) { on -> vm.update { it.copy(shakeToExtend = on) } }
        ) {
            Text(stringResource(UiR.string.app_settings_shake), modifier = Modifier.weight(1f))
            Switch(checked = s.shakeToExtend, onCheckedChange = null)
        }
        if (s.shakeToExtend) {
            IntSlider(
                stringResource(UiR.string.app_settings_shake_minutes, s.shakeExtendMin.toString()),
                s.shakeExtendMin,
                1..15,
                1,
                showValue = false
            ) { v ->
                vm.update { it.copy(shakeExtendMin = v) }
            }
        }
    }
}

/** Step dots on a 60-step slider are noise; the value is shown as text. */
@Composable
internal fun quietTicks() = SliderDefaults.colors(activeTickColor = Color.Transparent, inactiveTickColor = Color.Transparent)

@Composable
private fun Section(title: String) {
    HorizontalDivider()
    Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
}

@Composable
private fun IntSlider(label: String, value: Int, range: IntRange, step: Int, showValue: Boolean = true, onChange: (Int) -> Unit) {
    var dragging by remember { mutableStateOf(false) }
    var drag by remember { mutableFloatStateOf(value.toFloat()) }
    val shown = if (dragging) drag.roundToInt() else value
    val valueText = stringResource(UiR.string.app_settings_seconds, shown.toString())
    Column {
        Row {
            Text(label, modifier = Modifier.weight(1f))
            if (showValue) Text(valueText, style = MaterialTheme.typography.labelLarge)
        }
        Slider(
            value = shown.toFloat(),
            onValueChange = {
                dragging = true
                drag = ((it / step).roundToInt() * step).toFloat()
            },
            onValueChangeFinished = {
                dragging = false
                onChange(drag.roundToInt())
            },
            valueRange = range.first.toFloat()..range.last.toFloat(),
            steps = (range.last - range.first) / step - 1,
            colors = quietTicks(),
            modifier =
            Modifier.semantics {
                contentDescription = label
                if (showValue) stateDescription = valueText
            }
        )
    }
}

@Composable
private fun SpeedSlider(value: Float, onChange: (Float) -> Unit) {
    var dragging by remember { mutableStateOf(false) }
    var drag by remember { mutableFloatStateOf(value) }
    val shown = if (dragging) drag else value
    val label = stringResource(UiR.string.app_settings_default_speed)
    val valueText = speedText(shown)
    Column {
        Row {
            Text(label, modifier = Modifier.weight(1f))
            Text(valueText, style = MaterialTheme.typography.labelLarge)
        }
        Slider(
            value = shown,
            onValueChange = {
                dragging = true
                drag = Speed.clamp(it)
            },
            onValueChangeFinished = {
                dragging = false
                onChange(drag)
            },
            valueRange = Speed.MIN..Speed.MAX,
            steps = ((Speed.MAX - Speed.MIN) / Speed.STEP).roundToInt() - 1,
            colors = quietTicks(),
            modifier =
            Modifier.semantics {
                contentDescription = label
                stateDescription = valueText
            }
        )
    }
}

@Composable
private fun buttonLabel(b: ControlButton, s: PlaybackSettings): String = when (b) {
    ControlButton.BACK -> stringResource(UiR.string.player_skip_back, s.skipBackS.toString())
    ControlButton.PLAY_PAUSE -> stringResource(UiR.string.app_settings_button_play_pause)
    ControlButton.FORWARD -> stringResource(UiR.string.player_skip_forward, s.skipForwardS.toString())
    ControlButton.PREVIOUS_CHAPTER -> stringResource(UiR.string.player_previous_chapter)
    ControlButton.NEXT_CHAPTER -> stringResource(UiR.string.player_next_chapter)
    ControlButton.SPEED -> stringResource(UiR.string.player_speed)
    ControlButton.SLEEP -> stringResource(UiR.string.player_sleep)
}

/** The chosen buttons in order (with move up/down), then the others to add. */
@Composable
private fun ButtonsEditor(s: PlaybackSettings, onChange: (List<ControlButton>) -> Unit) {
    val chosen = s.buttons
    val full = chosen.size >= PlaybackSettings.MAX_BUTTONS
    Column {
        chosen.forEachIndexed { i, b ->
            val name = buttonLabel(b, s)
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.heightIn(min = TouchTarget)) {
                Checkbox(
                    checked = true,
                    enabled = b != ControlButton.PLAY_PAUSE,
                    onCheckedChange = { onChange(chosen - b) },
                    modifier = Modifier.semantics { contentDescription = name }
                )
                Text(name, modifier = Modifier.weight(1f))
                IconButton(
                    onClick = { onChange(chosen.toMutableList().apply { add(i - 1, removeAt(i)) }) },
                    enabled = i > 0,
                    modifier = Modifier.size(TouchTarget)
                ) { Icon(Icons.Filled.KeyboardArrowUp, contentDescription = stringResource(UiR.string.app_settings_button_up, name)) }
                IconButton(
                    onClick = { onChange(chosen.toMutableList().apply { add(i + 1, removeAt(i)) }) },
                    enabled = i < chosen.size - 1,
                    modifier = Modifier.size(TouchTarget)
                ) { Icon(Icons.Filled.KeyboardArrowDown, contentDescription = stringResource(UiR.string.app_settings_button_down, name)) }
            }
        }
        ControlButton.entries.filter { it !in chosen }.forEach { b ->
            val name = buttonLabel(b, s)
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier =
                Modifier
                    .fillMaxWidth()
                    .heightIn(min = TouchTarget)
                    .toggleable(value = false, enabled = !full, role = Role.Checkbox) { onChange(chosen + b) }
            ) {
                Checkbox(checked = false, enabled = !full, onCheckedChange = null, modifier = Modifier.padding(horizontal = 14.dp))
                Text(name, modifier = Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun gestureLabel(g: Gesture): String = stringResource(
    when (g) {
        Gesture.SINGLE -> UiR.string.app_settings_headset_single
        Gesture.DOUBLE -> UiR.string.app_settings_headset_double
        Gesture.TRIPLE -> UiR.string.app_settings_headset_triple
        Gesture.LONG -> UiR.string.app_settings_headset_long
    }
)

@Composable
private fun actionLabel(a: HeadsetAction, s: PlaybackSettings): String = when (a) {
    HeadsetAction.NONE -> stringResource(UiR.string.app_settings_action_none)
    HeadsetAction.PLAY_PAUSE -> stringResource(UiR.string.app_settings_button_play_pause)
    HeadsetAction.BACK -> stringResource(UiR.string.player_skip_back, s.skipBackS.toString())
    HeadsetAction.FORWARD -> stringResource(UiR.string.player_skip_forward, s.skipForwardS.toString())
    HeadsetAction.PREVIOUS_CHAPTER -> stringResource(UiR.string.player_previous_chapter)
    HeadsetAction.NEXT_CHAPTER -> stringResource(UiR.string.player_next_chapter)
    HeadsetAction.BOOKMARK -> stringResource(UiR.string.app_player_bookmark_add)
}

@Composable
private fun ActionPicker(label: String, value: HeadsetAction, s: PlaybackSettings, onPick: (HeadsetAction) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(label, modifier = Modifier.weight(1f))
        Box {
            OutlinedButton(onClick = { open = true }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(actionLabel(value, s)) }
            DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
                HeadsetAction.entries.forEach { a ->
                    DropdownMenuItem(
                        text = { Text(actionLabel(a, s)) },
                        onClick = {
                            open = false
                            onPick(a)
                        }
                    )
                }
            }
        }
    }
}
