// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.book

import android.Manifest
import android.content.pm.PackageManager
import android.icu.text.RelativeDateTimeFormatter
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DownloadDone
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.simpleapb.api.model.BookDetail
import org.simpleapb.api.model.Chapter
import org.simpleapb.api.model.Listener
import org.simpleapb.api.model.PersonRef
import org.simpleapb.api.model.TagRef
import org.simpleapb.app.downloads.downloadStateText
import org.simpleapb.app.library.PersonKind
import org.simpleapb.app.player.Cover
import org.simpleapb.app.player.clock
import org.simpleapb.app.player.duration
import org.simpleapb.app.ui.ConfirmDialog
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.Pager
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.SectionTitle
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.coverUrl
import org.simpleapb.app.ui.errorText
import org.simpleapb.app.ui.percent
import org.simpleapb.app.ui.seriesLabel
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.db.DownloadRow
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.data.net.millis
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

private enum class BookDialog { NONE, COLLECTIONS, RECOMMEND, REPORT, REMOVE }

/** "now", "3 minutes ago", "yesterday", in the phone's language (like the web's `ago`). */
fun ago(iso: String, now: Long = System.currentTimeMillis()): String {
    val diffS = (millis(iso) - now) / 1000
    val abs = kotlin.math.abs(diffS)
    val f = RelativeDateTimeFormatter.getInstance()
    if (abs < 60) return f.format(RelativeDateTimeFormatter.Direction.PLAIN, RelativeDateTimeFormatter.AbsoluteUnit.NOW)
    val (n, unit) =
        when {
            abs < 3_600 -> abs / 60 to RelativeDateTimeFormatter.RelativeUnit.MINUTES
            abs < 86_400 -> abs / 3_600 to RelativeDateTimeFormatter.RelativeUnit.HOURS
            else -> abs / 86_400 to RelativeDateTimeFormatter.RelativeUnit.DAYS
        }
    val dir = if (diffS < 0) RelativeDateTimeFormatter.Direction.LAST else RelativeDateTimeFormatter.Direction.NEXT
    return f.format(n.toDouble(), dir, unit)
}

