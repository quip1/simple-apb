// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import org.simpleapb.core.ui.R as UiR

/** A labelled button that opens a menu of choices ("Sort by: Title"). */
@Composable
fun <T> ChoicePicker(label: String, value: T, options: List<T>, text: @Composable (T) -> String, onPick: (T) -> Unit, modifier: Modifier = Modifier) {
    var open by remember { mutableStateOf(false) }
    val current = text(value)
    Box(modifier) {
        OutlinedButton(
            onClick = { open = true },
            modifier = Modifier.heightIn(min = TouchTarget).semantics { contentDescription = "$label: $current" }
        ) {
            Text("$label: $current", maxLines = 1)
            Icon(Icons.Filled.ArrowDropDown, contentDescription = null)
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            options.forEach { o ->
                DropdownMenuItem(
                    text = { Text(text(o)) },
                    leadingIcon = { if (o == value) Icon(Icons.Filled.Check, contentDescription = null) },
                    onClick = {
                        open = false
                        onPick(o)
                    },
                    modifier = Modifier.heightIn(min = TouchTarget)
                )
            }
        }
    }
}

/** Radio buttons in a column, one row per option (each row is a 48 dp target). */
@Composable
fun <T> RadioGroup(value: T, options: List<T>, text: @Composable (T) -> String, onPick: (T) -> Unit) {
    Column(Modifier.selectableGroup()) {
        options.forEach { o ->
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier =
                Modifier
                    .fillMaxWidth()
                    .heightIn(min = TouchTarget)
                    .selectable(selected = o == value, role = Role.RadioButton, onClick = { onPick(o) })
            ) {
                RadioButton(selected = o == value, onClick = null)
                Text(text(o), modifier = Modifier.padding(start = 12.dp, top = 8.dp, bottom = 8.dp))
            }
        }
    }
}

/** A switch with its label; the whole row toggles. */
@Composable
fun SwitchRow(label: String, checked: Boolean, onChange: (Boolean) -> Unit, enabled: Boolean = true, help: String? = null) {
    Column {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier =
            Modifier
                .fillMaxWidth()
                .heightIn(min = TouchTarget)
                .toggleable(value = checked, enabled = enabled, role = Role.Switch, onValueChange = onChange)
        ) {
            Text(label, modifier = Modifier.weight(1f).padding(end = 12.dp, top = 8.dp, bottom = 8.dp))
            Switch(checked = checked, onCheckedChange = null, enabled = enabled)
        }
        if (help != null) Text(help, style = MaterialTheme.typography.bodySmall)
    }
}

/** "Are you sure?" with a confirm and a cancel button. */
@Composable
fun ConfirmDialog(title: String, body: String, confirm: String, onConfirm: () -> Unit, onDismiss: () -> Unit, busy: Boolean = false) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(body) },
        confirmButton = {
            TextButton(onClick = onConfirm, enabled = !busy, modifier = Modifier.heightIn(min = TouchTarget)) { Text(confirm) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_cancel)) }
        }
    )
}

/** A dialog asking for one line of text (a name). */
@Composable
fun TextDialog(title: String, label: String, initial: String, confirm: String, onConfirm: (String) -> Unit, onDismiss: () -> Unit) {
    var text by rememberSaveable { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            OutlinedTextField(value = text, onValueChange = {
                text = it
            }, label = { Text(label) }, singleLine = true, modifier = Modifier.fillMaxWidth())
        },
        confirmButton = {
            TextButton(
                onClick = { onConfirm(text.trim()) },
                enabled = text.isNotBlank(),
                modifier = Modifier.heightIn(min = TouchTarget)
            ) { Text(confirm) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_cancel)) }
        }
    )
}

/** A card-like group of settings or details with a heading. */
@Composable
fun Section(title: String, content: @Composable () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        SectionTitle(title)
        content()
    }
}
