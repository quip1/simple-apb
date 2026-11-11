// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.local

import android.content.Context
import android.net.Uri
import android.provider.DocumentsContract
import android.text.format.Formatter
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CloudUpload
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.PhoneAndroid
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.File
import javax.inject.Inject
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.simpleapb.api.model.Me
import org.simpleapb.api.model.UserRole
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.player.Cover
import org.simpleapb.app.player.duration
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.Section
import org.simpleapb.app.ui.SectionTitle
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.percent
import org.simpleapb.app.ui.shelfCardWidth
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.db.MirrorDao
import org.simpleapb.core.data.local.LocalBook
import org.simpleapb.core.data.local.LocalFolders
import org.simpleapb.core.data.local.UploadError
import org.simpleapb.core.data.local.UploadState
import org.simpleapb.core.data.local.UploadWorker
import org.simpleapb.core.player.PlaybackStore
import org.simpleapb.core.player.PlayerConnection
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

@HiltViewModel
class LocalViewModel
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val folders: LocalFolders,
    private val dao: MirrorDao,
    private val store: PlaybackStore,
    private val player: PlayerConnection,
    session: MeSession
) : ViewModel() {
    val books: StateFlow<List<LocalBook>> = folders.books.stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())
    val trees: StateFlow<List<String>> = folders.folders.stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())
    val scanning: StateFlow<Boolean> = folders.scanning
    val me: StateFlow<Me?> = session.me

    /** How far each book is (0–100), from this device's saved positions. */
    val progress: StateFlow<Map<String, Int>> =
        books
            .map { list -> list.associate { b -> b.id to percent(store.localPosition(b.id)?.positionMs ?: 0, b.row.durationMs) } }
            .stateIn(viewModelScope, SharingStarted.Eagerly, emptyMap())

    fun add(uri: Uri) = viewModelScope.launch { folders.add(uri) }

    fun remove(tree: String) = viewModelScope.launch { folders.remove(tree) }

    fun rescan() = viewModelScope.launch { folders.scan() }

    fun play(id: String) = player.play(id)

    fun upload(id: String, account: Account) = viewModelScope.launch { UploadWorker.enqueue(context, dao, id, account.id) }
}

/** A picked folder's name, from its tree address ("primary:Audiobooks" → "Audiobooks"). */
fun folderName(tree: String): String = runCatching { DocumentsContract.getTreeDocumentId(tree.toUri()) }
    .getOrNull()
    ?.substringAfter(':')
    ?.ifEmpty { null } ?: tree

fun coverOf(b: LocalBook): String? = b.row.coverPath?.let { File(it).toUri().toString() }

/** The "on this device" mark on covers (never colour alone: an icon, and spoken). */
@Composable
fun OnDeviceBadge(modifier: Modifier = Modifier) {
    Icon(
        Icons.Filled.PhoneAndroid,
        contentDescription = null,
        tint = MaterialTheme.colorScheme.onSurface,
        modifier =
        modifier
            .background(MaterialTheme.colorScheme.surface, CircleShape)
            .border(1.dp, MaterialTheme.colorScheme.onSurface, CircleShape)
            .padding(3.dp)
            .size(16.dp)
    )
}

