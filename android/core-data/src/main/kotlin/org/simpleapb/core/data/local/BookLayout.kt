// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data.local

/**
 * Which files on the device make up which book: the same rules as the
 * server's library scan (server/internal/library/detect.go, ADR-0015), so a
 * folder shows the same books here as after uploading it.
 *
 * - A folder with mp3 files, or with exactly one audio file, is one book.
 * - A folder with several m4b/m4a files holds one book per file (a series
 *   folder of m4bs).
 * - A folder with no audio of its own whose subfolders are all disc folders
 *   (CD1, Disc 2, Part 3…) is one book across them.
 * - Folders are looked at however deeply they nest (Author/Series/Book), and
 *   audio files directly in a picked folder are each a book.
 */
object BookLayout {
    /** A file or folder found under a picked folder. */
    data class Node(val id: String, val name: String, val isDir: Boolean, val isAudio: Boolean = false, val children: List<Node> = emptyList())

    sealed interface Book {
        /** A folder's files, in play order, as one book. */
        data class Folder(val dir: Node, val files: List<Node>) : Book

        /** A single file as a book. */
        data class File(val file: Node) : Book
    }

    private val DISC = Regex("""^(cd|disc|disk|part)[\s._-]*\d+$""", RegexOption.IGNORE_CASE)

    /** How deep folders are followed below a picked folder. */
    const val MAX_DEPTH = 8

    /** The books under a picked folder ([root]); children are in natural order. */
    fun plan(root: Node): List<Book> {
        val out = mutableListOf<Book>()
        // Loose files in the picked folder: each is its own book.
        root.children.filter { it.isAudio }.forEach { out += Book.File(it) }
        root.children.filter { it.isDir }.forEach { visit(it, out) }
        return out
    }

    private fun visit(dir: Node, out: MutableList<Book>) {
        val audio = dir.children.filter { it.isAudio }
        val subdirs = dir.children.filter { it.isDir }
        val discs = discs(audio, subdirs)
        if (discs != null) {
            out += Book.Folder(dir, discs.flatMap { d -> d.children.filter { it.isAudio } })
            // Other subfolders (without audio) can't hold books by the disc rule.
            return
        }
        if (audio.isNotEmpty()) {
            val hasMp3 = audio.any { it.name.endsWith(".mp3", ignoreCase = true) }
            if (hasMp3 || audio.size == 1) out += Book.Folder(dir, audio) else audio.forEach { out += Book.File(it) }
        }
        subdirs.forEach { visit(it, out) }
    }

    /** The disc folders, if this folder is a multi-disc book. */
    private fun discs(audio: List<Node>, subdirs: List<Node>): List<Node>? {
        if (audio.isNotEmpty() || subdirs.size < 2) return null
        val withAudio = subdirs.filter { s -> s.children.any { it.isAudio } }
        val allDiscs = withAudio.all { s -> DISC.matches(s.name) && s.children.none { it.isDir } }
        return withAudio.takeIf { allDiscs && it.size >= 2 }
    }
}
