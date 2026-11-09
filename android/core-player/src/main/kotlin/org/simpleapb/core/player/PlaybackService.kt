// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
import android.os.Bundle
import android.os.SystemClock
import android.view.KeyEvent
import androidx.annotation.OptIn
import androidx.core.content.IntentCompat
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSourceBitmapLoader
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.DefaultAudioSink
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.CacheBitmapLoader
import androidx.media3.session.CommandButton
import androidx.media3.session.LibraryResult
import androidx.media3.session.MediaLibraryService
import androidx.media3.session.MediaLibraryService.LibraryParams
import androidx.media3.session.MediaSession
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionError
import androidx.media3.session.SessionResult
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject
import kotlin.math.abs
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.guava.future
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.net.AccountCalls
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.AppScope
import org.simpleapb.core.data.sync.SyncEngine
import org.simpleapb.core.ui.R as UiR

/**
 * The one playback service (brief §7.2): background play, the notification
 * and lock screen, headset and Bluetooth buttons, and (later) Android Auto
 * and Wear, all through one MediaSession.
 *
 * A book plays as a playlist of its files; everything the user sees and
 * every progress report uses the book-global position from [BookTimeline].
 */
@OptIn(UnstableApi::class)
@AndroidEntryPoint
class PlaybackService :
    MediaLibraryService(),
    BookPlayer.Hooks {
    @Inject lateinit var repo: PlaybackRepository

    @Inject lateinit var state: PlayerState

    @Inject lateinit var store: PlaybackStore

    @Inject lateinit var calls: AccountCalls

    @Inject lateinit var engine: SyncEngine

    @Inject lateinit var browse: BrowseTree

    @Inject @AppScope
    lateinit var appScope: CoroutineScope

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private lateinit var exo: ExoPlayer
    private lateinit var player: BookPlayer
    private var session: MediaLibrarySession? = null
    private var settings = PlaybackSettings()
    private lateinit var presses: HeadsetPresses

    // Progress reporting (brief §6.8): every 15 s while playing, on pause, after a seek settles, at the end.
    private var lastReportAt = 0L
    private var lastSavedAt = 0L
    private var lastSentMs = -1L
    private var seekAt = 0L
    private var pausedAt = 0L

    private val shake = ShakeDetector()
    private val effects = EffectsProcessor()
    private var sensorOn = false

    override fun onCreate() {
        super.onCreate()
        // A queued write met a conflict while this book is open: ask now.
        scope.launch {
            engine.conflicts.collect { c ->
                val b = book() ?: return@collect
                if (b.accountId == c.accountId &&
                    b.id == c.bookId
                ) {
                    state.conflict.value = PendingConflict(b.id, c.conflictId, c.serverPositionMs, c.serverDevice)
                }
            }
        }
        val http = OkHttpDataSource.Factory(calls)
        // The audio effects run inside the player's sink, before the speed change.
        val renderers =
            object : DefaultRenderersFactory(this) {
                override fun buildAudioSink(context: Context, enableFloatOutput: Boolean, enableAudioTrackPlaybackParams: Boolean): AudioSink =
                    DefaultAudioSink
                        .Builder(context)
                        .setAudioProcessors(arrayOf(effects))
                        .setEnableFloatOutput(false)
                        .setEnableAudioTrackPlaybackParams(enableAudioTrackPlaybackParams)
                        .build()
            }
        exo =
            ExoPlayer
                .Builder(this, renderers)
                .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(this, http)))
                .setAudioAttributes(attributes(FocusMode.PAUSE), true)
                .setHandleAudioBecomingNoisy(true)
                .setWakeMode(C.WAKE_MODE_NETWORK)
                .build()
        exo.addListener(listener)
        player = BookPlayer(exo, this)
        presses = HeadsetPresses(scope) { g -> perform(settings.action(g)) }
        // Covers come from the server or, for downloads, from the book's folder.
        val bitmaps = CacheBitmapLoader(DataSourceBitmapLoader.Builder(this).setDataSourceFactory(DefaultDataSource.Factory(this, http)).build())
        val builder =
            MediaLibrarySession
                .Builder(this, player, callback)
                .setBitmapLoader(bitmaps)
                .setMediaButtonPreferences(buttons())
        launchIntent()?.let(builder::setSessionActivity)
        session = builder.build()

        scope.launch {
            store.settings.collect { s ->
                val focusChanged = s.focus != settings.focus
                settings = s
                effects.config = s.effects
                if (focusChanged) exo.setAudioAttributes(attributes(s.focus), true)
                refreshButtons()
                updateSensor()
            }
        }
        scope.launch {
            state.sleep.collect {
                updateSensor()
                if (it == null) exo.volume = 1f
            }
        }
        scope.launch { tick() }
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaLibrarySession? = session

    override fun onTaskRemoved(rootIntent: Intent?) {
        report(force = true)
        super.onTaskRemoved(rootIntent)
    }

    override fun onDestroy() {
        report(force = true)
        setSensor(false)
        session?.release()
        session = null
        exo.release()
        scope.cancel()
        super.onDestroy()
    }

    // ── controls ──

    private fun book(): LoadedBook? = state.book.value

    private fun position(): Long = book()?.timeline?.global(exo.currentMediaItemIndex, exo.currentPosition) ?: exo.currentPosition

    private fun seekBook(ms: Long) {
        val b = book() ?: return
        val p = b.timeline.locate(ms)
        exo.seekTo(p.index, p.localMs)
    }

    override fun skipBack() = seekBook((position() - settings.skipBackS * 1_000L).coerceAtLeast(0))

    override fun skipForward() {
        val b = book() ?: return
        seekBook((position() + settings.skipForwardS * 1_000L).coerceAtMost(b.timeline.durationMs))
    }

    private fun previousChapter() {
        val b = book() ?: return
        seekBook(b.timeline.previousChapterStart(position()))
    }

    private fun nextChapter() {
        val b = book() ?: return
        b.timeline.nextChapterStart(position())?.let(::seekBook)
    }

    /** Auto-rewind (brief §7.2) when resuming after a pause. */
    override fun beforePlay() {
        if (exo.isPlaying || pausedAt == 0L) return
        val back = autoRewindMs(settings.autoRewindS, SystemClock.elapsedRealtime() - pausedAt)
        pausedAt = 0
        if (back > 0) seekBook((position() - back).coerceAtLeast(0))
    }

    private fun togglePlay() {
        val ended = exo.playbackState == Player.STATE_ENDED
        if (exo.playWhenReady && !ended) {
            player.pause()
            return
        }
        // Play on a finished book starts it again from the beginning.
        if (ended) seekBook(0)
        if (exo.playbackState == Player.STATE_IDLE) exo.prepare()
        player.play()
    }

    private fun perform(action: HeadsetAction) {
        when (action) {
            HeadsetAction.NONE -> Unit
            HeadsetAction.PLAY_PAUSE -> togglePlay()
            HeadsetAction.BACK -> skipBack()
            HeadsetAction.FORWARD -> skipForward()
            HeadsetAction.PREVIOUS_CHAPTER -> previousChapter()
            HeadsetAction.NEXT_CHAPTER -> nextChapter()
            HeadsetAction.BOOKMARK -> addBookmark(null)
        }
    }

    private fun addBookmark(note: String?) {
        val b = book() ?: return
        val at = position()
        appScope.launch {
            when (val r = repo.addBookmark(b, at, note)) {
                is Outcome.Ok -> state.events.tryEmit(PlayerEvent.Bookmarked(at))
                is Outcome.Failed -> state.events.tryEmit(PlayerEvent.BookmarkFailed(r.failure))
            }
        }
    }

    // ── sleep timer (brief §7.2) ──

    private fun setSleep(minutes: Int?, endOfChapter: Boolean) {
        val now = SystemClock.elapsedRealtime()
        state.sleep.value =
            when {
                endOfChapter -> book()?.let { SleepTimer.EndOfChapter(it.timeline.chapterEnd(position())) }
                minutes != null && minutes > 0 -> {
                    scope.launch { store.update { it.copy(lastSleepMin = minutes) } }
                    SleepTimer.Until(now + minutes * 60_000L)
                }
                else -> null
            }
        exo.volume = 1f
        refreshButtons()
    }

    private fun sleepTick(now: Long) {
        val t = state.sleep.value ?: return
        val speed = exo.playbackParameters.speed
        val pos = position()
        if (SleepTimer.remainingMs(t, now, pos, speed) <= 0) {
            player.pause()
            state.sleep.value = null
            exo.volume = 1f
            refreshButtons()
            return
        }
        if (exo.isPlaying) exo.volume = SleepTimer.volume(t, now, pos, speed)
    }

    private val sensorListener =
        object : SensorEventListener {
            override fun onSensorChanged(e: SensorEvent) {
                val t = state.sleep.value ?: return
                val now = SystemClock.elapsedRealtime()
                if (shake.onSample(e.values[0], e.values[1], e.values[2], now)) {
                    state.sleep.value = SleepTimer.extend(t, now, position(), exo.playbackParameters.speed, settings.shakeExtendMin * 60_000L)
                    exo.volume = 1f
                    state.events.tryEmit(PlayerEvent.SleepExtended)
                }
            }

            override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) = Unit
        }

    /** The accelerometer runs only while a sleep timer is set (brief §7.2). */
    private fun updateSensor() = setSensor(state.sleep.value != null && settings.shakeToExtend)

    private fun setSensor(on: Boolean) {
        if (on == sensorOn) return
        val sm = getSystemService(Context.SENSOR_SERVICE) as? SensorManager ?: return
        if (on) {
            val acc = sm.getDefaultSensor(Sensor.TYPE_ACCELEROMETER) ?: return
            sensorOn = sm.registerListener(sensorListener, acc, SensorManager.SENSOR_DELAY_UI)
        } else {
            sm.unregisterListener(sensorListener)
            sensorOn = false
        }
    }

    // ── progress ──

    private suspend fun tick() {
        while (scope.isActive) {
            delay(TICK_MS)
            if (book() == null) continue
            val now = SystemClock.elapsedRealtime()
            sleepTick(now)
            if (exo.isPlaying && now - lastReportAt >= REPORT_EVERY_MS) report()
            if (now - lastPublishAt >= PUBLISH_EVERY_MS) publish()
            checkAutoNext()
            if (seekAt > 0 && now - seekAt >= SEEK_SETTLE_MS) {
                seekAt = 0
                report(force = true)
            }
            if (exo.isPlaying && now - lastSavedAt >= SAVE_EVERY_MS) {
                lastSavedAt = now
                val b = book() ?: continue
                val pos = position()
                appScope.launch { repo.saveLocal(b.id, pos) }
            }
        }
    }

    private var lastPublishAt = 0L

    /** The widget's view of playback (brief §7.6). */
    private fun publish() {
        lastPublishAt = SystemClock.elapsedRealtime()
        state.now.value = NowPlaying(exo.isPlaying || (exo.playWhenReady && exo.playbackState == Player.STATE_BUFFERING), position())
    }

    private var autoNextFor: String? = null

    /** Past 90 % of a downloaded book: download the next in its series (decision #4). */
    private fun checkAutoNext() {
        val b = book() ?: return
        if (b.localDir == null || b.seriesId == null || autoNextFor == b.id) return
        if (b.timeline.durationMs <= 0 || position() < b.timeline.durationMs * AUTO_NEXT_FRACTION) return
        autoNextFor = b.id
        appScope.launch { repo.autoNext(b) }
    }

    private fun report(force: Boolean = false, finished: Boolean? = null) {
        val b = book() ?: return
        lastReportAt = SystemClock.elapsedRealtime()
        // While the user decides a conflict, don't keep creating new ones.
        if (state.conflict.value != null) return
        val pos = if (finished == true) b.timeline.durationMs else position()
        if (!force && finished == null && abs(pos - lastSentMs) < MIN_MOVE_MS) return
        lastSentMs = pos
        val speed = exo.playbackParameters.speed
        appScope.launch { handle(repo.report(b, pos, speed, finished, null)) }
    }

    private fun handle(r: ReportResult) {
        if (r is ReportResult.Conflict && state.book.value?.id == r.conflict.bookId) state.conflict.value = r.conflict
    }

    private fun resolveConflict(jump: Boolean) {
        val c = state.conflict.value ?: return
        val b = book()?.takeIf { it.id == c.bookId }
        state.conflict.value = null
        if (b == null) return
        val pos = if (jump) c.serverPositionMs else position()
        if (jump) seekBook(pos)
        seekAt = 0
        lastSentMs = pos
        lastReportAt = SystemClock.elapsedRealtime()
        val speed = exo.playbackParameters.speed
        appScope.launch { repo.report(b, pos, speed, null, c.conflictId) }
    }

    private val listener =
        object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) = publish()

            override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
                if (!playWhenReady) {
                    pausedAt = SystemClock.elapsedRealtime()
                    report(force = true)
                }
            }

            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_READY) publish()
                if (playbackState == Player.STATE_ENDED) {
                    report(finished = true)
                    if (state.sleep.value is SleepTimer.EndOfChapter) state.sleep.value = null
                }
            }

            override fun onPositionDiscontinuity(oldPosition: Player.PositionInfo, newPosition: Player.PositionInfo, reason: Int) {
                if (reason == Player.DISCONTINUITY_REASON_SEEK) seekAt = SystemClock.elapsedRealtime()
                publish()
            }

            override fun onPlaybackParametersChanged(playbackParameters: PlaybackParameters) {
                val b = book() ?: return
                scope.launch { store.setSpeed(b.id, playbackParameters.speed) }
                refreshButtons()
            }

            override fun onPlayerError(error: PlaybackException) {
                state.events.tryEmit(PlayerEvent.PlaybackFailed)
            }
        }

    // ── loading a book ──

    private suspend fun resolve(bookId: String, accountId: String?, startMs: Long? = null): MediaSession.MediaItemsWithStartPosition {
        val account = accountId?.let { repo.account(it) } ?: repo.activeAccount() ?: throw LoadFailed(ApiFailure("signed_out", "", 401))
        val b =
            when (val r = repo.load(account, bookId)) {
                // A chapter or bookmark picked on the book's page starts there.
                is Outcome.Ok -> if (startMs != null) r.value.copy(startMs = startMs.coerceIn(0, r.value.timeline.durationMs)) else r.value
                is Outcome.Failed -> {
                    state.events.tryEmit(PlayerEvent.LoadFailed(r.failure))
                    throw LoadFailed(r.failure)
                }
            }
        if (book()?.id != b.id) report(force = true)
        state.book.value = b
        // A conflict found while this device was offline is asked about now (brief §7.5).
        state.conflict.value = repo.storedConflict(b)
        autoNextFor = null
        pausedAt = 0
        seekAt = 0
        lastSentMs = b.startMs
        lastReportAt = SystemClock.elapsedRealtime()
        exo.setPlaybackSpeed(b.speed)
        store.setLastBook(account.id, b.id)
        val p = b.timeline.locate(b.startMs)
        return MediaSession.MediaItemsWithStartPosition(repo.mediaItems(b), p.index, p.localMs)
    }

    private class LoadFailed(val failure: ApiFailure) : Exception(failure.code)

    private val callback =
        object : MediaLibrarySession.Callback {
            override fun onConnect(session: MediaSession, controller: MediaSession.ControllerInfo): MediaSession.ConnectionResult {
                val commands =
                    MediaSession.ConnectionResult.DEFAULT_SESSION_AND_LIBRARY_COMMANDS
                        .buildUpon()
                        .apply { Commands.ALL.forEach { add(it) } }
                        .build()
                val car = session.isAutoCompanionController(controller) || session.isAutomotiveController(controller)
                val result =
                    MediaSession.ConnectionResult
                        .AcceptedResultBuilder(session, controller)
                        .setAvailableSessionCommands(commands)
                        // In the car: skips and chapter skips, whatever the notification shows (brief §7.3).
                        .setMediaButtonPreferences(if (car) carButtons() else buttons())
                if (session.isMediaNotificationController(controller)) {
                    // The notification shows only the buttons the user chose, not "previous/next track".
                    result.setAvailablePlayerCommands(
                        MediaSession.ConnectionResult.DEFAULT_PLAYER_COMMANDS
                            .buildUpon()
                            .removeAll(
                                Player.COMMAND_SEEK_TO_PREVIOUS,
                                Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                                Player.COMMAND_SEEK_TO_NEXT,
                                Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM
                            ).build()
                    )
                }
                return result.build()
            }

            override fun onCustomCommand(
                session: MediaSession,
                controller: MediaSession.ControllerInfo,
                customCommand: SessionCommand,
                args: Bundle
            ): ListenableFuture<SessionResult> {
                when (customCommand.customAction) {
                    Commands.SKIP_BACK -> skipBack()
                    Commands.SKIP_FORWARD -> skipForward()
                    Commands.PREVIOUS_CHAPTER -> previousChapter()
                    Commands.NEXT_CHAPTER -> nextChapter()
                    Commands.CYCLE_SPEED -> exo.setPlaybackSpeed(Speed.next(exo.playbackParameters.speed))
                    Commands.TOGGLE_SLEEP ->
                        if (state.sleep.value != null) setSleep(null, false) else setSleep(settings.lastSleepMin, false)
                    Commands.SET_SLEEP ->
                        setSleep(args.getInt(Commands.EXTRA_MINUTES, 0).takeIf { it > 0 }, args.getBoolean(Commands.EXTRA_CHAPTER))
                    Commands.ADD_BOOKMARK -> addBookmark(args.getString(Commands.EXTRA_NOTE))
                    Commands.RESOLVE_CONFLICT -> resolveConflict(args.getBoolean(Commands.EXTRA_JUMP))
                    Commands.SEEK_BOOK -> seekBook(args.getLong(Commands.EXTRA_POSITION))
                    else -> return Futures.immediateFuture(SessionResult(SessionError.ERROR_NOT_SUPPORTED))
                }
                return Futures.immediateFuture(SessionResult(SessionResult.RESULT_SUCCESS))
            }

            override fun onSetMediaItems(
                mediaSession: MediaSession,
                controller: MediaSession.ControllerInfo,
                mediaItems: MutableList<MediaItem>,
                startIndex: Int,
                startPositionMs: Long
            ): ListenableFuture<MediaSession.MediaItemsWithStartPosition> {
                val item = mediaItems.singleOrNull()
                // "Play <title>" by voice: the best match from the library's search.
                val query = item?.requestMetadata?.searchQuery
                if (item != null && item.mediaId.isEmpty() && query != null) {
                    return scope.future {
                        val hit = browse.search(query).firstOrNull() ?: throw LoadFailed(ApiFailure("not_found", "", 404))
                        resolve(hit.mediaId.removePrefix(Commands.BOOK_PREFIX), null)
                    }
                }
                val id = item?.mediaId?.takeIf { it.startsWith(Commands.BOOK_PREFIX) }
                if (id == null) return super.onSetMediaItems(mediaSession, controller, mediaItems, startIndex, startPositionMs)
                val start = item.requestMetadata.extras?.getLong(Commands.EXTRA_POSITION, -1L)?.takeIf { it >= 0 }
                return scope.future { resolve(id.removePrefix(Commands.BOOK_PREFIX), null, start) }
            }

            override fun onGetLibraryRoot(
                session: MediaLibrarySession,
                browser: MediaSession.ControllerInfo,
                params: LibraryParams?
            ): ListenableFuture<LibraryResult<MediaItem>> = Futures.immediateFuture(LibraryResult.ofItem(browse.root(), params))

            override fun onGetChildren(
                session: MediaLibrarySession,
                browser: MediaSession.ControllerInfo,
                parentId: String,
                page: Int,
                pageSize: Int,
                params: LibraryParams?
            ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> = scope.future {
                val all = browse.children(parentId)
                val items = if (pageSize in 1 until Int.MAX_VALUE) all.drop(page * pageSize).take(pageSize) else all
                grantCovers(browser, items)
                LibraryResult.ofItemList(items, params)
            }

            override fun onGetItem(
                session: MediaLibrarySession,
                browser: MediaSession.ControllerInfo,
                mediaId: String
            ): ListenableFuture<LibraryResult<MediaItem>> = scope.future {
                val b = book()
                if (b != null && mediaId == Commands.bookMediaId(b.id)) {
                    LibraryResult.ofItem(
                        MediaItem.Builder().setMediaId(
                            mediaId
                        ).setMediaMetadata(MediaMetadata.Builder().setTitle(b.title).setIsPlayable(true).setIsBrowsable(false).build()).build(),
                        null
                    )
                } else {
                    LibraryResult.ofError(SessionError.ERROR_BAD_VALUE)
                }
            }

            override fun onSearch(
                session: MediaLibrarySession,
                browser: MediaSession.ControllerInfo,
                query: String,
                params: LibraryParams?
            ): ListenableFuture<LibraryResult<Void>> = scope.future {
                val n = browse.search(query).size
                session.notifySearchResultChanged(browser, query, n, params)
                LibraryResult.ofVoid()
            }

            override fun onGetSearchResult(
                session: MediaLibrarySession,
                browser: MediaSession.ControllerInfo,
                query: String,
                page: Int,
                pageSize: Int,
                params: LibraryParams?
            ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> = scope.future {
                val all = browse.search(query)
                val items = if (pageSize in 1 until Int.MAX_VALUE) all.drop(page * pageSize).take(pageSize) else all
                grantCovers(browser, items)
                LibraryResult.ofItemList(items, params)
            }

            override fun onPlaybackResumption(
                mediaSession: MediaSession,
                controller: MediaSession.ControllerInfo,
                isForPlayback: Boolean
            ): ListenableFuture<MediaSession.MediaItemsWithStartPosition> = scope.future {
                val last = store.lastBook() ?: throw LoadFailed(ApiFailure("not_found", "", 404))
                resolve(last.bookId, last.accountId)
            }

            override fun onMediaButtonEvent(session: MediaSession, controllerInfo: MediaSession.ControllerInfo, intent: Intent): Boolean {
                val ev = IntentCompat.getParcelableExtra(intent, Intent.EXTRA_KEY_EVENT, KeyEvent::class.java) ?: return false
                return when (ev.keyCode) {
                    // Single/double/triple/long press, mapped by the user (brief §7.2).
                    KeyEvent.KEYCODE_HEADSETHOOK, KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> {
                        when (ev.action) {
                            KeyEvent.ACTION_DOWN -> presses.down(ev.repeatCount, ev.isLongPress)
                            KeyEvent.ACTION_UP -> presses.up()
                        }
                        true
                    }
                    KeyEvent.KEYCODE_MEDIA_NEXT, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> {
                        if (ev.action == KeyEvent.ACTION_DOWN && ev.repeatCount == 0) skipForward()
                        true
                    }
                    KeyEvent.KEYCODE_MEDIA_PREVIOUS, KeyEvent.KEYCODE_MEDIA_REWIND -> {
                        if (ev.action == KeyEvent.ACTION_DOWN && ev.repeatCount == 0) skipBack()
                        true
                    }
                    else -> false
                }
            }
        }

    // ── notification buttons (brief §7.2) ──

    private fun refreshButtons() {
        session?.setMediaButtonPreferences(buttons())
    }

    /**
     * The user's buttons in their order. Play/pause is always shown by the
     * system in the middle; the button just before it takes the "back" slot
     * and the one after it the "forward" slot, the rest go to the overflow.
     */
    /** Lets the car host read the covers it is shown (the provider isn't exported). */
    private fun grantCovers(browser: MediaSession.ControllerInfo, items: List<MediaItem>) {
        BrowseTree.artwork(items).forEach { runCatching { grantUriPermission(browser.packageName, it, Intent.FLAG_GRANT_READ_URI_PERMISSION) } }
    }

    /** Android Auto: back and forward by the skip lengths, and chapter skips. */
    private fun carButtons(): List<CommandButton> = listOf(
        button(ControlButton.PREVIOUS_CHAPTER).setSlots(CommandButton.SLOT_BACK_SECONDARY, CommandButton.SLOT_OVERFLOW).build(),
        button(ControlButton.BACK).setSlots(CommandButton.SLOT_BACK, CommandButton.SLOT_OVERFLOW).build(),
        button(ControlButton.FORWARD).setSlots(CommandButton.SLOT_FORWARD, CommandButton.SLOT_OVERFLOW).build(),
        button(ControlButton.NEXT_CHAPTER).setSlots(CommandButton.SLOT_FORWARD_SECONDARY, CommandButton.SLOT_OVERFLOW).build()
    )

    private fun buttons(): List<CommandButton> {
        val list = settings.buttons
        val play = list.indexOf(ControlButton.PLAY_PAUSE).let { if (it < 0) list.size else it }
        return list.mapIndexedNotNull { i, b ->
            if (b == ControlButton.PLAY_PAUSE) return@mapIndexedNotNull null
            val builder = button(b)
            when {
                i == play - 1 -> builder.setSlots(CommandButton.SLOT_BACK, CommandButton.SLOT_OVERFLOW)
                i == play + 1 -> builder.setSlots(CommandButton.SLOT_FORWARD, CommandButton.SLOT_OVERFLOW)
                i < play -> builder.setSlots(CommandButton.SLOT_BACK_SECONDARY, CommandButton.SLOT_OVERFLOW)
                else -> builder.setSlots(CommandButton.SLOT_FORWARD_SECONDARY, CommandButton.SLOT_OVERFLOW)
            }
            builder.build()
        }
    }

    private fun button(b: ControlButton): CommandButton.Builder = when (b) {
        ControlButton.BACK ->
            CommandButton
                .Builder(skipIcon(settings.skipBackS, back = true))
                .setDisplayName(getString(UiR.string.player_skip_back, settings.skipBackS.toString()))
                .setSessionCommand(SessionCommand(Commands.SKIP_BACK, Bundle.EMPTY))
        ControlButton.FORWARD ->
            CommandButton
                .Builder(skipIcon(settings.skipForwardS, back = false))
                .setDisplayName(getString(UiR.string.player_skip_forward, settings.skipForwardS.toString()))
                .setSessionCommand(SessionCommand(Commands.SKIP_FORWARD, Bundle.EMPTY))
        ControlButton.PREVIOUS_CHAPTER ->
            CommandButton
                .Builder(CommandButton.ICON_PREVIOUS)
                .setDisplayName(getString(UiR.string.player_previous_chapter))
                .setSessionCommand(SessionCommand(Commands.PREVIOUS_CHAPTER, Bundle.EMPTY))
        ControlButton.NEXT_CHAPTER ->
            CommandButton
                .Builder(CommandButton.ICON_NEXT)
                .setDisplayName(getString(UiR.string.player_next_chapter))
                .setSessionCommand(SessionCommand(Commands.NEXT_CHAPTER, Bundle.EMPTY))
        ControlButton.SPEED ->
            CommandButton
                .Builder(speedIcon(if (::exo.isInitialized) exo.playbackParameters.speed else 1f))
                .setDisplayName(getString(UiR.string.player_speed))
                .setSessionCommand(SessionCommand(Commands.CYCLE_SPEED, Bundle.EMPTY))
        ControlButton.SLEEP ->
            CommandButton
                .Builder(CommandButton.ICON_UNDEFINED)
                .setCustomIconResId(if (state.sleep.value != null) R.drawable.ic_sleep_on else R.drawable.ic_sleep)
                .setDisplayName(getString(UiR.string.player_sleep))
                .setSessionCommand(SessionCommand(Commands.TOGGLE_SLEEP, Bundle.EMPTY))
        ControlButton.PLAY_PAUSE -> CommandButton.Builder(CommandButton.ICON_PLAY).setPlayerCommand(Player.COMMAND_PLAY_PAUSE)
    }

    private fun skipIcon(s: Int, back: Boolean): Int = when (s) {
        5 -> if (back) CommandButton.ICON_SKIP_BACK_5 else CommandButton.ICON_SKIP_FORWARD_5
        10 -> if (back) CommandButton.ICON_SKIP_BACK_10 else CommandButton.ICON_SKIP_FORWARD_10
        15 -> if (back) CommandButton.ICON_SKIP_BACK_15 else CommandButton.ICON_SKIP_FORWARD_15
        30 -> if (back) CommandButton.ICON_SKIP_BACK_30 else CommandButton.ICON_SKIP_FORWARD_30
        else -> if (back) CommandButton.ICON_SKIP_BACK else CommandButton.ICON_SKIP_FORWARD
    }

    private fun speedIcon(s: Float): Int = when ((s * 100).toInt()) {
        50 -> CommandButton.ICON_PLAYBACK_SPEED_0_5
        100 -> CommandButton.ICON_PLAYBACK_SPEED_1_0
        150 -> CommandButton.ICON_PLAYBACK_SPEED_1_5
        200 -> CommandButton.ICON_PLAYBACK_SPEED_2_0
        else -> CommandButton.ICON_PLAYBACK_SPEED
    }

    private fun launchIntent(): PendingIntent? {
        val intent = packageManager.getLaunchIntentForPackage(packageName) ?: return null
        intent.putExtra(EXTRA_OPEN_PLAYER, true)
        intent.addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }

    companion object {
        /** Set on the activity intent when the notification is tapped. */
        const val EXTRA_OPEN_PLAYER = "org.simpleapb.player.OPEN_PLAYER"

        private const val TICK_MS = 250L
        private const val REPORT_EVERY_MS = 15_000L
        private const val SAVE_EVERY_MS = 5_000L
        private const val SEEK_SETTLE_MS = 1_500L
        private const val MIN_MOVE_MS = 1_000L
        private const val AUTO_NEXT_FRACTION = 0.9
        private const val PUBLISH_EVERY_MS = 10_000L

        /** Calls and alarms always pause. With PAUSE, navigation prompts pause too; with DUCK they lower the volume. */
        fun attributes(focus: FocusMode): AudioAttributes = AudioAttributes
            .Builder()
            .setUsage(C.USAGE_MEDIA)
            .setContentType(if (focus == FocusMode.PAUSE) C.AUDIO_CONTENT_TYPE_SPEECH else C.AUDIO_CONTENT_TYPE_MUSIC)
            .build()
    }
}
