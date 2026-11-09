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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.LibraryBooks
import androidx.compose.material.icons.filled.BarChart
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavDestination
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import kotlinx.serialization.Serializable
import org.simpleapb.app.book.BookScreen
import org.simpleapb.app.book.EditBookScreen
import org.simpleapb.app.downloads.DownloadsScreen
import org.simpleapb.app.home.HomeScreen
import org.simpleapb.app.library.LibraryScreen
import org.simpleapb.app.library.PersonKind
import org.simpleapb.app.library.PersonScreen
import org.simpleapb.app.player.CarDetector
import org.simpleapb.app.player.CarModeScreen
import org.simpleapb.app.player.ConflictDialog
import org.simpleapb.app.player.MiniPlayer
import org.simpleapb.app.player.PlayerScreen
import org.simpleapb.app.player.PlayerViewModel
import org.simpleapb.app.player.clock
import org.simpleapb.app.settings.AccountsScreen
import org.simpleapb.app.settings.EnrollRequiredScreen
import org.simpleapb.app.settings.PlaybackSettingsScreen
import org.simpleapb.app.settings.SecurityScreen
import org.simpleapb.app.settings.SettingsScreen
import org.simpleapb.app.stats.StatsScreen
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.data.download.DownloadState
import org.simpleapb.core.player.PlayerEvent
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

@Serializable private object HomeRoute

@Serializable private data class LibraryRoute(val shelf: String? = null, val tag: String? = null, val tagName: String? = null)

@Serializable private object DownloadsRoute

@Serializable private object StatsRoute

@Serializable private object SettingsRoute

@Serializable private data class BookRoute(val id: String)

@Serializable private data class EditBookRoute(val id: String)

@Serializable private data class PersonRoute(val kind: PersonKind, val id: String)

@Serializable private object PlayerRoute

@Serializable private object CarModeRoute

@Serializable private object PlaybackSettingsRoute

@Serializable private object SecurityRoute

@Serializable private object AccountsRoute

private enum class Tab(val route: Any, val icon: ImageVector, val label: Int) {
    HOME(HomeRoute, Icons.Filled.Home, UiR.string.nav_home),
    LIBRARY(LibraryRoute(), Icons.AutoMirrored.Filled.LibraryBooks, UiR.string.nav_library),
    DOWNLOADS(DownloadsRoute, Icons.Filled.Download, UiR.string.app_nav_downloads),
    STATS(StatsRoute, Icons.Filled.BarChart, UiR.string.nav_stats),
    SETTINGS(SettingsRoute, Icons.Filled.Settings, UiR.string.nav_settings);

    fun matches(d: NavDestination?): Boolean = when (this) {
        HOME -> d?.hasRoute<HomeRoute>() == true
        LIBRARY -> d?.hasRoute<LibraryRoute>() == true
        DOWNLOADS -> d?.hasRoute<DownloadsRoute>() == true
        STATS -> d?.hasRoute<StatsRoute>() == true
        SETTINGS -> d?.hasRoute<SettingsRoute>() == true
    }
}

