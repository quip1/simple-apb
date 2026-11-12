// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.data

import org.junit.Assert.assertEquals
import org.junit.Test
import org.simpleapb.core.data.local.BookLayout
import org.simpleapb.core.data.local.BookLayout.Book
import org.simpleapb.core.data.local.BookLayout.Node

class BookLayoutTest {
    private fun file(name: String) = Node(name, name, isDir = false, isAudio = true)

    private fun other(name: String) = Node(name, name, isDir = false)

    private fun dir(name: String, vararg children: Node) = Node(name, name, isDir = true, children = children.toList())

    /** Each book as "folder: files" or "file". */
    private fun describe(books: List<Book>) = books.map {
        when (it) {
            is Book.Folder -> it.dir.name + ": " + it.files.joinToString(", ") { f -> f.name }
            is Book.File -> it.file.name
        }
    }

    @Test
    fun aSeriesFolderOfM4bsIsOneBookPerFile() {
        val root =
            dir(
                "picked",
                dir("Dungeon Crawler Carl", file("1 Dungeon Crawler Carl.m4b"), file("2 Carl's Doomsday Scenario.m4b"), other("cover.jpg"))
            )
        assertEquals(listOf("1 Dungeon Crawler Carl.m4b", "2 Carl's Doomsday Scenario.m4b"), describe(BookLayout.plan(root)))
    }

    @Test
    fun mp3FoldersAndSingleFilesAreOneBook() {
        val root =
            dir(
                "picked",
                dir("Folder Book", file("01.mp3"), file("02.mp3")),
                dir("Single", file("book.m4b")),
                file("loose.m4b")
            )
        assertEquals(listOf("loose.m4b", "Folder Book: 01.mp3, 02.mp3", "Single: book.m4b"), describe(BookLayout.plan(root)))
    }

    @Test
    fun discFoldersMakeOneBook() {
        val root = dir("picked", dir("Big Book", dir("CD1", file("a.mp3")), dir("CD2", file("b.mp3")), dir("Scans", other("x.jpg"))))
        assertEquals(listOf("Big Book: a.mp3, b.mp3"), describe(BookLayout.plan(root)))
    }

    @Test
    fun nestedAuthorSeriesBookFoldersAreFound() {
        val root =
            dir(
                "picked",
                dir(
                    "Matt Dinniman",
                    dir("Dungeon Crawler Carl", dir("Book 1", file("01.mp3"), file("02.mp3")), dir("Book 2", file("only.m4b")))
                )
            )
        assertEquals(listOf("Book 1: 01.mp3, 02.mp3", "Book 2: only.m4b"), describe(BookLayout.plan(root)))
    }

    @Test
    fun subfoldersThatArentDiscsAreBooksOfTheirOwn() {
        // Two subfolders not named like discs: two books, not one merged book.
        val root = dir("picked", dir("Series", dir("First", file("1.mp3")), dir("Second", file("2.mp3"))))
        assertEquals(listOf("First: 1.mp3", "Second: 2.mp3"), describe(BookLayout.plan(root)))
    }
}
