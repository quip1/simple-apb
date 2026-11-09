// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.downloads

import android.text.format.Formatter
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.io.File
import javax.inject.Inject
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.simpleapb.app.player.Cover
import org.simpleapb.app.ui.ChoicePicker
import org.simpleapb.app.ui.ConfirmDialog
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.RadioGroup
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.Section
import org.simpleapb.app.ui.SwitchRow
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.db.DownloadRow
import org.simpleapb.core.data.download.DownloadError
import org.simpleapb.core.data.download.DownloadSettings
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.data.download.DownloadWorker
import org.simpleapb.core.data.download.Downloads
import org.simpleapb.core.data.download.StorageLocation
import org.simpleapb.core.ui.R as UiR

@HiltViewModel
class DownloadsViewModel
@Inject
constructor(private val downloads: Downloads) : ViewModel() {
    val rows: StateFlow<List<DownloadRow>> = downloads.all.stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())
    val settings: StateFlow<DownloadSettings?> = downloads.settings.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** Bytes on disk, finished or partial. */
    val used: StateFlow<Long> = downloads.all.combine(downloads.settings) { r, _ ->
        r.sumOf { it.doneBytes }
    }.stateIn(viewModelScope, SharingStarted.Eagerly, 0)

    val cardAvailable get() = downloads.cardRoot() != null

    fun remove(r: DownloadRow) = viewModelScope.launch { downloads.remove(r.accountId, r.bookId) }

    fun retry(r: DownloadRow) = viewModelScope.launch { downloads.enqueue(r.accountId, r.bookId, r.title, r.auto) }

    fun update(change: (DownloadSettings) -> DownloadSettings, reschedule: Boolean = false) = viewModelScope.launch {
        downloads.update(change)
        if (reschedule) downloads.reschedule()
    }
}

/** What a download row says about its state. */
@Composable
fun downloadStateText(r: DownloadRow, wifiOnly: Boolean): String {
    val ctx = LocalContext.current
    val pct = if (r.totalBytes > 0) ((r.doneBytes * 100) / r.totalBytes).toInt() else 0
    return when (r.state) {
        DownloadState.DONE -> stringResource(UiR.string.app_downloads_state_done, Formatter.formatShortFileSize(ctx, r.totalBytes))
        DownloadState.RUNNING -> stringResource(UiR.string.app_downloads_state_running, pct.toString())
        DownloadState.FAILED -> stringResource(UiR.string.app_downloads_state_failed, errorText(r.error))
        else -> stringResource(if (wifiOnly) UiR.string.app_downloads_state_wifi else UiR.string.app_downloads_state_queued)
    }
}

@Composable
private fun errorText(code: String?) = stringResource(
    when (code) {
        DownloadError.CAP -> UiR.string.app_downloads_error_cap
        DownloadError.SPACE -> UiR.string.app_downloads_error_space
        DownloadError.CHECKSUM -> UiR.string.app_downloads_error_checksum
        DownloadError.GONE -> UiR.string.app_downloads_error_gone
        DownloadError.NO_CARD -> UiR.string.app_downloads_error_no_card
        else -> UiR.string.app_downloads_error_other
    }
)

