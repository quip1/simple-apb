// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.ui

import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import kotlinx.serialization.Serializable
import org.simpleapb.app.home.HomeScreen
import org.simpleapb.app.player.ConflictDialog
import org.simpleapb.app.player.MiniPlayer
import org.simpleapb.app.player.PlayerScreen
import org.simpleapb.app.player.PlayerViewModel
import org.simpleapb.app.player.clock
import org.simpleapb.app.settings.PlaybackSettingsScreen
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.player.PlayerEvent
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

@Serializable private object HomeRoute

@Serializable private object PlayerRoute

@Serializable private object PlaybackSettingsRoute

/** The signed-in app: screens, the mini player, the conflict prompt and player notices. */
@Composable
fun MainApp(app: AppViewModel, account: Account, accounts: List<Account>, snackbar: SnackbarHostState) {
    val nav = rememberNavController()
    val player: PlayerViewModel = hiltViewModel()
    val eink = LocalEink.current
    val entry by nav.currentBackStackEntryAsState()
    val onPlayer = entry?.destination?.hasRoute<PlayerRoute>() == true
    val openPlayer by app.openPlayer.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val res = LocalResources.current

    LaunchedEffect(openPlayer) {
        if (openPlayer) {
            if (!onPlayer) nav.navigate(PlayerRoute) { launchSingleTop = true }
            app.playerOpened()
        }
    }
    LaunchedEffect(Unit) {
        player.events.collect { e ->
            val text =
                when (e) {
                    is PlayerEvent.Bookmarked -> res.getString(UiR.string.app_player_bookmark_added, clock(e.positionMs))
                    PlayerEvent.SleepExtended -> res.getString(UiR.string.app_player_sleep_extended)
                    PlayerEvent.PlaybackFailed -> res.getString(UiR.string.player_error_playback)
                    is PlayerEvent.LoadFailed -> errorMessage(context, e.failure)
                    is PlayerEvent.BookmarkFailed -> errorMessage(context, e.failure)
                }
            snackbar.showSnackbar(text)
        }
    }

    Column(Modifier.fillMaxSize()) {
        Box(Modifier.weight(1f).fillMaxWidth()) {
            NavHost(
                navController = nav,
                startDestination = HomeRoute,
                // E-ink: no animation at all (brief §11).
                enterTransition = { if (eink) EnterTransition.None else fadeIn() },
                exitTransition = { if (eink) ExitTransition.None else fadeOut() }
            ) {
                composable<HomeRoute> {
                    HomeScreen(
                        app,
                        account,
                        accounts,
                        onPlay = { id ->
                            player.play(id)
                            nav.navigate(PlayerRoute) { launchSingleTop = true }
                        },
                        onPlaybackSettings = { nav.navigate(PlaybackSettingsRoute) { launchSingleTop = true } }
                    )
                }
                composable<PlayerRoute> { PlayerScreen(player, onClose = { nav.popBackStack() }) }
                composable<PlaybackSettingsRoute> { PlaybackSettingsScreen(onBack = { nav.popBackStack() }) }
            }
        }
        if (!onPlayer) MiniPlayer(player, onOpen = { nav.navigate(PlayerRoute) { launchSingleTop = true } })
    }
    ConflictDialog(player)
}
