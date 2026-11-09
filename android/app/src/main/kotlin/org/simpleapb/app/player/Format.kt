// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.player

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import java.text.NumberFormat
import java.util.Locale
import org.simpleapb.core.ui.R as UiR

/** "4:12:30" or "12:30". */
fun clock(ms: Long): String {
    val s = (ms / 1000).coerceAtLeast(0)
    val h = s / 3600
    val m = (s % 3600) / 60
    val sec = s % 60
    return if (h > 0) String.format(Locale.ROOT, "%d:%02d:%02d", h, m, sec) else String.format(Locale.ROOT, "%d:%02d", m, sec)
}

/** "3 h 12 min" or "12 min". */
@Composable
fun duration(ms: Long): String {
    val totalMin = ((ms + 59_999) / 60_000).coerceAtLeast(0)
    val h = totalMin / 60
    val m = totalMin % 60
    return if (h > 0) {
        stringResource(UiR.string.time_hours_minutes, h.toString(), m.toString())
    } else {
        stringResource(UiR.string.time_minutes, m.toString())
    }
}

/** "1.25×" in the user's number format. */
@Composable
fun speedText(s: Float): String {
    val nf = NumberFormat.getNumberInstance().apply {
        minimumFractionDigits = 0
        maximumFractionDigits = 2
    }
    return stringResource(UiR.string.player_speed_value, nf.format(s.toDouble()))
}
