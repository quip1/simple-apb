// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.stats

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.time.DayOfWeek
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.time.temporal.TemporalAdjusters
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.model.MyStats
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.player.Cover
import org.simpleapb.app.ui.ChoicePicker
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.SectionTitle
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.coverUrl
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.millis
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

data class StatsState(val year: Int = LocalDate.now().year, val stats: MyStats? = null, val error: ApiFailure? = null)

@HiltViewModel
class StatsViewModel
@Inject
constructor(private val session: MeSession) : ViewModel() {
    private val _state = MutableStateFlow(StatsState())
    val state: StateFlow<StatsState> = _state.asStateFlow()

    fun load(year: Int = _state.value.year) = viewModelScope.launch {
        _state.update { it.copy(year = year) }
        val api = session.api() ?: return@launch
        // The server buckets days in our time zone.
        val r = call { api.me.getMyStats(year = year, tz = ZoneId.systemDefault().id) }
        _state.update {
            when (r) {
                is Outcome.Ok -> it.copy(stats = r.value, error = null)
                is Outcome.Failed -> it.copy(error = r.failure)
            }
        }
    }
}

/** One bar: a spoken/table label, a short axis tick, and listening time. */
data class Bar(val label: String, val tick: String, val valueMs: Long)

/** "12 h 30 min" for listening totals. */
@Composable
fun hours(ms: Long): String {
    val totalMin = ms / 60_000
    val h = totalMin / 60
    val m = totalMin % 60
    return when {
        h > 0 && m > 0 -> stringResource(UiR.string.time_hours_minutes, h.toString(), m.toString())
        h > 0 -> stringResource(UiR.string.time_hours, h.toString())
        else -> stringResource(UiR.string.time_minutes, m.toString())
    }
}