/** The signed-in app: tabs, screens, the mini player, the conflict prompt and player notices. */
@Composable
fun MainApp(app: AppViewModel, account: Account, accounts: List<Account>, snackbar: SnackbarHostState) {
    val me by app.me.collectAsStateWithLifecycle()
    val m = me
    if (m != null && m.totpRequired && !m.totpEnabled) {
        EnrollRequiredScreen(app, account)
        return
    }
    val nav = rememberNavController()
    val player: PlayerViewModel = hiltViewModel()
    val eink = LocalEink.current
    val entry by nav.currentBackStackEntryAsState()
    val destination = entry?.destination
    val onPlayer = destination?.hasRoute<PlayerRoute>() == true
    val onCar = destination?.hasRoute<CarModeRoute>() == true
    val openPlayer by app.openPlayer.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val res = LocalResources.current
    // The tab stays lit while the user is on a page opened from it.
    var tab by rememberSaveable { mutableStateOf(Tab.HOME) }
    val lit = Tab.entries.firstOrNull { it.matches(destination) } ?: tab
    LaunchedEffect(lit) { tab = lit }

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

    // Car mode opens by itself when a car's Bluetooth connects, if the user asked for it.
    val playerSettings by player.settings.collectAsStateWithLifecycle()
    if (playerSettings.carModeAuto) {
        DisposableEffect(Unit) {
            val detector = CarDetector(context) { nav.navigate(CarModeRoute) { launchSingleTop = true } }
            detector.start()
            onDispose { detector.stop() }
        }
    }

    val openBook = { id: String -> nav.navigate(BookRoute(id)) }
    val openPerson = { k: PersonKind, id: String -> nav.navigate(PersonRoute(k, id)) }
    val showPlayer = { nav.navigate(PlayerRoute) { launchSingleTop = true } }
    val back: () -> Unit = { nav.popBackStack() }

    val downloads by app.downloads.collectAsStateWithLifecycle()
    val downloaded =
        remember(downloads, account.id) {
            downloads.filter { it.accountId == account.id && it.state == DownloadState.DONE }.map { it.bookId }.toSet()
        }
    CompositionLocalProvider(LocalDownloaded provides downloaded) {
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
                        HomeScreen(account, onBook = openBook, onLibrary = { shelf -> nav.navigate(LibraryRoute(shelf = shelf)) })
                    }
                    composable<LibraryRoute> { e ->
                        val r = e.toRoute<LibraryRoute>()
                        LibraryScreen(account, r.shelf, r.tag, r.tagName, onBook = openBook, onPerson = openPerson)
                    }
                    composable<DownloadsRoute> { DownloadsScreen(account, onBook = openBook) }
                    composable<StatsRoute> { StatsScreen(account, onBook = openBook, onAuthor = { openPerson(PersonKind.AUTHOR, it) }) }
                    composable<SettingsRoute> {
                        SettingsScreen(
                            account,
                            onPlayback = { nav.navigate(PlaybackSettingsRoute) },
                            onSecurity = { nav.navigate(SecurityRoute) },
                            onAccounts = { nav.navigate(AccountsRoute) },
                            onSignOut = { app.signOut(account) }
                        )
                    }
                    composable<BookRoute> { e ->
                        val id = e.toRoute<BookRoute>().id
                        BookScreen(
                            account,
                            id,
                            onBack = back,
                            onPlayer = showPlayer,
                            onPerson = openPerson,
                            onGenre = { t -> nav.navigate(LibraryRoute(tag = t.id.toString(), tagName = t.name)) },
                            onEdit = { nav.navigate(EditBookRoute(id)) }
                        )
                    }
                    composable<EditBookRoute> { e -> EditBookScreen(e.toRoute<EditBookRoute>().id, onDone = back) }
                    composable<PersonRoute> { e ->
                        val r = e.toRoute<PersonRoute>()
                        PersonScreen(account, r.kind, r.id, onBack = back, onBook = openBook)
                    }
                    composable<PlayerRoute> {
                        PlayerScreen(player, onClose = back, onCarMode = { nav.navigate(CarModeRoute) { launchSingleTop = true } })
                    }
                    composable<CarModeRoute> { CarModeScreen(player, onClose = back) }
                    composable<PlaybackSettingsRoute> { PlaybackSettingsScreen(onBack = back) }
                    composable<SecurityRoute> { SecurityScreen(onBack = back) }
                    composable<AccountsRoute> { AccountsScreen(app, account, accounts, onBack = back) }
                }
            }
            if (!onPlayer && !onCar) {
                MiniPlayer(player, onOpen = showPlayer)
                TabBar(lit) { t -> nav.goToTab(t) }
            }
        }
    }
    ConflictDialog(player)
}

private fun NavHostController.goToTab(t: Tab) {
    navigate(t.route) {
        popUpTo(graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = true
    }
}

/** Bottom tabs. The selected tab is bold (and underlined on e-ink), never colour alone (brief §11). */
@Composable
private fun TabBar(selected: Tab, onSelect: (Tab) -> Unit) {
    val eink = LocalEink.current
    if (eink) HorizontalDivider(thickness = 2.dp)
    NavigationBar(tonalElevation = if (eink) 0.dp else 3.dp) {
        Tab.entries.forEach { t ->
            val on = t == selected
            NavigationBarItem(
                selected = on,
                onClick = { onSelect(t) },
                icon = { Icon(t.icon, contentDescription = null) },
                label = {
                    Text(
                        stringResource(t.label),
                        fontWeight = if (on) FontWeight.Bold else FontWeight.Normal,
                        textDecoration = if (on && eink) TextDecoration.Underline else null
                    )
                },
                colors = if (eink) NavigationBarItemDefaults.colors(indicatorColor = Color.Transparent) else NavigationBarItemDefaults.colors()
            )
        }
    }
}
