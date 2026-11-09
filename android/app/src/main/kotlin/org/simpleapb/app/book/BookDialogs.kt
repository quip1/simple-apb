// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.book

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import java.util.UUID
import kotlinx.coroutines.delay
import org.simpleapb.api.model.Collection
import org.simpleapb.api.model.ReportCreate
import org.simpleapb.api.model.ReportReason
import org.simpleapb.api.model.UserRef
import org.simpleapb.app.ui.RadioGroup
import org.simpleapb.app.ui.SectionTitle
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.ui.R as UiR

@Composable
private fun CheckRow(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier =
        Modifier
            .fillMaxWidth()
            .heightIn(min = TouchTarget)
            .toggleable(value = checked, role = Role.Checkbox, onValueChange = onChange)
    ) {
        Checkbox(checked = checked, onCheckedChange = null)
        Text(label, modifier = Modifier.padding(start = 12.dp))
    }
}

/** Tick the collections the book belongs in, or make a new one. */
@Composable
fun CollectionsDialog(bookId: UUID, collections: List<Collection>?, onToggle: (Collection) -> Unit, onCreate: (String) -> Unit, onClose: () -> Unit) {
    var name by rememberSaveable { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text(stringResource(UiR.string.collections_add_title)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                when {
                    collections == null -> Text(stringResource(UiR.string.common_loading))
                    collections.isEmpty() -> Text(stringResource(UiR.string.library_no_collections))
                    else -> collections.forEach { c -> CheckRow(c.name, bookId in c.bookIds) { onToggle(c) } }
                }
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text(stringResource(UiR.string.library_collection_new_placeholder)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(top = 12.dp)
                )
                TextButton(
                    onClick = {
                        onCreate(name)
                        name = ""
                    },
                    enabled = name.isNotBlank(),
                    modifier = Modifier.heightIn(min = TouchTarget)
                ) { Text(stringResource(UiR.string.library_collection_create)) }
            }
        },
        confirmButton = {
            TextButton(onClick = onClose, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_done)) }
        }
    )
}

/** Recommend the book to people (who must be able to see it). */
@Composable
fun RecommendDialog(title: String, s: BookState, onSearch: (String) -> Unit, onSend: (List<UserRef>, String) -> Unit, onClose: () -> Unit) {
    var query by rememberSaveable { mutableStateOf("") }
    var note by rememberSaveable { mutableStateOf("") }
    val picked = remember { mutableStateListOf<UserRef>() }
    LaunchedEffect(query) {
        delay(250)
        onSearch(query)
    }
    val sent = s.recommended
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text(stringResource(UiR.string.recommend_title, title)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                if (sent != null) {
                    Column(Modifier.semantics { liveRegion = LiveRegionMode.Polite }) {
                        Text(
                            if (sent.sent >
                                0
                            ) {
                                pluralStringResource(UiR.plurals.recommend_sent, sent.sent, sent.sent)
                            } else {
                                stringResource(UiR.string.recommend_none_sent)
                            }
                        )
                        if (sent.skipped > 0) Text(pluralStringResource(UiR.plurals.recommend_skipped, sent.skipped, sent.skipped))
                    }
                    return@Column
                }
                OutlinedTextField(
                    value = query,
                    onValueChange = { query = it },
                    label = { Text(stringResource(UiR.string.recommend_to)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )
                (picked + s.people.filter { p -> picked.none { it.id == p.id } }).forEach { p ->
                    CheckRow("${p.displayName} (@${p.username})", picked.any { it.id == p.id }) { on ->
                        if (on) picked.add(p) else picked.removeAll { it.id == p.id }
                    }
                }
                OutlinedTextField(
                    value = note,
                    onValueChange = { if (it.length <= NOTE_MAX) note = it },
                    label = { Text(stringResource(UiR.string.recommend_note)) },
                    supportingText = { Text(stringResource(UiR.string.recommend_note_hint)) },
                    modifier = Modifier.fillMaxWidth()
                )
                s.error?.let { Text(errorText(it)) }
            }
        },
        confirmButton = {
            if (sent == null) {
                TextButton(onClick = {
                    onSend(picked.toList(), note)
                }, enabled = picked.isNotEmpty() && !s.busy, modifier = Modifier.heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.recommend_submit))
                }
            } else {
                TextButton(onClick = onClose, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_done)) }
            }
        },
        dismissButton = {
            if (sent ==
                null
            ) {
                TextButton(onClick = onClose, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_cancel)) }
            }
        }
    )
}

/** Something that can be reported. */
sealed interface ReportTarget {
    val id: UUID
    val type: ReportCreate.ObjectType

    data class Book(override val id: UUID, val title: String) : ReportTarget {
        override val type get() = ReportCreate.ObjectType.BOOK
    }

    data class Review(override val id: UUID, val author: String) : ReportTarget {
        override val type get() = ReportCreate.ObjectType.REVIEW
    }

    data class User(override val id: UUID, val name: String) : ReportTarget {
        override val type get() = ReportCreate.ObjectType.USER
    }
}

@Composable
private fun reportTitle(t: ReportTarget) = when (t) {
    is ReportTarget.Book -> stringResource(UiR.string.report_title_book, t.title)
    is ReportTarget.Review -> stringResource(UiR.string.report_title_review, t.author)
    is ReportTarget.User -> stringResource(UiR.string.report_title_user, t.name)
}

@Composable
private fun reasonLabel(r: ReportReason) = stringResource(
    when (r) {
        ReportReason.COPYRIGHT -> UiR.string.report_reason_copyright
        ReportReason.INAPPROPRIATE -> UiR.string.report_reason_inappropriate
        ReportReason.SPAM -> UiR.string.report_reason_spam
        ReportReason.HARASSMENT -> UiR.string.report_reason_harassment
        ReportReason.WRONG_METADATA -> UiR.string.report_reason_wrong_metadata
        ReportReason.OTHER -> UiR.string.report_reason_other
    }
)

/** Report a book, a review or its author to the admins. */
@Composable
fun ReportDialog(targets: List<ReportTarget>, onSend: (ReportCreate.ObjectType, UUID, ReportReason, String) -> Unit, onClose: () -> Unit) {
    var target by remember { mutableStateOf(targets.first()) }
    var reason by rememberSaveable { mutableStateOf(ReportReason.OTHER) }
    var details by rememberSaveable { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text(if (targets.size == 1) reportTitle(target) else stringResource(UiR.string.report_title)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                if (targets.size > 1) {
                    SectionTitle(stringResource(UiR.string.report_what))
                    RadioGroup(target, targets, { reportTitle(it) }) { target = it }
                }
                SectionTitle(stringResource(UiR.string.report_reason))
                RadioGroup(reason, ReportReason.entries, { reasonLabel(it) }) { reason = it }
                OutlinedTextField(
                    value = details,
                    onValueChange = { details = it },
                    label = { Text(stringResource(UiR.string.report_details)) },
                    supportingText = { Text(stringResource(UiR.string.report_details_hint)) },
                    modifier = Modifier.fillMaxWidth()
                )
            }
        },
        confirmButton = {
            TextButton(
                onClick = {
                    onSend(target.type, target.id, reason, details)
                    onClose()
                },
                modifier = Modifier.heightIn(min = TouchTarget)
            ) { Text(stringResource(UiR.string.report_submit)) }
        },
        dismissButton = {
            TextButton(onClick = onClose, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_cancel)) }
        }
    )
}

private const val NOTE_MAX = 300