/** A book on this device as a cover card (in the library grid). */
@Composable
fun LocalBookCard(b: LocalBook, pct: Int, onClick: () -> Unit, modifier: Modifier = Modifier, width: Dp = Dp.Unspecified) {
    val label = listOfNotNull(
        b.title,
        b.row.author,
        stringResource(UiR.string.app_local_title),
        if (pct >
            0
        ) {
            stringResource(UiR.string.book_progress_label, pct.toString())
        } else {
            null
        }
    ).joinToString(", ")
    Column(
        modifier
            .then(if (width == Dp.Unspecified) Modifier.fillMaxWidth() else Modifier.width(width))
            .clickable(role = Role.Button, onClick = onClick)
            .clearAndSetSemantics {
                contentDescription = label
                role = Role.Button
            }.padding(bottom = 4.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        Box {
            Cover(coverOf(b), Modifier.fillMaxWidth())
            OnDeviceBadge(Modifier.align(Alignment.TopStart).padding(6.dp))
        }
        Text(b.title, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
        b.row.author?.let { Text(it, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis) }
    }
}

/** A book on this device as a list row. */
@Composable
fun LocalBookRow(b: LocalBook, pct: Int, onClick: () -> Unit) {
    val label = listOfNotNull(
        b.title,
        b.row.author,
        stringResource(UiR.string.app_local_title),
        if (pct >
            0
        ) {
            stringResource(UiR.string.book_progress_label, pct.toString())
        } else {
            null
        }
    ).joinToString(", ")
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = TouchTarget)
            .clickable(role = Role.Button, onClick = onClick)
            .clearAndSetSemantics {
                contentDescription = label
                role = Role.Button
            }.padding(vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Box {
            Cover(coverOf(b), Modifier.size(64.dp))
            OnDeviceBadge(Modifier.align(Alignment.TopStart).padding(2.dp))
        }
        Column(Modifier.weight(1f)) {
            Text(b.title, style = MaterialTheme.typography.bodyLarge, maxLines = 2, overflow = TextOverflow.Ellipsis)
            b.row.author?.let { Text(it, style = MaterialTheme.typography.bodySmall) }
            Text(
                listOf(duration(b.row.durationMs), stringResource(UiR.string.app_local_title)).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
            if (pct > 0) Text(stringResource(UiR.string.book_progress_label, pct.toString()), style = MaterialTheme.typography.labelMedium)
        }
    }
}

/** "On this device" on Home: a row of the books in picked folders (none: nothing shown). */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun LocalShelf(onBook: (String) -> Unit) {
    val vm: LocalViewModel = hiltViewModel()
    val books by vm.books.collectAsStateWithLifecycle()
    val progress by vm.progress.collectAsStateWithLifecycle()
    if (books.isEmpty()) return
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        SectionTitle(stringResource(UiR.string.app_local_title))
        if (LocalEink.current) {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                books.take(6).forEach { b -> LocalBookCard(b, progress[b.id] ?: 0, { onBook(b.id) }, width = shelfCardWidth(eink = true)) }
            }
        } else {
            LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                items(books, key = { it.id }) { b -> LocalBookCard(b, progress[b.id] ?: 0, { onBook(b.id) }, width = shelfCardWidth(eink = false)) }
            }
        }
    }
}

/** Folders on this device (brief §7.4 screen 9): add with the system picker, remove, look for changes. */
@Composable
fun FoldersScreen(onBack: () -> Unit) {
    val vm: LocalViewModel = hiltViewModel()
    val trees by vm.trees.collectAsStateWithLifecycle()
    val books by vm.books.collectAsStateWithLifecycle()
    val scanning by vm.scanning.collectAsStateWithLifecycle()
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri -> if (uri != null) vm.add(uri) }
    FormColumn {
        ScreenHeader(stringResource(UiR.string.app_local_folders), onBack = onBack) {
            IconButton(onClick = vm::rescan, enabled = !scanning, modifier = Modifier.size(TouchTarget)) {
                Icon(Icons.Filled.Refresh, contentDescription = stringResource(UiR.string.app_local_rescan))
            }
        }
        Text(stringResource(UiR.string.app_local_folders_help))
        if (scanning) Text(stringResource(UiR.string.app_local_scanning), modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite })
        if (trees.isEmpty()) Text(stringResource(UiR.string.app_local_none))
        trees.forEach { t ->
            val name = folderName(t)
            val count = books.count { it.row.treeUri == t }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(name, style = MaterialTheme.typography.bodyLarge)
                    Text(pluralStringResource(UiR.plurals.library_book_count, count, count), style = MaterialTheme.typography.bodySmall)
                }
                IconButton(onClick = { vm.remove(t) }, modifier = Modifier.size(TouchTarget)) {
                    Icon(Icons.Filled.Delete, contentDescription = stringResource(UiR.string.app_local_remove, name))
                }
            }
            HorizontalDivider()
        }
        Button(onClick = { pick.launch(null) }, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.app_local_add))
        }
    }
}

