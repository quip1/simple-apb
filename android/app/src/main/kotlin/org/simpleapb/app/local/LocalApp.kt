// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.local

import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import kotlinx.serialization.Serializable
import org.simpleapb.app.player.CarModeScreen
import org.simpleapb.app.player.MiniPlayer
import org.simpleapb.app.player.PlayerScreen
import org.simpleapb.app.player.PlayerViewModel
import org.simpleapb.app.settings.PlaybackSettingsScreen
import org.simpleapb.app.ui.AppViewModel
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.RadioGroup
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.Section
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.ui.AppTheme
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

@Serializable private object LocalHomeRoute

@Serializable private data class LocalOnlyBookRoute(val id: String)

@Serializable private object LocalPlayerRoute

@Serializable private object LocalCarRoute

@Serializable private object LocalFoldersRoute

@Serializable private object LocalPlaybackRoute

/**
 * "Just play files on this device" (brief §7.4 onboarding): the app without
 * a server. Books from picked folders, the player, car mode and playback
 * settings; a server can be connected at any time.
 */
@Composable
fun LocalApp(app: AppViewModel) {
    val nav = rememberNavController()
    val player: PlayerViewModel = hiltViewModel()
    val eink = LocalEink.current
    val entry by nav.currentBackStackEntryAsState()
    val full = entry?.destination?.let { it.hasRoute<LocalPlayerRoute>() || it.hasRoute<LocalCarRoute>() } == true
    val back: () -> Unit = { nav.popBackStack() }
    val showPlayer = { nav.navigate(LocalPlayerRoute) { launchSingleTop = true } }
    Column(Modifier.fillMaxSize()) {
        Box(Modifier.weight(1f).fillMaxWidth()) {
            NavHost(
                navController = nav,
                startDestination = LocalHomeRoute,
                enterTransition = { if (eink) EnterTransition.None else fadeIn() },
                exitTransition = { if (eink) ExitTransition.None else fadeOut() }
            ) {
                composable<LocalHomeRoute> {
                    LocalHome(
                        app,
                        onBook = { nav.navigate(LocalOnlyBookRoute(it)) },
                        onFolders = { nav.navigate(LocalFoldersRoute) },
                        onPlayback = { nav.navigate(LocalPlaybackRoute) }
                    )
                }
                composable<LocalOnlyBookRoute> { e ->
                    LocalBookScreen(e.toRoute<LocalOnlyBookRoute>().id, null, onBack = back, onPlayer = showPlayer)
                }
                composable<LocalPlayerRoute> {
                    PlayerScreen(player, onClose = back, onCarMode = { nav.navigate(LocalCarRoute) { launchSingleTop = true } })
                }
                composable<LocalCarRoute> { CarModeScreen(player, onClose = back) }
                composable<LocalFoldersRoute> { FoldersScreen(onBack = back) }
                composable<LocalPlaybackRoute> { PlaybackSettingsScreen(onBack = back) }
            }
        }
        if (!full) MiniPlayer(player, onOpen = showPlayer)
    }
}

@Composable
private fun LocalHome(app: AppViewModel, onBook: (String) -> Unit, onFolders: () -> Unit, onPlayback: () -> Unit) {
    val vm: LocalViewModel = hiltViewModel()
    val books by vm.books.collectAsStateWithLifecycle()
    val progress by vm.progress.collectAsStateWithLifecycle()
    val trees by vm.trees.collectAsStateWithLifecycle()
    val theme by app.theme.collectAsStateWithLifecycle()
    FormColumn {
        ScreenHeader(stringResource(UiR.string.app_local_title)) {
            IconButton(onClick = vm::rescan, modifier = Modifier.heightIn(min = TouchTarget)) {
                Icon(Icons.Filled.Refresh, contentDescription = stringResource(UiR.string.app_local_rescan))
            }
        }
        if (books.isEmpty()) {
            Text(stringResource(if (trees.isEmpty()) UiR.string.app_local_empty else UiR.string.app_local_empty_folders))
            Button(onClick = onFolders, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_local_add))
            }
        }
        books.forEach { b ->
            LocalBookRow(b, progress[b.id] ?: 0) { onBook(b.id) }
            HorizontalDivider()
        }
        Section(stringResource(UiR.string.nav_settings)) {
            OutlinedButton(onClick = onFolders, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_local_folders))
            }
            OutlinedButton(onClick = onPlayback, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_settings_playback_title))
            }
            Text(stringResource(UiR.string.settings_theme), style = MaterialTheme.typography.labelLarge)
            RadioGroup(theme ?: AppTheme.DARK, AppTheme.entries, {
                stringResource(
                    if (it ==
                        AppTheme.EINK
                    ) {
                        UiR.string.settings_theme_eink
                    } else {
                        UiR.string.settings_theme_dark
                    }
                )
            }) {
                app.setTheme(it)
            }
        }
        Section(stringResource(UiR.string.app_local_server)) {
            Text(stringResource(UiR.string.app_local_server_help))
            Button(onClick = app::startAdding, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_welcome_connect))
            }
        }
    }
}
