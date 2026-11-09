// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import org.simpleapb.app.ui.BusyButton
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.ui.R as UiR

/** The 3-screen tour after the first sign-in (brief §7.4). Paged, no animation. */
@Composable
fun TourScreen(serverName: String, onDone: () -> Unit) {
    var page by rememberSaveable { mutableIntStateOf(0) }
    val pages =
        listOf(
            stringResource(UiR.string.app_tour_one_title) to stringResource(UiR.string.app_tour_one_body, serverName),
            stringResource(UiR.string.app_tour_two_title) to stringResource(UiR.string.app_tour_two_body),
            stringResource(UiR.string.app_tour_three_title) to stringResource(UiR.string.app_tour_three_body)
        )
    val (title, body) = pages[page]
    FormColumn {
        Text(title, style = MaterialTheme.typography.headlineSmall, modifier = Modifier.semantics { heading() })
        Text(body, style = MaterialTheme.typography.bodyLarge)
        Text("${page + 1} / ${pages.size}", style = MaterialTheme.typography.labelLarge)
        val last = page == pages.lastIndex
        BusyButton(
            stringResource(if (last) UiR.string.app_tour_done else UiR.string.app_tour_next),
            busy = false,
            onClick = { if (last) onDone() else page++ }
        )
        Row(horizontalArrangement = Arrangement.SpaceBetween, modifier = Modifier.fillMaxWidth()) {
            if (page > 0) {
                TextButton(
                    onClick = { page-- },
                    modifier = Modifier.heightIn(min = TouchTarget)
                ) { Text(stringResource(UiR.string.common_back)) }
            }
            if (!last) {
                TextButton(
                    onClick = onDone,
                    modifier = Modifier.heightIn(min = TouchTarget)
                ) { Text(stringResource(UiR.string.app_tour_skip)) }
            }
        }
    }
}
