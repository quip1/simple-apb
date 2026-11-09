// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.widget

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.glance.ColorFilter
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.Image
import androidx.glance.ImageProvider
import androidx.glance.LocalSize
import androidx.glance.action.ActionParameters
import androidx.glance.action.actionParametersOf
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetManager
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.LinearProgressIndicator
import androidx.glance.appwidget.SizeMode
import androidx.glance.appwidget.action.ActionCallback
import androidx.glance.appwidget.action.actionRunCallback
import androidx.glance.appwidget.action.actionStartActivity
import androidx.glance.appwidget.cornerRadius
import androidx.glance.appwidget.provideContent
import androidx.glance.appwidget.updateAll
import androidx.glance.background
import androidx.glance.layout.Alignment
import androidx.glance.layout.Box
import androidx.glance.layout.Column
import androidx.glance.layout.ContentScale
import androidx.glance.layout.Row
import androidx.glance.layout.Spacer
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.height
import androidx.glance.layout.padding
import androidx.glance.layout.size
import androidx.glance.layout.width
import androidx.glance.semantics.contentDescription
import androidx.glance.semantics.semantics
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextStyle
import androidx.glance.unit.ColorProvider
import coil3.SingletonImageLoader
import coil3.request.ImageRequest
import coil3.request.allowHardware
import coil3.toBitmap
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import org.simpleapb.app.MainActivity
import org.simpleapb.app.R
import org.simpleapb.app.UiSettings
import org.simpleapb.core.data.net.AppScope
import org.simpleapb.core.player.LoadedBook
import org.simpleapb.core.player.NowPlaying
import org.simpleapb.core.player.PlaybackService
import org.simpleapb.core.player.PlayerConnection
import org.simpleapb.core.player.PlayerState
import org.simpleapb.core.ui.AppTheme
import org.simpleapb.core.ui.R as UiR

@EntryPoint
@InstallIn(SingletonComponent::class)
interface WidgetDeps {
    fun state(): PlayerState

    fun player(): PlayerConnection

    fun ui(): UiSettings
}

/** What the widget draws. */
private data class WidgetModel(val book: LoadedBook?, val now: NowPlaying, val eink: Boolean, val cover: Bitmap?)

/**
 * The Now playing widget (brief §7.6) in two sizes: 2×2 (cover, title,
 * chapter progress, back · play/pause · forward) and 4×2 (the same, wider,
 * with the chapter's name). In the e-ink theme it is black on white with
 * no animation.
 */
