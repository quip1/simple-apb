// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.library

import androidx.annotation.Keep
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridScope
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ViewList
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Clear
import androidx.compose.material.icons.filled.GridView
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.simpleapb.api.endpoints.LibraryApi.ShelfListLibrary
import org.simpleapb.api.endpoints.LibraryApi.SortListLibrary
import org.simpleapb.api.model.LibraryFacet
import org.simpleapb.app.ui.BookCard
import org.simpleapb.app.ui.BookRow
import org.simpleapb.app.ui.ChoicePicker
import org.simpleapb.app.ui.ConfirmDialog
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.Pager
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.SectionTitle
import org.simpleapb.app.ui.TextDialog
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

/** Where a facet row leads. */
@Keep
enum class PersonKind { AUTHOR, NARRATOR, SERIES }

@Composable
private fun viewLabel(v: LibraryView) = stringResource(
    when (v) {
        LibraryView.BOOKS -> UiR.string.library_view_books
        LibraryView.DOWNLOADED -> UiR.string.app_library_view_downloaded
        LibraryView.SERIES -> UiR.string.library_view_series
        LibraryView.AUTHORS -> UiR.string.library_view_authors
        LibraryView.NARRATORS -> UiR.string.library_view_narrators
        LibraryView.GENRES -> UiR.string.library_view_genres
        LibraryView.COLLECTIONS -> UiR.string.library_view_collections
    }
)

@Composable
private fun shelfLabel(s: ShelfListLibrary) = stringResource(
    when (s) {
        ShelfListLibrary.ALL -> UiR.string.library_shelf_all
        ShelfListLibrary.IN_PROGRESS -> UiR.string.library_shelf_in_progress
        ShelfListLibrary.NOT_STARTED -> UiR.string.library_shelf_not_started
        ShelfListLibrary.FINISHED -> UiR.string.library_shelf_finished
        ShelfListLibrary.ARCHIVED -> UiR.string.library_shelf_archived
        ShelfListLibrary.RECENTLY_REMOVED -> UiR.string.library_shelf_recently_removed
    }
)

@Composable
private fun sortLabel(s: SortListLibrary) = stringResource(
    when (s) {
        SortListLibrary.RECENT_ADDED -> UiR.string.library_sort_recent_added
        SortListLibrary.RECENT_PLAYED -> UiR.string.library_sort_recent_played
        SortListLibrary.TITLE -> UiR.string.library_sort_title
        SortListLibrary.AUTHOR -> UiR.string.library_sort_author
        SortListLibrary.SERIES -> UiR.string.library_sort_series
        SortListLibrary.DURATION -> UiR.string.library_sort_duration
    }
)

private fun LazyGridScope.full(key: Any? = null, content: @Composable () -> Unit) {
    item(key = key, span = { GridItemSpan(maxLineSpan) }) { content() }
}

