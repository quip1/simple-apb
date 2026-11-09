// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.ui

import android.annotation.SuppressLint
import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

/** Minimum touch target (brief §11: 48 dp). */
val TouchTarget = 48.dp

/** A scrolling, centred form column that stays above the keyboard. */
@Composable
fun FormColumn(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier =
        modifier
            .fillMaxWidth()
            .imePadding()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 20.dp, vertical = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Column(
            modifier = Modifier.widthIn(max = 480.dp).fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(16.dp),
            content = content
        )
    }
}

/** A primary button that shows a spinner while busy (no spinner in e-ink). */
@Composable
fun BusyButton(text: String, busy: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    Button(
        onClick = onClick,
        enabled = enabled && !busy,
        modifier = modifier.fillMaxWidth().heightIn(min = TouchTarget)
    ) {
        if (busy && !LocalEink.current) {
            CircularProgressIndicator(
                modifier = Modifier.padding(end = 8.dp).heightIn(max = 20.dp).widthIn(max = 20.dp),
                strokeWidth = 2.dp
            )
        }
        Text(text)
    }
}

/** An error or notice box that screen readers announce. */
@Composable
fun Notice(text: String, error: Boolean = true) {
    val colors =
        if (error) {
            CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.errorContainer,
                contentColor = MaterialTheme.colorScheme.onErrorContainer
            )
        } else {
            CardDefaults.cardColors()
        }
    Card(
        colors = colors,
        border = if (LocalEink.current) CardDefaults.outlinedCardBorder() else null,
        modifier = Modifier.fillMaxWidth().semantics { liveRegion = if (error) LiveRegionMode.Assertive else LiveRegionMode.Polite }
    ) {
        Text(text, modifier = Modifier.padding(16.dp))
    }
}

/** The translated text for an API failure (error.<code>, with fallbacks). */
@Composable
fun errorText(failure: ApiFailure): String {
    LocalResources.current // re-read when the configuration changes
    return errorMessage(LocalContext.current, failure)
}

/** [errorText] outside composition (snackbars from events). */
@SuppressLint("DiscouragedApi")
fun errorMessage(context: Context, failure: ApiFailure): String {
    val code =
        when {
            failure.code == "offline" -> "offline"
            failure.code == "bad_response" || failure.code == "http_404" -> "not_simpleapb"
            else -> failure.code
        }
    val id = context.resources.getIdentifier("error_$code", "string", context.packageName)
    return context.getString(if (id != 0) id else UiR.string.error_generic)
}
