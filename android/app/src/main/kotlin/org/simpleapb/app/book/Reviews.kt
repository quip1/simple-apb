// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.book

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.StarBorder
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.unit.dp
import java.math.RoundingMode
import java.text.NumberFormat
import org.simpleapb.api.model.Me
import org.simpleapb.api.model.Review
import org.simpleapb.app.ui.ConfirmDialog
import org.simpleapb.app.ui.SectionTitle
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.ui.R as UiR

/** Read-only stars with a spoken "4 out of 5 stars". */
@Composable
fun Stars(n: Int) {
    val label = stringResource(UiR.string.reviews_stars_of, n.toString())
    Row(Modifier.clearAndSetSemantics { contentDescription = label }) {
        (1..5).forEach { i ->
            Icon(
                if (i <=
                    n
                ) {
                    Icons.Filled.Star
                } else {
                    Icons.Filled.StarBorder
                },
                contentDescription = null,
                modifier = Modifier.size(18.dp)
            )
        }
    }
}

/** Five stars to pick a rating from; each is a 48 dp radio button. */
@Composable
private fun StarPicker(value: Int, onPick: (Int) -> Unit) {
    Row(Modifier.selectableGroup()) {
        (1..5).forEach { i ->
            val label = pluralStringResource(UiR.plurals.reviews_stars, i, i)
            Icon(
                if (i <= value) Icons.Filled.Star else Icons.Filled.StarBorder,
                contentDescription = label,
                tint = MaterialTheme.colorScheme.primary,
                modifier =
                Modifier
                    .size(TouchTarget)
                    .selectable(selected = value == i, role = Role.RadioButton, onClick = { onPick(i) })
            )
        }
    }
}

/** Ratings and reviews: the average, your own review, then everyone else's. */
@Composable
fun Reviews(vm: BookViewModel, s: BookState, me: Me?) {
    val page = s.reviews ?: return
    var reporting by rememberSaveable { mutableStateOf<String?>(null) }
    var deleting by rememberSaveable { mutableStateOf(false) }
    SectionTitle(stringResource(UiR.string.reviews_title))
    val sum = page.summary
    if (sum.count == 0) {
        Text(stringResource(UiR.string.reviews_none))
    } else {
        val avg = NumberFormat.getNumberInstance().apply { maximumFractionDigits = 1 }.format(sum.average.setScale(1, RoundingMode.HALF_UP))
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Stars(sum.average.setScale(0, RoundingMode.HALF_UP).toInt())
            Text(
                stringResource(UiR.string.app_book_rating_average, avg) + " · " +
                    pluralStringResource(UiR.plurals.reviews_count, sum.count, sum.count)
            )
        }
        // The breakdown as text rows (5 stars first).
        Column(Modifier.fillMaxWidth()) {
            sum.histogram.withIndex().reversed().forEach { (i, n) ->
                Text(pluralStringResource(UiR.plurals.reviews_stars, i + 1, i + 1) + ": $n", style = MaterialTheme.typography.bodySmall)
            }
        }
    }

    if (BookViewModel.canReview(me)) MyReview(page.mine, s.busy, vm::saveReview) { deleting = true }

    page.items.filter { !it.mine }.forEach { r ->
        HorizontalDivider()
        ReviewItem(r, onReport = { reporting = r.id.toString() })
    }
    if (page.nextCursor != null) {
        TextButton(onClick = vm::moreReviews, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.reviews_more)) }
    }

    if (deleting) {
        ConfirmDialog(
            title = stringResource(UiR.string.reviews_delete_title),
            body = stringResource(UiR.string.reviews_delete_body),
            confirm = stringResource(UiR.string.reviews_delete),
            onConfirm = {
                vm.deleteReview()
                deleting = false
            },
            onDismiss = { deleting = false }
        )
    }
    val target = page.items.firstOrNull { it.id.toString() == reporting }
    if (target != null) {
        val former = stringResource(UiR.string.reviews_former_user)
        val targets =
            listOfNotNull(
                ReportTarget.Review(target.id, target.author?.displayName ?: former),
                target.author?.let { ReportTarget.User(it.id, it.displayName) }
            )
        ReportDialog(targets, onSend = vm::report, onClose = { reporting = null })
    }
}

@Composable
private fun MyReview(mine: Review?, busy: Boolean, onSave: (Int, String) -> Unit, onDelete: () -> Unit) {
    var editing by rememberSaveable(mine?.updatedAt) { mutableStateOf(mine == null) }
    var stars by rememberSaveable(mine?.updatedAt) { mutableIntStateOf(mine?.stars ?: 0) }
    var text by rememberSaveable(mine?.updatedAt) { mutableStateOf(mine?.text.orEmpty()) }
    LaunchedEffect(mine?.updatedAt) { editing = mine == null }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(stringResource(UiR.string.reviews_yours), style = MaterialTheme.typography.titleSmall)
        if (mine?.hidden == true) Text(stringResource(UiR.string.reviews_hidden_mine), style = MaterialTheme.typography.bodySmall)
        if (!editing && mine != null) {
            Stars(mine.stars)
            mine.text?.let { Text(it) }
            Row {
                TextButton(onClick = {
                    editing = true
                }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.reviews_edit)) }
                TextButton(onClick = onDelete, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.reviews_delete)) }
            }
            return@Column
        }
        Text(stringResource(UiR.string.reviews_your_rating), style = MaterialTheme.typography.labelMedium)
        StarPicker(stars) { stars = it }
        OutlinedTextField(
            value = text,
            onValueChange = { text = it },
            label = { Text(stringResource(UiR.string.reviews_text)) },
            isError = text.length > MAX,
            supportingText = {
                if (text.length > MAX) {
                    Text(stringResource(UiR.string.reviews_too_long))
                } else {
                    Text(pluralStringResource(UiR.plurals.reviews_text_hint, MAX - text.length, MAX - text.length))
                }
            },
            modifier = Modifier.fillMaxWidth()
        )
        Row {
            Button(onClick = {
                onSave(stars, text)
            }, enabled = stars > 0 && text.length <= MAX && !busy, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(if (mine == null) UiR.string.reviews_post else UiR.string.reviews_update))
            }
            if (mine != null) {
                TextButton(onClick = {
                    editing = false
                }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.common_cancel)) }
            }
        }
    }
}

@Composable
private fun ReviewItem(r: Review, onReport: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(r.author?.displayName ?: stringResource(UiR.string.reviews_former_user), style = MaterialTheme.typography.bodyLarge)
            Stars(r.stars)
        }
        Text(ago(r.updatedAt), style = MaterialTheme.typography.bodySmall)
        if (r.hidden) {
            Text(
                r.hiddenReason?.let {
                    stringResource(UiR.string.reviews_hidden_reason, it)
                } ?: stringResource(UiR.string.reviews_hidden),
                style = MaterialTheme.typography.labelMedium
            )
        }
        r.text?.let { Text(it) }
        TextButton(onClick = onReport, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.report_action)) }
    }
}

private const val MAX = 500
