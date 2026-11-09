// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

/** One audio file of a book, placed on the book's timeline. */
data class TimelineFile(val id: String, val offsetMs: Long, val durationMs: Long, val mimeType: String)

data class TimelineChapter(val index: Int, val title: String, val startMs: Long, val endMs: Long)

/** A position inside one file: the playlist index and the offset in that file. */
data class FilePosition(val index: Int, val localMs: Long)

/**
 * A book's single timeline over its files (positions are always book-global,
 * brief §6.8). The player plays the files as a playlist; this maps between
 * the two and answers chapter questions.
 */
class BookTimeline(files: List<TimelineFile>, chapters: List<TimelineChapter>) {
    val files: List<TimelineFile> = files.sortedBy { it.offsetMs }
    val chapters: List<TimelineChapter> = chapters.sortedBy { it.startMs }
    val durationMs: Long = this.files.lastOrNull()?.let { it.offsetMs + it.durationMs } ?: 0

    /** Global position → (file, offset in file), clamped to the book. */
    fun locate(globalMs: Long): FilePosition {
        if (files.isEmpty()) return FilePosition(0, 0)
        val ms = globalMs.coerceIn(0, durationMs)
        val i = files.indexOfLast { it.offsetMs <= ms }.coerceAtLeast(0)
        val f = files[i]
        return FilePosition(i, (ms - f.offsetMs).coerceIn(0, f.durationMs))
    }

    fun global(index: Int, localMs: Long): Long = (files.getOrNull(index)?.offsetMs ?: 0) + localMs.coerceAtLeast(0)

    fun chapterAt(globalMs: Long): TimelineChapter? = chapters.lastOrNull { it.startMs <= globalMs } ?: chapters.firstOrNull()

    /**
     * "Previous chapter": the start of the current chapter, or of the one
     * before when we are within [graceMs] of the current start.
     */
    fun previousChapterStart(globalMs: Long, graceMs: Long = 3_000): Long {
        val i = chapters.indexOfLast { it.startMs <= globalMs }
        if (i < 0) return 0
        val cur = chapters[i]
        return if (globalMs - cur.startMs < graceMs && i > 0) chapters[i - 1].startMs else cur.startMs
    }

    /** The start of the next chapter, or null in the last chapter. */
    fun nextChapterStart(globalMs: Long): Long? = chapters.firstOrNull { it.startMs > globalMs }?.startMs

    /** Where the current chapter ends (the book's end when there are no chapters). */
    fun chapterEnd(globalMs: Long): Long = chapterAt(globalMs)?.endMs?.takeIf { it > 0 } ?: durationMs
}
