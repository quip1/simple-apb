// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app

import android.app.Application
import android.content.Context
import android.os.Build
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.HiltAndroidApp
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import org.simpleapb.core.data.net.AppInfo
import org.simpleapb.core.ui.AppTheme

@HiltAndroidApp
class SimpleApbApp : Application()

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
    fun uiSettings(@ApplicationContext context: Context): UiSettings = UiSettings(context.uiStore)
}
