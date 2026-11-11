// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.local

import android.content.Context
import android.content.Intent
import android.database.ContentObserver
import android.media.MediaMetadataRetriever
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import androidx.core.net.toUri
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringSetPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.File
import java.security.MessageDigest
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import org.simpleapb.core.data.db.LocalBookRow
import org.simpleapb.core.data.db.MirrorDao
import org.simpleapb.core.data.net.AppScope

private val Context.localStore: DataStore<Preferences> by preferencesDataStore(name = "local_folders")

/** One audio file of a book on this device. */
@Serializable
data class LocalFile(val uri: String, val name: String, val mimeType: String, val sizeBytes: Long, val durationMs: Long)

/** A book found in a folder on this device. */
data class LocalBook(val row: LocalBookRow, val files: List<LocalFile>) {
    val id get() = row.id
    val title get() = row.title
}

/**
 * Folders on this device (brief §7.4 screen 9): the user picks folders with
 * the system picker (no storage permission), and each subfolder is a book.
 * An audio file lying directly in a picked folder is a book of its own.
 * Folders are rescanned when the app opens, on request, and when Android
 * reports a change (not every storage provider does).
 */
@Singleton
class LocalFolders
@Inject
constructor(
    @ApplicationContext private val context: Context,
    private val dao: MirrorDao,
    @AppScope private val scope: CoroutineScope
) {
    private val store = context.localStore
    private val foldersKey = stringSetPreferencesKey("trees")
    private val json = Json { ignoreUnknownKeys = true }
    private val lock = Mutex()
    private val _scanning = MutableStateFlow(false)

    /** A scan is running. */
    val scanning: StateFlow<Boolean> = _scanning

    /** The picked folders (tree URIs). */
    val folders: Flow<List<String>> = store.data.map { it[foldersKey].orEmpty().sorted() }

    val books: Flow<List<LocalBook>> = dao.localBooks().map { rows -> rows.map { book(it) } }

    suspend fun book(id: String): LocalBook? = dao.localBook(id)?.let { book(it) }

    private fun book(r: LocalBookRow) = LocalBook(
        r,
        runCatching {
            json.decodeFromString(ListSerializer(LocalFile.serializer()), r.files)
        }.getOrDefault(emptyList())
    )

    /** Adds a folder from the system picker (keeps read access across restarts) and scans it. */
    suspend fun add(tree: Uri) {
        context.contentResolver.takePersistableUriPermission(tree, Intent.FLAG_GRANT_READ_URI_PERMISSION)
        store.edit { it[foldersKey] = it[foldersKey].orEmpty() + tree.toString() }
        scan()
        observe()
    }

    /** Forgets a folder and its books (the files stay where they are). */
    suspend fun remove(tree: String) {
        store.edit { it[foldersKey] = it[foldersKey].orEmpty() - tree }
        runCatching { context.contentResolver.releasePersistableUriPermission(tree.toUri(), Intent.FLAG_GRANT_READ_URI_PERMISSION) }
        dao.deleteLocalBooks(dao.allLocalBooks().filter { it.treeUri == tree }.map { it.id })
    }

    private var lastScan = 0L

    /** Rescans when the app opens, at most once a minute. */
    fun scanSoon() {
        if (System.currentTimeMillis() - lastScan < RESCAN_MS) return
        scope.launch { scan() }
    }

    /** Finds the books in every folder; unchanged folders are skipped. */
    suspend fun scan() = lock.withLock {
        _scanning.value = true
        lastScan = System.currentTimeMillis()
        try {
            withContext(Dispatchers.IO) {
                val trees = folders.first()
                val known = dao.allLocalBooks().associateBy { it.id }
                val found = mutableListOf<LocalBookRow>()
                for (tree in trees) {
                    val treeUri = tree.toUri()
                    val root = runCatching { DocumentsContract.getTreeDocumentId(treeUri) }.getOrNull() ?: continue
                    for (child in children(treeUri, root)) {
                        val row =
                            when {
                                child.isDir -> folderBook(treeUri, tree, child, known)
                                isAudio(child.name, child.mime) -> fileBook(treeUri, tree, child, known)
                                else -> null
                            }
                        if (row != null) found += row
                    }
                }
                dao.upsertLocalBooks(found)
                // Books whose folder disappeared (or whose tree was forgotten) go.
                val keep = found.map { it.id }.toSet()
                dao.deleteLocalBooks(known.keys.filter { it !in keep && known[it]!!.treeUri in trees })
            }
        } finally {
            _scanning.value = false
        }
    }

    private data class Entry(val docId: String, val name: String, val mime: String, val size: Long, val modified: Long) {
        val isDir get() = mime == Document.MIME_TYPE_DIR
    }

    private fun children(tree: Uri, docId: String): List<Entry> {
        val uri = DocumentsContract.buildChildDocumentsUriUsingTree(tree, docId)
        val cols =
            arrayOf(
                Document.COLUMN_DOCUMENT_ID,
                Document.COLUMN_DISPLAY_NAME,
                Document.COLUMN_MIME_TYPE,
                Document.COLUMN_SIZE,
                Document.COLUMN_LAST_MODIFIED
            )
        val out = mutableListOf<Entry>()
        runCatching {
            context.contentResolver.query(uri, cols, null, null, null)?.use { c ->
                while (c.moveToNext()) {
                    out +=
                        Entry(
                            c.getString(0),
                            c.getString(1).orEmpty(),
                            c.getString(2).orEmpty(),
                            if (c.isNull(3)) 0 else c.getLong(3),
                            if (c.isNull(4)) 0 else c.getLong(4)
                        )
                }
            }
        }
        return out.sortedWith(compareBy(NaturalOrder) { it.name })
    }

    /** Audio files of a book folder, in order, one level of subfolders included (CD1, CD2…). */
    private fun audioFiles(tree: Uri, docId: String, depth: Int = 0): List<Entry> = children(tree, docId).flatMap { e ->
        when {
            e.isDir && depth < 1 -> audioFiles(tree, e.docId, depth + 1)
            isAudio(e.name, e.mime) -> listOf(e)
            else -> emptyList()
        }
    }

    private fun folderBook(tree: Uri, treeStr: String, dir: Entry, known: Map<String, LocalBookRow>): LocalBookRow? {
        val audio = audioFiles(tree, dir.docId)
        if (audio.isEmpty()) return null
        val folderUri = DocumentsContract.buildDocumentUriUsingTree(tree, dir.docId).toString()
        val id = idFor(folderUri)
        val modified = maxOf(dir.modified, audio.maxOf { it.modified })
        known[id]?.takeIf { it.modified == modified && it.files.isNotEmpty() }?.let { return it }
        val files = audio.map { e -> fileOf(tree, e) }
        val cover = coverFor(id, tree, dir.docId, files.first().uri)
        val tags = tags(files.first().uri)
        return LocalBookRow(
            id = id,
            treeUri = treeStr,
            folderUri = folderUri,
            title = tags.album ?: dir.name,
            author = tags.artist,
            durationMs = files.sumOf { it.durationMs },
            sizeBytes = files.sumOf { it.sizeBytes },
            files = json.encodeToString(ListSerializer(LocalFile.serializer()), files),
            coverPath = cover,
            modified = modified,
            uploadAccountId = known[id]?.uploadAccountId,
            uploadUrl = known[id]?.uploadUrl,
            uploadState = known[id]?.uploadState,
            uploadError = known[id]?.uploadError,
            uploadBookId = known[id]?.uploadBookId
        )
    }

    private fun fileBook(tree: Uri, treeStr: String, e: Entry, known: Map<String, LocalBookRow>): LocalBookRow {
        val uri = DocumentsContract.buildDocumentUriUsingTree(tree, e.docId).toString()
        val id = idFor(uri)
        known[id]?.takeIf { it.modified == e.modified && it.files.isNotEmpty() }?.let { return it }
        val f = fileOf(tree, e)
        val tags = tags(f.uri)
        return LocalBookRow(
            id = id,
            treeUri = treeStr,
            folderUri = uri,
            title = tags.album ?: tags.title ?: e.name.substringBeforeLast('.'),
            author = tags.artist,
            durationMs = f.durationMs,
            sizeBytes = f.sizeBytes,
            files = json.encodeToString(ListSerializer(LocalFile.serializer()), listOf(f)),
            coverPath = coverFor(id, null, null, f.uri),
            modified = e.modified,
            uploadAccountId = known[id]?.uploadAccountId,
            uploadUrl = known[id]?.uploadUrl,
            uploadState = known[id]?.uploadState,
            uploadError = known[id]?.uploadError,
            uploadBookId = known[id]?.uploadBookId
        )
    }

    private fun fileOf(tree: Uri, e: Entry): LocalFile {
        val uri = DocumentsContract.buildDocumentUriUsingTree(tree, e.docId)
        val duration = withRetriever(uri) { it.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull() } ?: 0
        return LocalFile(uri.toString(), e.name, e.mime.ifEmpty { mimeFor(e.name) }, e.size, duration)
    }

    private data class Tags(val title: String?, val album: String?, val artist: String?)

    private fun tags(uri: String): Tags = withRetriever(uri.toUri()) { r ->
        Tags(
            r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_TITLE)?.trim()?.ifEmpty { null },
            r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_ALBUM)?.trim()?.ifEmpty { null },
            (
                r.extractMetadata(
                    MediaMetadataRetriever.METADATA_KEY_ALBUMARTIST
                ) ?: r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_ARTIST)
                )?.trim()?.ifEmpty {
                null
            }
        )
    } ?: Tags(null, null, null)

    /** The cover: a picture embedded in the first file, or cover/folder.jpg in the folder; copied into the app. */
    private fun coverFor(id: String, tree: Uri?, dirDoc: String?, firstFile: String): String? {
        val out = File(File(context.filesDir, "local-covers"), id.removePrefix("local:") + ".jpg")
        val embedded = withRetriever(firstFile.toUri()) { it.embeddedPicture }
        val bytes =
            embedded ?: if (tree != null && dirDoc != null) {
                children(tree, dirDoc).firstOrNull { it.name.lowercase() in COVER_NAMES }?.let { e ->
                    runCatching {
                        context.contentResolver.openInputStream(DocumentsContract.buildDocumentUriUsingTree(tree, e.docId))?.use { it.readBytes() }
                    }.getOrNull()
                }
            } else {
                null
            }
        if (bytes == null) return null
        return runCatching {
            out.parentFile?.mkdirs()
            val tmp = File(out.path + ".tmp")
            tmp.outputStream().use {
                it.write(bytes)
                it.fd.sync()
            }
            tmp.renameTo(out)
            out.path
        }.getOrNull()
    }

    private fun <T> withRetriever(uri: Uri, block: (MediaMetadataRetriever) -> T?): T? {
        val r = MediaMetadataRetriever()
        return try {
            r.setDataSource(context, uri)
            block(r)
        } catch (_: Exception) {
            null
        } finally {
            runCatching { r.release() }
        }
    }

    private var observers = emptyList<ContentObserver>()
    private var pending: Job? = null

    /** Rescans after Android reports a change in a picked folder (where the storage provider reports them). */
    suspend fun observe() {
        val trees = folders.first()
        withContext(Dispatchers.Main) {
            observers.forEach { context.contentResolver.unregisterContentObserver(it) }
            observers =
                trees.mapNotNull { tree ->
                    val uri = tree.toUri()
                    val root = runCatching { DocumentsContract.getTreeDocumentId(uri) }.getOrNull() ?: return@mapNotNull null
                    val o =
                        object : ContentObserver(Handler(Looper.getMainLooper())) {
                            override fun onChange(selfChange: Boolean) {
                                pending?.cancel()
                                pending =
                                    scope.launch {
                                        delay(CHANGE_SETTLE_MS)
                                        scan()
                                    }
                            }
                        }
                    runCatching {
                        context.contentResolver.registerContentObserver(DocumentsContract.buildChildDocumentsUriUsingTree(uri, root), true, o)
                    }.getOrNull()
                        ?: return@mapNotNull null
                    o
                }
        }
    }

    companion object {
        const val PREFIX = "local:"
        private const val RESCAN_MS = 60_000L
        private const val CHANGE_SETTLE_MS = 2_000L
        private val COVER_NAMES = setOf("cover.jpg", "cover.jpeg", "cover.png", "folder.jpg", "folder.png", "front.jpg")
        private val AUDIO_EXT = setOf("mp3", "m4b", "m4a", "aac", "ogg", "opus", "oga", "flac", "wav")

        fun isLocal(bookId: String) = bookId.startsWith(PREFIX)

        fun isAudio(name: String, mime: String): Boolean = mime.startsWith("audio/") || name.substringAfterLast('.', "").lowercase() in AUDIO_EXT

        fun mimeFor(name: String): String = when (name.substringAfterLast('.', "").lowercase()) {
            "mp3" -> "audio/mpeg"
            "m4b", "m4a", "aac" -> "audio/mp4"
            "ogg", "oga", "opus" -> "audio/ogg"
            "flac" -> "audio/flac"
            "wav" -> "audio/wav"
            else -> "application/octet-stream"
        }

        /** A stable id for a folder (or a single file), so progress survives rescans. */
        fun idFor(documentUri: String): String {
            val d = MessageDigest.getInstance("SHA-256").digest(documentUri.toByteArray())
            return PREFIX + d.take(12).joinToString("") { "%02x".format(it) }
        }

        /** "Part 2" before "Part 10". */
        val NaturalOrder: Comparator<String> =
            Comparator { a, b ->
                val re = Regex("\\d+|\\D+")
                val pa = re.findAll(a.lowercase()).map { it.value }.toList()
                val pb = re.findAll(b.lowercase()).map { it.value }.toList()
                for (i in 0 until minOf(pa.size, pb.size)) {
                    val x = pa[i]
                    val y = pb[i]
                    val c =
                        if (x[0].isDigit() && y[0].isDigit()) {
                            x.trimStart('0').length.compareTo(y.trimStart('0').length).takeIf { it != 0 }
                                ?: x.trimStart('0').compareTo(y.trimStart('0'))
                        } else {
                            x.compareTo(y)
                        }
                    if (c != 0) return@Comparator c
                }
                pa.size.compareTo(pb.size)
            }
    }
}