@Composable
fun BookScreen(
    account: Account,
    bookId: String,
    onBack: () -> Unit,
    onPlayer: () -> Unit,
    onPerson: (PersonKind, String) -> Unit,
    onGenre: (TagRef) -> Unit,
    onEdit: () -> Unit
) {
    val vm: BookViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    val me by vm.me.collectAsStateWithLifecycle()
    val current by vm.current.collectAsStateWithLifecycle()
    val ui by vm.playback.collectAsStateWithLifecycle()
    val download by vm.download.collectAsStateWithLifecycle()
    var dialog by rememberSaveable { mutableStateOf(BookDialog.NONE) }
    LaunchedEffect(bookId) { vm.load(bookId) }

    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        val b = s.book
        ScreenHeader(b?.title ?: stringResource(UiR.string.common_loading), onBack = onBack)
        if (b == null) {
            s.error?.let { Notice(errorText(it)) }
            return@Column
        }
        val isCurrent = current?.id == b.id.toString()
        val positionMs = if (isCurrent) ui.positionMs else b.progress?.positionMs ?: 0
        val finished = b.progress?.finishedAt != null
        val started = positionMs > 0 && !finished

        Cover(
            coverUrl(account.serverUrl, b.coverUrl, b.hasCover, 512),
            Modifier.fillMaxWidth(0.7f).widthIn(max = 320.dp).align(Alignment.CenterHorizontally)
        )
        b.subtitle?.let { Text(it, style = MaterialTheme.typography.titleMedium) }
        Details(b, onPerson)

        if (finished) {
            Text(stringResource(UiR.string.book_finished), style = MaterialTheme.typography.titleSmall)
        } else if (started) {
            val pct = percent(positionMs, b.durationMs)
            LinearProgressIndicator(
                progress = { pct / 100f },
                modifier = Modifier.fillMaxWidth(),
                drawStopIndicator = {}
            )
            Text(stringResource(UiR.string.book_time_left, pct.toString(), duration(b.durationMs - positionMs)))
        }

        if (s.offline) Notice(stringResource(UiR.string.app_offline_notice), error = false)
        if (b.needsReview == true && b.canEdit == true) Notice(stringResource(UiR.string.meta_needs_review_banner), error = false)
        s.notice?.let { Notice(noticeText(it), error = false) }
        s.error?.let { Notice(errorText(it)) }

        if (b.removedAt != null) {
            Notice(stringResource(UiR.string.book_removed_banner), error = false)
            Button(onClick = vm::restore, enabled = !s.busy, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.book_restore))
            }
        } else {
            Actions(
                b = b,
                busy = s.busy,
                playLabel =
                stringResource(
                    when {
                        isCurrent && ui.playing -> UiR.string.book_open_player
                        started -> UiR.string.book_resume
                        else -> UiR.string.book_play
                    }
                ),
                canRecommend = BookViewModel.canRecommend(me),
                onPlay = {
                    if (!(isCurrent && ui.playing)) vm.play()
                    onPlayer()
                },
                onFinished = { vm.setFinished(!finished) },
                finished = finished,
                onDialog = {
                    if (it == BookDialog.COLLECTIONS) vm.loadCollections()
                    if (it == BookDialog.RECOMMEND) vm.resetRecommend()
                    dialog = it
                },
                onArchive = { vm.archive(b.hidden != true) },
                onEdit = onEdit,
                download = { DownloadButton(download, onDownload = vm::download, onRemove = vm::removeDownload) }
            )
        }

        b.description?.takeIf { it.isNotBlank() }?.let {
            SectionTitle(stringResource(UiR.string.book_about))
            Text(it)
        }
        if (b.tags.isNotEmpty()) Tags(b.tags, onGenre)
        if (b.chapters.isNotEmpty()) {
            Chapters(b.chapters, if (started || isCurrent) positionMs else -1) { ms ->
                vm.playFrom(ms)
                onPlayer()
            }
        }

        SectionTitle(stringResource(UiR.string.bookmarks_title))
        if (s.bookmarks.isEmpty()) Text(stringResource(UiR.string.bookmarks_none))
        s.bookmarks.forEach { m ->
            val time = clock(m.positionMs)
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(
                    Modifier
                        .weight(1f)
                        .heightIn(min = TouchTarget)
                        .clickable(role = Role.Button, onClickLabel = stringResource(UiR.string.bookmarks_jump, time)) {
                            vm.playFrom(m.positionMs)
                            onPlayer()
                        }.padding(vertical = 8.dp)
                ) {
                    Text(time, style = MaterialTheme.typography.bodyLarge)
                    m.note?.let { Text(it, style = MaterialTheme.typography.bodySmall) }
                }
                IconButton(onClick = { vm.deleteBookmark(m) }, modifier = Modifier.size(TouchTarget)) {
                    Icon(Icons.Filled.Delete, contentDescription = stringResource(UiR.string.app_book_bookmark_delete, time))
                }
            }
        }

        Reviews(vm, s, me)

        if (s.listeners.isNotEmpty()) Listeners(s.listeners)
    }

    val b = s.book ?: return
    val close = { dialog = BookDialog.NONE }
    when (dialog) {
        BookDialog.NONE -> Unit
        BookDialog.COLLECTIONS -> CollectionsDialog(b.id, s.collections, vm::toggleCollection, vm::createCollection, close)
        BookDialog.RECOMMEND -> RecommendDialog(b.title, s, vm::searchPeople, vm::recommend, close)
        BookDialog.REPORT -> ReportDialog(listOf(ReportTarget.Book(b.id, b.title)), onSend = vm::report, onClose = close)
        BookDialog.REMOVE ->
            ConfirmDialog(
                title = stringResource(UiR.string.book_remove_title),
                body = stringResource(UiR.string.book_remove_body),
                confirm = stringResource(UiR.string.book_remove),
                onConfirm = {
                    vm.remove()
                    close()
                },
                onDismiss = close
            )
    }
}

