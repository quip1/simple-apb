// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app

import android.app.Application
import android.content.Context
import android.os.Build
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import androidx.hilt.work.HiltWorkerFactory
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import androidx.work.Configuration
import coil3.ImageLoader
import coil3.PlatformContext
import coil3.SingletonImageLoader
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.HiltAndroidApp
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import org.simpleapb.app.downloads.DownloadNotifier
import org.simpleapb.app.push.PushManager
import org.simpleapb.app.widget.WidgetUpdater
import org.simpleapb.core.data.download.DownloadNotifications
import org.simpleapb.core.data.local.LocalFolders
import org.simpleapb.core.data.net.AccountCalls
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.data.net.AppScope
import org.simpleapb.core.data.sync.LiveHints
import org.simpleapb.core.data.sync.SyncEngine
import org.simpleapb.core.ui.AppTheme

@HiltAndroidApp
class SimpleApbApp :
    Application(),
    SingletonImageLoader.Factory,
    Configuration.Provider {
    @Inject lateinit var calls: AccountCalls

    @Inject lateinit var workers: HiltWorkerFactory

    @Inject lateinit var sync: SyncEngine

    @Inject lateinit var live: LiveHints

    @Inject lateinit var widgets: WidgetUpdater

    @Inject lateinit var push: PushManager

    @Inject lateinit var folders: LocalFolders

    @Inject @AppScope
    lateinit var appScope: CoroutineScope

    override val workManagerConfiguration: Configuration
        get() = Configuration.Builder().setWorkerFactory(workers).build()

    override fun onCreate() {
        super.onCreate()
        // Sync on reconnect and every 15 minutes; live hints while the app is open (brief §7.5).
        sync.schedule()
        live.start()
        widgets.start(this)
        push.start()
        // Folders on this device: rescanned whenever the app comes to the front (at most once a minute).
        folders.scanSoon()
        ProcessLifecycleOwner.get().lifecycle.addObserver(
            object : DefaultLifecycleObserver {
                override fun onStart(owner: LifecycleOwner) {
                    folders.scanSoon()
                    push.refresh()
                }
            }
        )
        appScope.launch { folders.observe() }
    }

    /** Covers are fetched signed in, as the account whose server they come from. */
    override fun newImageLoader(context: PlatformContext): ImageLoader = ImageLoader
        .Builder(context)
        .components { add(OkHttpNetworkFetcherFactory(callFactory = { calls })) }
        .build()
}

private val Context.uiStore: DataStore<Preferences> by preferencesDataStore(name = "ui")

/** Per-device UI preferences (the theme is per device, like on the web). */
class UiSettings(private val store: DataStore<Preferences>) {
    private val themeKey = stringPreferencesKey("theme")

    val theme: Flow<AppTheme> =
        store.data.map { prefs ->
            prefs[themeKey]?.let { runCatching { AppTheme.valueOf(it) }.getOrNull() } ?: AppTheme.DARK
        }

    suspend fun setTheme(theme: AppTheme) {
        store.edit { it[themeKey] = theme.name }
    }

    private val localKey = booleanPreferencesKey("local_mode")

    /** The user chose to play files on this device without a server. */
    val localMode: Flow<Boolean> = store.data.map { it[localKey] ?: false }

    suspend fun setLocalMode(on: Boolean) {
        store.edit { it[localKey] = on }
    }

    private val listKey = booleanPreferencesKey("library_list")

    /** The library shows a list instead of a grid of covers. */
    val libraryList: Flow<Boolean> = store.data.map { it[listKey] ?: false }

    suspend fun setLibraryList(list: Boolean) {
        store.edit { it[listKey] = list }
    }
}

@Module
@InstallIn(SingletonComponent::class)
object AppModule {
    @Provides
    @Singleton
    fun appInfo(): AppInfo = AppInfo(
        versionName = BuildConfig.VERSION_NAME,
        deviceName = listOf(Build.MANUFACTURER, Build.MODEL).joinToString(" ").trim().ifEmpty { "Android" }
    )

    @Provides
    @Singleton
    fun downloadNotifications(@ApplicationContext context: Context): DownloadNotifications = DownloadNotifier(context)

    @Provides
    @Singleton
    fun uiSettings(@ApplicationContext context: Context): UiSettings = UiSettings(context.uiStore)
}