@Composable
private fun uploadError(code: String?) = when (code) {
    UploadError.FORBIDDEN -> stringResource(UiR.string.app_local_upload_error_forbidden)
    UploadError.QUOTA -> stringResource(UiR.string.app_local_upload_error_quota)
    UploadError.UNSUPPORTED -> stringResource(UiR.string.app_local_upload_error_unsupported)
    UploadError.REJECTED, null -> stringResource(UiR.string.app_local_upload_error_rejected)
    UploadError.OTHER -> stringResource(UiR.string.error_generic)
    else -> code // the server's own explanation of a failed import
}

/**
 * A book on this device: play it, or upload it to the active server
 * (when the account may upload). The files are never changed or deleted.
 */
@Composable
fun LocalBookScreen(id: String, account: Account?, onBack: () -> Unit, onPlayer: () -> Unit) {
    val vm: LocalViewModel = hiltViewModel()
    val books by vm.books.collectAsStateWithLifecycle()
    val progress by vm.progress.collectAsStateWithLifecycle()
    val me by vm.me.collectAsStateWithLifecycle()
    val ctx = LocalContext.current
    val b = books.firstOrNull { it.id == id }
    FormColumn {
        ScreenHeader(b?.title ?: stringResource(UiR.string.common_loading), onBack = onBack)
        if (b == null) return@FormColumn
        Cover(coverOf(b), Modifier.fillMaxWidth(0.7f).widthIn(max = 320.dp).align(Alignment.CenterHorizontally))
        b.row.author?.let { Text(it, style = MaterialTheme.typography.titleMedium) }
        Text(
            listOf(
                duration(b.row.durationMs),
                pluralStringResource(UiR.plurals.app_local_files, b.files.size, b.files.size),
                Formatter.formatShortFileSize(ctx, b.row.sizeBytes)
            ).joinToString(" · ")
        )
        Text(stringResource(UiR.string.app_local_in_folder, folderName(b.row.treeUri)), style = MaterialTheme.typography.bodySmall)
        val pct = progress[b.id] ?: 0
        if (pct > 0) {
            LinearProgressIndicator(progress = { pct / 100f }, modifier = Modifier.fillMaxWidth(), drawStopIndicator = {})
            Text(stringResource(UiR.string.book_progress_label, pct.toString()))
        }
        Button(
            onClick = {
                vm.play(b.id)
                onPlayer()
            },
            modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)
        ) {
            Icon(Icons.Filled.PlayArrow, contentDescription = null)
            Text(stringResource(if (pct > 0) UiR.string.book_resume else UiR.string.book_play), modifier = Modifier.padding(start = 8.dp))
        }

        // Upload to the server (brief §7.4 screen 9), for accounts that may upload.
        val canUpload = account != null && (me?.role == UserRole.ADMIN || me?.permissions?.canUpload == true)
        val forThis = b.row.uploadAccountId == account?.id
        val state = b.row.uploadState.takeIf { forThis }
        if (account != null && (canUpload || state != null)) {
            Section(stringResource(UiR.string.app_local_upload_title)) {
                Column(Modifier.semantics { liveRegion = LiveRegionMode.Polite }) {
                    when (state) {
                        UploadState.QUEUED -> Text(stringResource(UiR.string.app_local_upload_queued))
                        UploadState.SENDING -> Text(stringResource(UiR.string.app_local_upload_sending))
                        UploadState.PROCESSING -> Text(stringResource(UiR.string.app_local_upload_processing, account.serverName))
                        UploadState.DONE -> Text(stringResource(UiR.string.app_local_upload_done, account.serverName))
                        UploadState.FAILED -> Notice(stringResource(UiR.string.app_local_upload_failed, uploadError(b.row.uploadError)))
                        else -> Text(stringResource(UiR.string.app_local_upload_help, account.serverName))
                    }
                }
                if (canUpload &&
                    state != UploadState.QUEUED &&
                    state != UploadState.SENDING &&
                    state != UploadState.PROCESSING &&
                    state != UploadState.DONE
                ) {
                    OutlinedButton(onClick = { vm.upload(b.id, account) }, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                        Icon(Icons.Filled.CloudUpload, contentDescription = null)
                        Text(stringResource(UiR.string.app_local_upload, account.serverName), modifier = Modifier.padding(start = 8.dp))
                    }
                }
            }
        }
        if (LocalEink.current) HorizontalDivider()
    }
}