/** Download, its progress, or remove it (brief §7.4 screen 4). */
@Composable
private fun DownloadButton(d: DownloadRow?, onDownload: () -> Unit, onRemove: () -> Unit) {
    var confirm by rememberSaveable { mutableStateOf(false) }
    // Android 13+: the download's progress notification needs permission; downloading works either way.
    val context = LocalContext.current
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { onDownload() }
    val start = {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            ask.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            onDownload()
        }
    }
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        when (d?.state) {
            null -> OutlinedButton(onClick = start, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Icon(Icons.Filled.Download, contentDescription = null)
                Text(stringResource(UiR.string.app_book_download), modifier = Modifier.padding(start = 8.dp))
            }
            DownloadState.FAILED -> {
                Text(downloadStateText(d, wifiOnly = false), style = MaterialTheme.typography.bodySmall)
                OutlinedButton(onClick = start, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.app_downloads_retry))
                }
            }
            else -> {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (d.state ==
                        DownloadState.DONE
                    ) {
                        Icon(Icons.Filled.DownloadDone, contentDescription = null, modifier = Modifier.padding(end = 8.dp))
                    }
                    Text(
                        if (d.state == DownloadState.DONE) stringResource(UiR.string.app_book_downloaded) else downloadStateText(d, wifiOnly = false),
                        modifier = Modifier.weight(1f)
                    )
                    TextButton(onClick = { confirm = true }, modifier = Modifier.heightIn(min = TouchTarget)) {
                        Text(stringResource(if (d.state == DownloadState.DONE) UiR.string.app_downloads_remove else UiR.string.app_downloads_cancel))
                    }
                }
                if (d.state == DownloadState.RUNNING && d.totalBytes > 0) {
                    LinearProgressIndicator(progress = {
                        d.doneBytes.toFloat() / d.totalBytes
                    }, modifier = Modifier.fillMaxWidth(), drawStopIndicator = {})
                }
            }
        }
    }
    if (confirm && d != null) {
        ConfirmDialog(
            title = stringResource(UiR.string.app_downloads_remove),
            body = stringResource(UiR.string.app_downloads_remove_body, d.title),
            confirm = stringResource(UiR.string.app_downloads_remove),
            onConfirm = {
                onRemove()
                confirm = false
            },
            onDismiss = { confirm = false }
        )
    }
}

@Composable
private fun noticeText(n: BookNotice) = stringResource(
    when (n) {
        BookNotice.ARCHIVED -> UiR.string.book_archived_notice
        BookNotice.RESTORED -> UiR.string.book_restored_notice
        BookNotice.REMOVED -> UiR.string.book_removed_notice
        BookNotice.REVIEW_SAVED -> UiR.string.reviews_saved
        BookNotice.REPORT_SENT -> UiR.string.report_sent
        BookNotice.DETAILS_SAVED -> UiR.string.meta_saved
    }
)

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun People(label: String, people: List<PersonRef>, onClick: (PersonRef) -> Unit) {
    Column {
        Text(label, style = MaterialTheme.typography.labelMedium)
        FlowRow {
            people.forEach { p ->
                TextButton(onClick = { onClick(p) }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(p.name) }
            }
        }
    }
}