/** Listening stats and year in review (brief §6.17, §7.4 screen 7). */
@Composable
fun StatsScreen(account: Account, onBook: (String) -> Unit, onAuthor: (String) -> Unit) {
    val vm: StatsViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    LaunchedEffect(account.id) { vm.load() }
    val thisYear = LocalDate.now().year
    val years = (listOf(thisYear) + s.stats?.years.orEmpty()).distinct().sortedDescending()
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        ScreenHeader(stringResource(UiR.string.stats_title, s.year.toString()))
        ChoicePicker(stringResource(UiR.string.stats_year), s.year, years, { it.toString() }, { vm.load(it) })
        s.error?.let { Notice(errorText(it)) }
        val d = s.stats
        when {
            d == null -> Text(stringResource(UiR.string.common_loading))
            d.totalMs == 0L && d.booksFinished == 0 -> {
                SectionTitle(stringResource(UiR.string.stats_empty_title))
                Text(stringResource(UiR.string.stats_empty_body))
            }
            else -> StatsBody(d, s.year, account.serverUrl, onBook, onAuthor)
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun StatsBody(d: MyStats, year: Int, serverUrl: String, onBook: (String) -> Unit, onAuthor: (String) -> Unit) {
    FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Tile(stringResource(UiR.string.stats_total), hours(d.totalMs))
        Tile(stringResource(UiR.string.stats_finished), d.booksFinished.toString())
        Tile(stringResource(UiR.string.stats_active_days), d.activeDays.toString())
        Tile(stringResource(UiR.string.stats_streak), pluralStringResource(UiR.plurals.stats_days, d.longestStreakDays, d.longestStreakDays))
    }
    d.books.firstOrNull()?.let { top ->
        Text(stringResource(UiR.string.stats_top_fact, top.title, hours(top.listenedMs), d.authors.firstOrNull()?.name.orEmpty()))
    }

    val byDay = remember(d) { d.days.associate { LocalDate.parse(it.date) to it.listenedMs } }
    val last = if (year == LocalDate.now().year) LocalDate.now() else LocalDate.of(year, 12, 31)
    val dayFmt = remember { DateTimeFormatter.ofPattern("EEE d MMM") }
    val shortFmt = remember { DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM) }
    val weekLabel = stringResource(UiR.string.stats_week_of, "%s")
    val daily =
        remember(d, year) {
            (29 downTo 0).map { last.minusDays(it.toLong()) }.filter { it.year == year }.mapIndexed { i, day ->
                Bar(day.format(dayFmt), if ((29 - i) % 7 == 0) day.dayOfMonth.toString() else "", byDay[day] ?: 0)
            }
        }
    val weekly =
        remember(d, year) {
            val out = mutableListOf<Bar>()
            var start = LocalDate.of(year, 1, 1).with(TemporalAdjusters.previousOrSame(DayOfWeek.MONDAY))
            while (!start.isAfter(last)) {
                var sum = 0L
                var tick = ""
                for (i in 0 until 7) {
                    val day = start.plusDays(i.toLong())
                    if (day.year != year) continue
                    sum += byDay[day] ?: 0
                    if (day.dayOfMonth == 1) tick = day.month.getDisplayName(java.time.format.TextStyle.SHORT, java.util.Locale.getDefault())
                }
                out += Bar(weekLabel.replace("%s", start.format(shortFmt)), tick, sum)
                start = start.plusWeeks(1)
            }
            out
        }
    BarChart(stringResource(UiR.string.stats_daily), daily)
    BarChart(stringResource(UiR.string.stats_weekly), weekly)

    SectionTitle(stringResource(UiR.string.stats_per_book))
    val topMs = d.books.firstOrNull()?.listenedMs ?: 0
    d.books.forEach { b ->
        Row(
            Modifier
                .fillMaxWidth()
                .heightIn(min = TouchTarget)
                .clickable(role = Role.Button) { onBook(b.bookId.toString()) },
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Cover(coverUrl(serverUrl, b.coverUrl, b.hasCover, 128), Modifier.size(48.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(b.title, maxLines = 2)
                LinearProgressIndicator(progress = {
                    if (topMs >
                        0
                    ) {
                        b.listenedMs.toFloat() / topMs
                    } else {
                        0f
                    }
                }, modifier = Modifier.fillMaxWidth(), drawStopIndicator = {})
                Text(hours(b.listenedMs), style = MaterialTheme.typography.bodySmall)
            }
        }
    }

    SectionTitle(stringResource(UiR.string.stats_top_authors))
    d.authors.forEach { a ->
        Row(
            Modifier
                .fillMaxWidth()
                .heightIn(min = TouchTarget)
                .clickable(role = Role.Button) { onAuthor(a.id.toString()) },
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(a.name, modifier = Modifier.weight(1f))
            Text(hours(a.listenedMs), style = MaterialTheme.typography.bodySmall)
        }
    }

    SectionTitle(stringResource(UiR.string.stats_finished_list))
    if (d.finished.isEmpty()) Text(stringResource(UiR.string.stats_none_finished))
    val dateFmt = remember { DateTimeFormatter.ofPattern("d MMMM") }
    d.finished.forEach { b ->
        Row(
            Modifier
                .fillMaxWidth()
                .heightIn(min = TouchTarget)
                .clickable(role = Role.Button) { onBook(b.bookId.toString()) },
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(b.title, modifier = Modifier.weight(1f))
            b.finishedAt?.let {
                Text(
                    java.time.Instant.ofEpochMilli(millis(it)).atZone(ZoneId.systemDefault()).toLocalDate().format(dateFmt),
                    style = MaterialTheme.typography.bodySmall
                )
            }
        }
    }
}

@Composable
private fun Tile(label: String, value: String) {
    OutlinedCard(Modifier.width(156.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(label, style = MaterialTheme.typography.labelMedium)
            Text(value, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
        }
    }
}

/**
 * A single-series bar chart of listening time. Tap a bar to read it; the
 * same numbers are one tap away as a table (screen readers, e-ink).
 */
@Composable
private fun BarChart(title: String, bars: List<Bar>) {
    var table by rememberSaveable(title) { mutableStateOf(false) }
    var picked by rememberSaveable(title) { mutableIntStateOf(-1) }
    val eink = LocalEink.current
    val bar = MaterialTheme.colorScheme.primary.let { if (eink) MaterialTheme.colorScheme.onSurface else it }
    val grid = MaterialTheme.colorScheme.outlineVariant
    val maxMs = (bars.maxOfOrNull { it.valueMs } ?: 0L).coerceAtLeast(1)
    val alt = stringResource(UiR.string.stats_chart_alt, title)
    OutlinedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                SectionTitle(title, Modifier.weight(1f))
                TextButton(onClick = { table = !table }, modifier = Modifier.heightIn(min = TouchTarget)) {
                    Text(stringResource(if (table) UiR.string.stats_show_chart else UiR.string.stats_show_table))
                }
            }
            if (table) {
                Row {
                    Text(stringResource(UiR.string.stats_col_period), style = MaterialTheme.typography.labelMedium, modifier = Modifier.weight(1f))
                    Text(stringResource(UiR.string.stats_col_listening), style = MaterialTheme.typography.labelMedium)
                }
                HorizontalDivider()
                bars.forEach { b ->
                    Row(Modifier.padding(vertical = 4.dp)) {
                        Text(b.label, modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
                        Text(hours(b.valueMs), style = MaterialTheme.typography.bodySmall)
                    }
                }
                return@Column
            }
            val sel = bars.getOrNull(picked)
            Text(
                if (sel != null) "${sel.label}: ${hours(sel.valueMs)}" else " ",
                style = MaterialTheme.typography.bodySmall
            )
            Canvas(
                Modifier
                    .fillMaxWidth()
                    .height(140.dp)
                    .semantics { contentDescription = alt }
                    .pointerInput(bars) {
                        detectTapGestures { o ->
                            if (bars.isNotEmpty()) picked = (o.x / (size.width.toFloat() / bars.size)).toInt().coerceIn(0, bars.lastIndex)
                        }
                    }
            ) {
                val n = bars.size.coerceAtLeast(1)
                val slot = size.width / n
                val gap = 2.dp.toPx()
                val w = (slot - gap).coerceAtLeast(1f)
                // A recessive baseline.
                drawLine(grid, Offset(0f, size.height), Offset(size.width, size.height), strokeWidth = 1.dp.toPx())
                bars.forEachIndexed { i, b ->
                    if (b.valueMs <= 0) return@forEachIndexed
                    val h = (b.valueMs.toFloat() / maxMs) * size.height
                    val x = i * slot + gap / 2
                    val r = minOf(4.dp.toPx(), w / 2, h)
                    // Rounded at the data end only, anchored to the baseline.
                    val path =
                        Path().apply {
                            addRoundRect(
                                androidx.compose.ui.geometry.RoundRect(
                                    left = x,
                                    top = size.height - h,
                                    right = x + w,
                                    bottom = size.height,
                                    topLeftCornerRadius = CornerRadius(r, r),
                                    topRightCornerRadius = CornerRadius(r, r)
                                )
                            )
                        }
                    drawPath(path, bar)
                    if (i == picked) drawRect(grid, Offset(x - gap / 2, 0f), Size(slot, size.height), style = Stroke(1.dp.toPx()))
                }
            }
            // Axis ticks, centred under their bars and free to overhang them.
            val measurer = rememberTextMeasurer()
            val tickStyle = MaterialTheme.typography.labelSmall.copy(color = MaterialTheme.colorScheme.onSurfaceVariant)
            Canvas(Modifier.fillMaxWidth().height(18.dp)) {
                val slot = size.width / bars.size.coerceAtLeast(1)
                bars.forEachIndexed { i, b ->
                    if (b.tick.isEmpty()) return@forEachIndexed
                    val t = measurer.measure(b.tick, tickStyle)
                    val x = (i * slot + slot / 2 - t.size.width / 2).coerceIn(0f, (size.width - t.size.width).coerceAtLeast(0f))
                    drawText(t, topLeft = Offset(x, 0f))
                }
            }
        }
    }
}
