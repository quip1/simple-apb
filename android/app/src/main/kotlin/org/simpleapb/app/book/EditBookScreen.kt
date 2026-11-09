// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.book

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import java.util.UUID
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.model.BookDetail
import org.simpleapb.api.model.BookMetadataPatch
import org.simpleapb.api.model.SeriesInput
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.ui.BusyButton
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.ui.R as UiR

/** The editable details as text fields (people and genres one per line). */
data class BookForm(
    val title: String = "",
    val subtitle: String = "",
    val authors: String = "",
    val narrators: String = "",
    val series: String = "",
    val seriesIndex: String = "",
    val year: String = "",
    val publisher: String = "",
    val language: String = "",
    val isbn: String = "",
    val asin: String = "",
    val genres: String = "",
    val description: String = ""
) {
    /** Only the fields that changed: each one sent is kept as set (locked against automatic matching). */
    fun patch(from: BookForm): BookMetadataPatch {
        fun lines(s: String) = s.lines().map { it.trim() }.filter { it.isNotEmpty() }

        fun changed(a: String, b: String) = a.trim() != b.trim()
        return BookMetadataPatch(
            title = title.trim().takeIf { changed(title, from.title) && it.isNotEmpty() },
            subtitle = subtitle.trim().takeIf { changed(subtitle, from.subtitle) },
            description = description.trim().takeIf { changed(description, from.description) },
            publisher = publisher.trim().takeIf { changed(publisher, from.publisher) },
            publishedYear = year.trim().toIntOrNull().takeIf { changed(year, from.year) },
            language = language.trim().takeIf { changed(language, from.language) },
            isbn = isbn.trim().takeIf { changed(isbn, from.isbn) },
            asin = asin.trim().takeIf { changed(asin, from.asin) },
            authors = lines(authors).takeIf { lines(authors) != lines(from.authors) },
            narrators = lines(narrators).takeIf { lines(narrators) != lines(from.narrators) },
            series =
            if (changed(series, from.series) || changed(seriesIndex, from.seriesIndex)) {
                SeriesInput(series.trim(), seriesIndex.trim().ifEmpty { null })
            } else {
                null
            },
            genres = lines(genres).takeIf { lines(genres) != lines(from.genres) }
        )
    }

    companion object {
        fun of(b: BookDetail) = BookForm(
            title = b.title,
            subtitle = b.subtitle.orEmpty(),
            authors = b.authors.joinToString("\n") { it.name },
            narrators = b.narrators.joinToString("\n") { it.name },
            series = b.series?.name.orEmpty(),
            seriesIndex = b.series?.index.orEmpty(),
            year = b.publishedYear?.toString().orEmpty(),
            publisher = b.publisher.orEmpty(),
            language = b.language.orEmpty(),
            isbn = b.isbn.orEmpty(),
            asin = b.asin.orEmpty(),
            genres = b.tags.filter { it.kind.value == "genre" }.joinToString("\n") { it.name },
            description = b.description.orEmpty()
        )
    }
}

data class EditState(val original: BookForm? = null, val busy: Boolean = false, val error: ApiFailure? = null, val saved: Boolean = false)

@HiltViewModel
class EditBookViewModel
@Inject
constructor(private val session: MeSession) : ViewModel() {
    private val _state = MutableStateFlow(EditState())
    val state: StateFlow<EditState> = _state.asStateFlow()

    fun load(id: String) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val uuid = runCatching { UUID.fromString(id) }.getOrNull() ?: return@launch
        when (val r = call { api.library.getBook(uuid) }) {
            is Outcome.Ok -> _state.update { it.copy(original = BookForm.of(r.value)) }
            is Outcome.Failed -> _state.update { it.copy(error = r.failure) }
        }
    }

    fun save(id: String, form: BookForm) = viewModelScope.launch {
        val api = session.api() ?: return@launch
        val from = _state.value.original ?: return@launch
        _state.update { it.copy(busy = true, error = null) }
        val r = call { api.library.updateBookMetadata(UUID.fromString(id), form.patch(from)) }
        _state.update { it.copy(busy = false, error = (r as? Outcome.Failed)?.failure, saved = r is Outcome.Ok) }
    }
}

/**
 * Edit a book's details (admins and uploaders with the permission, brief
 * §6.12). Finding a match online stays in the web app for now.
 */
@Composable
fun EditBookScreen(bookId: String, onDone: () -> Unit) {
    val vm: EditBookViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    LaunchedEffect(bookId) { vm.load(bookId) }
    LaunchedEffect(s.saved) { if (s.saved) onDone() }
    val original = s.original
    FormColumn {
        ScreenHeader(stringResource(UiR.string.book_edit), onBack = onDone)
        s.error?.let { Notice(errorText(it)) }
        if (original == null) {
            Text(stringResource(UiR.string.common_loading))
            return@FormColumn
        }
        var f by remember(original) { mutableStateOf(original) }
        Notice(stringResource(UiR.string.meta_lock_explainer), error = false)
        Field(stringResource(UiR.string.meta_title), f.title) { f = f.copy(title = it) }
        Field(stringResource(UiR.string.meta_subtitle), f.subtitle) { f = f.copy(subtitle = it) }
        Field(stringResource(UiR.string.meta_authors), f.authors, multi = true, hint = stringResource(UiR.string.meta_one_per_line)) {
            f =
                f.copy(authors = it)
        }
        Field(stringResource(UiR.string.meta_narrators), f.narrators, multi = true, hint = stringResource(UiR.string.meta_one_per_line)) {
            f =
                f.copy(narrators = it)
        }
        Field(stringResource(UiR.string.meta_series), f.series) { f = f.copy(series = it) }
        Field(stringResource(UiR.string.meta_series_index), f.seriesIndex) { f = f.copy(seriesIndex = it) }
        Field(stringResource(UiR.string.meta_year), f.year, number = true) { f = f.copy(year = it.filter(Char::isDigit).take(4)) }
        Field(stringResource(UiR.string.meta_publisher), f.publisher) { f = f.copy(publisher = it) }
        Field(stringResource(UiR.string.meta_language), f.language, hint = stringResource(UiR.string.meta_language_hint)) {
            f = f.copy(language = it)
        }
        Field(stringResource(UiR.string.meta_isbn), f.isbn) { f = f.copy(isbn = it) }
        Field(stringResource(UiR.string.meta_asin), f.asin) { f = f.copy(asin = it) }
        Field(stringResource(UiR.string.meta_genres), f.genres, multi = true, hint = stringResource(UiR.string.meta_one_per_line)) {
            f =
                f.copy(genres = it)
        }
        Field(stringResource(UiR.string.meta_description), f.description, multi = true) { f = f.copy(description = it) }
        BusyButton(stringResource(UiR.string.common_save), s.busy, onClick = { vm.save(bookId, f) }, enabled = f.title.isNotBlank())
    }
}

@Composable
private fun Field(label: String, value: String, multi: Boolean = false, number: Boolean = false, hint: String? = null, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        label = { Text(label) },
        singleLine = !multi,
        minLines = if (multi) 3 else 1,
        supportingText = hint?.let { { Text(it) } },
        keyboardOptions = if (number) KeyboardOptions(keyboardType = KeyboardType.Number) else KeyboardOptions.Default,
        modifier = Modifier.fillMaxWidth()
    )
}