@Composable
private fun Details(b: BookDetail, onPerson: (PersonKind, String) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        if (b.authors.isNotEmpty()) People(stringResource(UiR.string.book_by), b.authors) { onPerson(PersonKind.AUTHOR, it.id.toString()) }
        if (b.narrators.isNotEmpty()) {
            People(stringResource(UiR.string.book_narrated_by), b.narrators) {
                onPerson(PersonKind.NARRATOR, it.id.toString())
            }
        }
        b.series?.let { s ->
            Text(stringResource(UiR.string.book_series), style = MaterialTheme.typography.labelMedium)
            TextButton(onClick = {
                onPerson(PersonKind.SERIES, s.id.toString())
            }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(seriesLabel(s)) }
        }
        Text(stringResource(UiR.string.book_length) + ": " + duration(b.durationMs))
        b.publishedYear?.let { y ->
            Text(stringResource(UiR.string.book_published) + ": " + (b.publisher?.let { "$it, $y" } ?: y.toString()))
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Actions(
    b: BookDetail,
    busy: Boolean,
    playLabel: String,
    canRecommend: Boolean,
    finished: Boolean,
    onPlay: () -> Unit,
    onFinished: () -> Unit,
    onDialog: (BookDialog) -> Unit,
    onArchive: () -> Unit,
    onEdit: () -> Unit,
    download: @Composable () -> Unit
) {
    Button(onClick = onPlay, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
        Icon(Icons.Filled.PlayArrow, contentDescription = null)
        Text(playLabel, modifier = Modifier.padding(start = 8.dp))
    }
    download()
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        val m = Modifier.heightIn(min = TouchTarget)
        OutlinedButton(onClick = onFinished, enabled = !busy, modifier = m) {
            Text(stringResource(if (finished) UiR.string.book_mark_unfinished else UiR.string.book_mark_finished))
        }
        OutlinedButton(onClick = { onDialog(BookDialog.COLLECTIONS) }, modifier = m) { Text(stringResource(UiR.string.book_add_to_collection)) }
        OutlinedButton(onClick = onArchive, enabled = !busy, modifier = m) {
            Text(stringResource(if (b.hidden == true) UiR.string.book_unarchive else UiR.string.book_archive))
        }
        if (canRecommend) {
            OutlinedButton(onClick = {
                onDialog(BookDialog.RECOMMEND)
            }, modifier = m) { Text(stringResource(UiR.string.book_recommend)) }
        }
        if (b.canEdit == true) OutlinedButton(onClick = onEdit, modifier = m) { Text(stringResource(UiR.string.book_edit)) }
        TextButton(onClick = { onDialog(BookDialog.REMOVE) }, modifier = m) { Text(stringResource(UiR.string.book_remove)) }
        if (canRecommend) TextButton(onClick = { onDialog(BookDialog.REPORT) }, modifier = m) { Text(stringResource(UiR.string.report_action)) }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Tags(tags: List<TagRef>, onGenre: (TagRef) -> Unit) {
    SectionTitle(stringResource(UiR.string.book_tags))
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        tags.forEach { t -> AssistChip(onClick = { onGenre(t) }, label = { Text(t.name) }, modifier = Modifier.heightIn(min = TouchTarget)) }
    }
}

/** The chapter list; on e-ink it moves by page (brief §11). */
@Composable
private fun Chapters(chapters: List<Chapter>, positionMs: Long, onPick: (Long) -> Unit) {
    val eink = LocalEink.current
    val currentIndex = if (positionMs < 0) -1 else chapters.indexOfLast { it.startMs <= positionMs }
    var page by rememberSaveable { mutableIntStateOf(if (currentIndex >= 0) currentIndex / CHAPTER_PAGE + 1 else 1) }
    val pages = (chapters.size + CHAPTER_PAGE - 1) / CHAPTER_PAGE
    SectionTitle(pluralStringResource(UiR.plurals.chapters_title, chapters.size, chapters.size))
    val shown = if (eink) chapters.drop((page - 1) * CHAPTER_PAGE).take(CHAPTER_PAGE) else chapters
    Column {
        shown.forEach { c ->
            val isCurrent = chapters.indexOf(c) == currentIndex
            Row(
                Modifier
                    .fillMaxWidth()
                    .heightIn(min = TouchTarget)
                    .clickable(role = Role.Button, onClick = { onPick(c.startMs) })
                    .padding(vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    c.title,
                    modifier = Modifier.weight(1f),
                    fontWeight = if (isCurrent) FontWeight.Bold else FontWeight.Normal
                )
                Text(clock(c.startMs), style = MaterialTheme.typography.bodySmall)
            }
            HorizontalDivider()
        }
    }
    if (eink) Pager(page, page > 1, page < pages, { page-- }, { page++ })
}

@Composable
private fun Listeners(listeners: List<Listener>) {
    SectionTitle(stringResource(UiR.string.listeners_title))
    listeners.forEach { l ->
        Column {
            Text(l.user.displayName, style = MaterialTheme.typography.bodyLarge)
            Text(
                if (l.state ==
                    Listener.State.FINISHED
                ) {
                    stringResource(UiR.string.listeners_finished)
                } else {
                    stringResource(UiR.string.listeners_listening, ago(l.updatedAt))
                },
                style = MaterialTheme.typography.bodySmall
            )
        }
    }
}

private const val CHAPTER_PAGE = 15