@Composable
fun LibraryScreen(
    account: Account,
    startShelf: String?,
    startTag: String?,
    startTagName: String?,
    onBook: (String) -> Unit,
    onPerson: (PersonKind, String) -> Unit
) {
    val vm: LibraryViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    val list by vm.listLayout.collectAsStateWithLifecycle()
    val eink = LocalEink.current
    LaunchedEffect(account.id) { vm.start(startShelf, startTag, startTagName, eink) }
    var search by rememberSaveable { mutableStateOf(s.query) }
    var renaming by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf(false) }
    val grid = rememberLazyGridState()
    val focus = LocalFocusManager.current

    // Endless scrolling in the dark theme.
    LaunchedEffect(grid, eink) {
        if (eink) return@LaunchedEffect
        snapshotFlow { grid.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0 }.collect { last ->
            if (last >= grid.layoutInfo.totalItemsCount - 8) vm.loadMore()
        }
    }
    // E-ink: a new page starts at the top, without animation.
    LaunchedEffect(s.page, s.facetPage) { if (eink) grid.scrollToItem(0) }

    val books = s.view == LibraryView.BOOKS || s.view == LibraryView.DOWNLOADED
    LazyVerticalGrid(
        columns = if (books && !list) GridCells.Adaptive(100.dp) else GridCells.Fixed(1),
        state = grid,
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(16.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalArrangement = Arrangement.spacedBy(if (books && !list) 16.dp else 0.dp)
    ) {
        full("header") {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                ScreenHeader(stringResource(UiR.string.nav_library)) {
                    if (books) {
                        IconButton(onClick = { vm.setListLayout(!list) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                            Icon(
                                if (list) Icons.Filled.GridView else Icons.AutoMirrored.Filled.ViewList,
                                contentDescription = stringResource(if (list) UiR.string.library_layout_grid else UiR.string.library_layout_list)
                            )
                        }
                    }
                    IconButton(onClick = vm::reload, modifier = Modifier.heightIn(min = TouchTarget)) {
                        Icon(Icons.Filled.Refresh, contentDescription = stringResource(UiR.string.app_common_refresh))
                    }
                }
                OutlinedTextField(
                    value = search,
                    onValueChange = {
                        search = it
                        vm.setQuery(it)
                    },
                    label = { Text(stringResource(UiR.string.library_search)) },
                    placeholder = { Text(stringResource(UiR.string.library_search_placeholder)) },
                    leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                    trailingIcon = {
                        if (search.isNotEmpty()) {
                            IconButton(onClick = {
                                search = ""
                                vm.setQuery("")
                            }) { Icon(Icons.Filled.Clear, contentDescription = stringResource(UiR.string.app_library_clear_search)) }
                        }
                    },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                    keyboardActions = KeyboardActions(onSearch = { focus.clearFocus() }),
                    modifier = Modifier.fillMaxWidth()
                )
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    LibraryView.entries.forEach { v ->
                        val selected = s.view == v && s.tag == null && s.collectionId == null
                        FilterChip(
                            selected = selected,
                            onClick = { vm.setView(v) },
                            label = { Text(viewLabel(v)) },
                            // Selection is never colour alone (brief §11).
                            leadingIcon = if (selected) ({ Icon(Icons.Filled.Check, contentDescription = null) }) else null,
                            modifier = Modifier.heightIn(min = TouchTarget)
                        )
                    }
                }
                s.error?.let { Notice(errorText(it)) }
                if (s.offline) Notice(stringResource(UiR.string.app_offline_notice), error = false)
                if (s.view == LibraryView.BOOKS) BooksHeader(s, vm, onRename = { renaming = true }, onDelete = { deleting = true })
            }
        }

        when (s.view) {
            LibraryView.BOOKS, LibraryView.DOWNLOADED -> {
                if (s.loaded && s.books.isEmpty() && !s.loading) {
                    full("empty") {
                        Text(
                            if (s.query.isNotBlank()) {
                                stringResource(
                                    UiR.string.library_no_results,
                                    s.query.trim()
                                )
                            } else {
                                stringResource(UiR.string.library_empty_shelf)
                            },
                            modifier = Modifier.padding(vertical = 16.dp).semantics { liveRegion = LiveRegionMode.Polite }
                        )
                    }
                }
                if (list) {
                    items(s.books, key = { it.id }) { b ->
                        Column {
                            BookRow(b, account.serverUrl) { onBook(b.id.toString()) }
                            HorizontalDivider()
                        }
                    }
                } else {
                    items(s.books, key = {
                        it.id
                    }) { b -> BookCard(b, account.serverUrl, { onBook(b.id.toString()) }) }
                }
                full("footer") {
                    Column(Modifier.padding(top = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (s.books.isNotEmpty()) {
                            Text(
                                pluralStringResource(UiR.plurals.library_count_shown, s.books.size, s.books.size),
                                style = MaterialTheme.typography.bodySmall,
                                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite }
                            )
                        }
                        if (eink) {
                            Pager(s.page, s.page > 1, s.nextCursor != null, vm::previousPage, vm::nextPage)
                        } else if (s.nextCursor != null) {
                            OutlinedButton(
                                onClick = vm::loadMore,
                                enabled = !s.loading,
                                modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)
                            ) {
                                Text(stringResource(UiR.string.library_load_more))
                            }
                        }
                    }
                }
            }
            LibraryView.COLLECTIONS -> collections(s, vm)
            else -> {
                val pages = (s.facets.size + LibraryViewModel.FACET_PAGE - 1) / LibraryViewModel.FACET_PAGE
                val shown = if (eink) s.facets.drop((s.facetPage - 1) * LibraryViewModel.FACET_PAGE).take(LibraryViewModel.FACET_PAGE) else s.facets
                if (s.loaded && s.facets.isEmpty() && !s.loading) full("empty") { Text(stringResource(UiR.string.library_no_facets)) }
                items(shown, key = { it.id }) { f ->
                    FacetRow(f) {
                        when (s.view) {
                            LibraryView.SERIES -> onPerson(PersonKind.SERIES, f.id.toString())
                            LibraryView.AUTHORS -> onPerson(PersonKind.AUTHOR, f.id.toString())
                            LibraryView.NARRATORS -> onPerson(PersonKind.NARRATOR, f.id.toString())
                            else -> vm.showGenre(f)
                        }
                    }
                }
                if (eink) {
                    full("pager") {
                        Pager(s.facetPage, s.facetPage > 1, s.facetPage < pages, { vm.setFacetPage(s.facetPage - 1) }, {
                            vm.setFacetPage(
                                s.facetPage + 1
                            )
                        })
                    }
                }
            }
        }
    }

    val own = s.ownCollection
    if (renaming && own != null) {
        TextDialog(
            title = stringResource(UiR.string.library_collection_rename),
            label = stringResource(UiR.string.library_collection_name),
            initial = own.name,
            confirm = stringResource(UiR.string.common_save),
            onConfirm = {
                vm.renameCollection(it)
                renaming = false
            },
            onDismiss = { renaming = false }
        )
    }
    if (deleting && own != null) {
        ConfirmDialog(
            title = stringResource(UiR.string.library_collection_delete),
            body = stringResource(UiR.string.library_collection_delete_body, own.name),
            confirm = stringResource(UiR.string.library_collection_delete),
            onConfirm = {
                vm.deleteCollection()
                deleting = false
            },
            onDismiss = { deleting = false }
        )
    }
}