/** Downloads (brief §7.4 screen 6): the queue, storage used against the limit, and where and when to download. */
@Composable
fun DownloadsScreen(account: Account, onBook: (String) -> Unit) {
    val vm: DownloadsViewModel = hiltViewModel()
    val all by vm.rows.collectAsStateWithLifecycle()
    val loaded by vm.settings.collectAsStateWithLifecycle()
    val used by vm.used.collectAsStateWithLifecycle()
    val ctx = LocalContext.current
    var removing by remember { mutableStateOf<DownloadRow?>(null) }
    val s = loaded ?: return
    val rows = all.filter { it.accountId == account.id }
    FormColumn {
        ScreenHeader(stringResource(UiR.string.app_downloads_title))
        val usedText = Formatter.formatShortFileSize(ctx, used)
        if (s.capBytes > 0) {
            Text(stringResource(UiR.string.app_downloads_used, usedText, Formatter.formatShortFileSize(ctx, s.capBytes)))
            LinearProgressIndicator(progress = {
                (used.toFloat() / s.capBytes).coerceIn(0f, 1f)
            }, modifier = Modifier.fillMaxWidth(), drawStopIndicator = {})
        } else {
            Text(stringResource(UiR.string.app_downloads_used_nocap, usedText))
        }

        if (rows.isEmpty()) Text(stringResource(UiR.string.app_downloads_empty))
        Column {
            rows.forEach { r ->
                Row(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = TouchTarget)
                        .clickable(role = Role.Button) { onBook(r.bookId) }
                        .padding(vertical = 8.dp),
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    val cover = File(r.dir, DownloadWorker.COVER).takeIf { it.isFile }?.toUri()?.toString()
                    Cover(cover, Modifier.size(56.dp))
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text(r.title, style = MaterialTheme.typography.bodyLarge, maxLines = 2)
                        Text(downloadStateText(r, s.wifiOnly), style = MaterialTheme.typography.bodySmall)
                        if (r.auto) Text(stringResource(UiR.string.app_downloads_auto_badge), style = MaterialTheme.typography.labelSmall)
                        if (r.state == DownloadState.RUNNING && r.totalBytes > 0) {
                            LinearProgressIndicator(
                                progress = { r.doneBytes.toFloat() / r.totalBytes },
                                modifier = Modifier.fillMaxWidth(),
                                drawStopIndicator = {}
                            )
                        }
                    }
                    if (r.state == DownloadState.FAILED) {
                        IconButton(onClick = { vm.retry(r) }, modifier = Modifier.size(TouchTarget)) {
                            Icon(Icons.Filled.Refresh, contentDescription = stringResource(UiR.string.app_downloads_retry))
                        }
                    }
                    IconButton(onClick = { removing = r }, modifier = Modifier.size(TouchTarget)) {
                        Icon(Icons.Filled.Delete, contentDescription = stringResource(UiR.string.app_downloads_remove_named, r.title))
                    }
                }
                HorizontalDivider()
            }
        }

        Section(stringResource(UiR.string.app_downloads_settings)) {
            Text(stringResource(UiR.string.app_downloads_location), style = MaterialTheme.typography.labelLarge)
            val card = vm.cardAvailable
            RadioGroup(
                s.location,
                if (card || s.location == StorageLocation.SD_CARD) StorageLocation.entries else listOf(StorageLocation.INTERNAL),
                {
                    stringResource(
                        if (it ==
                            StorageLocation.SD_CARD
                        ) {
                            UiR.string.app_downloads_location_card
                        } else {
                            UiR.string.app_downloads_location_internal
                        }
                    )
                }
            ) { loc -> vm.update({ it.copy(location = loc) }) }
            Text(
                stringResource(UiR.string.app_downloads_location_help) + if (!card) " " + stringResource(UiR.string.app_downloads_no_card) else "",
                style = MaterialTheme.typography.bodySmall
            )
            SwitchRow(stringResource(UiR.string.app_downloads_wifi_only), s.wifiOnly, { on ->
                vm.update({ it.copy(wifiOnly = on) }, reschedule = true)
            })
            ChoicePicker(
                stringResource(UiR.string.app_downloads_cap),
                s.capBytes,
                (DownloadSettings.CAPS + s.capBytes).distinct(),
                { if (it == 0L) stringResource(UiR.string.app_downloads_cap_none) else Formatter.formatShortFileSize(ctx, it) },
                { cap -> vm.update({ it.copy(capBytes = cap) }) }
            )
            SwitchRow(
                stringResource(UiR.string.app_downloads_auto_next),
                s.autoNext,
                { on -> vm.update({ it.copy(autoNext = on) }) },
                help = stringResource(UiR.string.app_downloads_auto_next_help)
            )
        }
    }

    removing?.let { r ->
        ConfirmDialog(
            title = stringResource(UiR.string.app_downloads_remove),
            body = stringResource(UiR.string.app_downloads_remove_body, r.title),
            confirm = stringResource(UiR.string.app_downloads_remove),
            onConfirm = {
                vm.remove(r)
                removing = null
            },
            onDismiss = { removing = null }
        )
    }
}