class NowPlayingWidget : GlanceAppWidget() {
    override val sizeMode = SizeMode.Responsive(setOf(SMALL, WIDE))

    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val deps = EntryPointAccessors.fromApplication(context, WidgetDeps::class.java)
        val state = deps.state()
        val initialTheme = deps.ui().theme.first()
        provideContent {
            // Collected here, so the widget follows playback while its session is alive.
            val book by state.book.collectAsState()
            val now by state.now.collectAsState()
            val theme by deps.ui().theme.collectAsState(initialTheme)
            val cover by produceState<Bitmap?>(null, book?.coverUrl) { value = book?.coverUrl?.let { loadCover(context, it) } }
            Content(context, WidgetModel(book, now, theme == AppTheme.EINK, cover))
        }
    }

    private suspend fun loadCover(context: Context, url: String): Bitmap? = runCatching {
        // Signed in through the app's image loader; small, and not in hardware memory (widgets are drawn elsewhere).
        val req = ImageRequest.Builder(context).data(url).size(256).allowHardware(false).build()
        SingletonImageLoader.get(context).execute(req).image?.toBitmap()
    }.getOrNull()

    @Composable
    private fun Content(context: Context, m: WidgetModel) {
        val fg = if (m.eink) Color.Black else Color(0xFFE8ECEF)
        val bg = if (m.eink) Color.White else Color(0xFF15191C)
        val accent = if (m.eink) Color.Black else Color(0xFF8FD694)
        val wide = LocalSize.current.width >= WIDE.width
        val open = actionStartActivity(Intent(context, MainActivity::class.java).putExtra(PlaybackService.EXTRA_OPEN_PLAYER, true))
        val b = m.book
        Column(
            GlanceModifier
                .fillMaxSize()
                .background(bg)
                .cornerRadius(16.dp)
                .padding(10.dp)
        ) {
            Row(GlanceModifier.fillMaxWidth().defaultWeight().clickable(open), verticalAlignment = Alignment.CenterVertically) {
                val size = if (wide) 72.dp else 44.dp
                if (m.cover != null) {
                    Image(
                        ImageProvider(m.cover),
                        contentDescription = null,
                        contentScale = ContentScale.Crop,
                        modifier = GlanceModifier.size(size).cornerRadius(8.dp)
                    )
                } else {
                    Box(GlanceModifier.size(size).background(if (m.eink) Color.White else Color(0xFF263036)).cornerRadius(8.dp)) {}
                }
                Spacer(GlanceModifier.width(8.dp))
                Column(GlanceModifier.defaultWeight()) {
                    Text(
                        b?.title ?: context.getString(UiR.string.player_nothing_playing),
                        maxLines = 2,
                        style = TextStyle(color = ColorProvider(fg), fontSize = 14.sp, fontWeight = FontWeight.Bold)
                    )
                    val chapter = b?.timeline?.chapterAt(m.now.positionMs)
                    if (wide && chapter != null) Text(chapter.title, maxLines = 1, style = TextStyle(color = ColorProvider(fg), fontSize = 12.sp))
                }
            }
            if (b != null) {
                val ch = b.timeline.chapterAt(m.now.positionMs)
                val start = ch?.startMs ?: 0
                val end = if (ch != null) b.timeline.chapterEnd(m.now.positionMs) else b.timeline.durationMs
                val progress = if (end > start) ((m.now.positionMs - start).toFloat() / (end - start)).coerceIn(0f, 1f) else 0f
                LinearProgressIndicator(
                    progress = progress,
                    color = ColorProvider(accent),
                    backgroundColor = ColorProvider(if (m.eink) Color.White else Color(0xFF39444B)),
                    modifier = GlanceModifier.fillMaxWidth().height(4.dp).padding(vertical = 0.dp)
                )
            }
            Row(
                GlanceModifier.fillMaxWidth().padding(top = 6.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                Control(R.drawable.ic_widget_back, context.getString(UiR.string.app_widget_back), WidgetAction.BACK, fg)
                Spacer(GlanceModifier.width(if (wide) 24.dp else 8.dp))
                val playing = m.now.playing
                Control(
                    if (playing) R.drawable.ic_widget_pause else R.drawable.ic_widget_play,
                    context.getString(if (playing) UiR.string.player_pause else UiR.string.player_play),
                    WidgetAction.PLAY,
                    fg
                )
                Spacer(GlanceModifier.width(if (wide) 24.dp else 8.dp))
                Control(R.drawable.ic_widget_forward, context.getString(UiR.string.app_widget_forward), WidgetAction.FORWARD, fg)
            }
        }
    }

    @Composable
    private fun Control(icon: Int, label: String, action: String, tint: Color) {
        Image(
            ImageProvider(icon),
            contentDescription = label,
            colorFilter = ColorFilter.tint(ColorProvider(tint)),
            modifier =
            GlanceModifier
                .size(40.dp)
                .padding(6.dp)
                .semantics { contentDescription = label }
                .clickable(actionRunCallback<WidgetAction>(actionParametersOf(WidgetAction.KEY to action)))
        )
    }

    companion object {
        val SMALL = DpSize(110.dp, 110.dp)
        val WIDE = DpSize(200.dp, 110.dp)
    }
}

/** A button on the widget: sends the command to the playback service (starting it if needed). */
class WidgetAction : ActionCallback {
    override suspend fun onAction(context: Context, glanceId: GlanceId, parameters: ActionParameters) {
        val deps = EntryPointAccessors.fromApplication(context, WidgetDeps::class.java)
        val p = deps.player()
        when (parameters[KEY]) {
            BACK -> p.skipBack()
            FORWARD -> p.skipForward()
            PLAY -> if (deps.state().now.value.playing) p.togglePlay() else p.playOrResume()
        }
    }

    companion object {
        val KEY = ActionParameters.Key<String>("action")
        const val BACK = "back"
        const val PLAY = "play"
        const val FORWARD = "forward"
    }
}

class NowPlayingWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = NowPlayingWidget()
}

/** Redraws the widgets when the book, playback or theme changes (only if any are placed). */
@Singleton
class WidgetUpdater
@Inject
constructor(private val state: PlayerState, private val ui: UiSettings, @AppScope private val scope: CoroutineScope) {
    @OptIn(FlowPreview::class)
    fun start(context: Context) {
        scope.launch {
            combine(state.book, state.now, ui.theme) { b, n, t -> Triple(b?.id, n, t) }
                .distinctUntilChanged()
                .debounce(UPDATE_DEBOUNCE_MS)
                .collect {
                    if (GlanceAppWidgetManager(context).getGlanceIds(NowPlayingWidget::class.java).isNotEmpty()) {
                        NowPlayingWidget().updateAll(context)
                    }
                }
        }
    }

    private companion object {
        const val UPDATE_DEBOUNCE_MS = 500L
    }
}