@Composable
private fun BooksHeader(s: LibraryState, vm: LibraryViewModel, onRename: () -> Unit, onDelete: () -> Unit) {
    val own = s.ownCollection
    val shared = s.sharedCollection
    when {
        s.tag != null -> FilterBanner(stringResource(UiR.string.library_genre_heading, s.tag.name), vm::clearFilter)
        own != null -> {
            FilterBanner(own.name, vm::clearFilter)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = onRename, modifier = Modifier.heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.library_collection_rename))
                }
                OutlinedButton(onClick = onDelete, modifier = Modifier.heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.library_collection_delete))
                }
            }
        }
        shared != null -> {
            FilterBanner(shared.name, vm::clearFilter)
            Text(stringResource(UiR.string.library_shared_by, shared.owner.displayName), style = MaterialTheme.typography.bodySmall)
        }
        s.collectionId != null -> FilterBanner(stringResource(UiR.string.library_view_collections), vm::clearFilter)
    }
    if (s.collectionId == null) {
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            ChoicePicker(stringResource(UiR.string.library_shelf), s.shelf, ShelfListLibrary.entries, { shelfLabel(it) }, vm::setShelf)
            ChoicePicker(stringResource(UiR.string.library_sort), s.sort, SortListLibrary.entries, { sortLabel(it) }, vm::setSort)
        }
    } else {
        ChoicePicker(stringResource(UiR.string.library_sort), s.sort, SortListLibrary.entries, { sortLabel(it) }, vm::setSort)
    }
}

@Composable
private fun FilterBanner(title: String, onClear: () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        SectionTitle(title, Modifier.weight(1f))
        TextButton(onClick = onClear, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.library_clear_filter)) }
    }
}

@Composable
private fun FacetRow(f: LibraryFacet, onClick: () -> Unit) {
    Column {
        Row(
            Modifier
                .fillMaxWidth()
                .heightIn(min = TouchTarget)
                .clickable(role = Role.Button, onClick = onClick)
                .padding(vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(f.name, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
            Text(pluralStringResource(UiR.plurals.library_book_count, f.count, f.count), style = MaterialTheme.typography.bodySmall)
        }
        HorizontalDivider()
    }
}

private fun LazyGridScope.collections(s: LibraryState, vm: LibraryViewModel) {
    full("new") {
        var name by rememberSaveable { mutableStateOf("") }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(
                value = name,
                onValueChange = { name = it },
                label = { Text(stringResource(UiR.string.library_collection_new_placeholder)) },
                singleLine = true,
                modifier = Modifier.weight(1f)
            )
            Button(
                onClick = {
                    vm.createCollection(name)
                    name = ""
                },
                enabled = name.isNotBlank(),
                modifier = Modifier.heightIn(min = TouchTarget)
            ) { Text(stringResource(UiR.string.library_collection_create)) }
        }
    }
    if (s.loaded &&
        s.collections.isEmpty()
    ) {
        full("none") { Text(stringResource(UiR.string.library_no_collections), Modifier.padding(vertical = 12.dp)) }
    }
    items(s.collections, key = { it.id }) { c ->
        FacetRow(LibraryFacet(c.id, c.name, c.bookIds.size)) { vm.openCollection(c.id) }
    }
    if (s.shared.isNotEmpty()) {
        full("shared") { SectionTitle(stringResource(UiR.string.library_shared_with_you), Modifier.padding(top = 16.dp)) }
        items(s.shared, key = { "s" + it.id }) { c ->
            Column {
                FacetRow(LibraryFacet(c.id, c.name, c.bookIds.size)) { vm.openCollection(c.id) }
                Text(stringResource(UiR.string.library_shared_by, c.owner.displayName), style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
