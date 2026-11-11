// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import kotlinx.coroutines.delay
import kotlinx.serialization.Serializable
import org.simpleapb.app.local.LocalApp
import org.simpleapb.app.onboarding.OnboardingViewModel
import org.simpleapb.app.onboarding.RequestAccountScreen
import org.simpleapb.app.onboarding.ServerScreen
import org.simpleapb.app.onboarding.SignInScreen
import org.simpleapb.app.onboarding.TourScreen
import org.simpleapb.app.onboarding.WelcomeScreen
import org.simpleapb.core.ui.LocalEink
import org.simpleapb.core.ui.R as UiR

@Serializable private object Welcome

@Serializable private object Server

@Serializable private object SignIn

@Serializable private object RequestAccount

/**
 * Top level: onboarding when there is no account (or another server is being
 * added), the tour after the first sign-in, otherwise the main app.
 */
@Composable
fun AppRoot(app: AppViewModel) {
    val accounts by app.accounts.collectAsStateWithLifecycle()
    val tour by app.tour.collectAsStateWithLifecycle()
    val adding by app.adding.collectAsStateWithLifecycle()
    val signedOut by app.signedOut.collectAsStateWithLifecycle()
    val localMode by app.localMode.collectAsStateWithLifecycle()
    val snackbar = remember { SnackbarHostState() }
    val signedOutText = signedOut?.let { stringResource(UiR.string.app_account_signed_out, it) }
    LaunchedEffect(signedOutText) {
        if (signedOutText != null) {
            snackbar.showSnackbar(signedOutText)
            app.dismissSignedOut()
        }
    }
    // E-ink: a full-screen black flash then white clears ghosting (brief §11).
    var flash by remember { mutableStateOf(false) }
    LaunchedEffect(flash) {
        if (flash) {
            delay(FLASH_MS)
            flash = false
        }
    }
    CompositionLocalProvider(LocalScreenRefresh provides { flash = true }) {
        Scaffold(snackbarHost = { SnackbarHost(snackbar) }) { padding ->
            Box(Modifier.fillMaxSize().padding(padding)) {
                val state = accounts ?: return@Box // still loading from disk
                val local = localMode ?: return@Box
                val active = state.active
                when {
                    // Files on this device only, until a server is connected.
                    active == null && local && !adding -> LocalApp(app)
                    active == null || adding ->
                        Onboarding(
                            firstAccount = state.accounts.isEmpty() && !local,
                            canCancel = active != null || local,
                            onCancel = app::stopAdding,
                            onSignedIn = app::signedIn,
                            onLocal = { app.useLocal(true) }
                        )
                    tour -> TourScreen(active.serverName, onDone = app::tourDone)
                    else -> MainApp(app, active, state.accounts, snackbar)
                }
            }
        }
        if (flash) Box(Modifier.fillMaxSize().background(Color.Black))
    }
}

private const val FLASH_MS = 250L

@Composable
private fun Onboarding(firstAccount: Boolean, canCancel: Boolean, onCancel: () -> Unit, onSignedIn: (Boolean) -> Unit, onLocal: () -> Unit) {
    val nav = rememberNavController()
    val vm: OnboardingViewModel = hiltViewModel()
    val eink = LocalEink.current
    BackHandler(enabled = canCancel && nav.previousBackStackEntry == null) { onCancel() }
    NavHost(
        navController = nav,
        startDestination = if (firstAccount) Welcome else Server,
        // E-ink: no animation at all (brief §11).
        enterTransition = { if (eink) EnterTransition.None else androidx.compose.animation.fadeIn() },
        exitTransition = { if (eink) ExitTransition.None else androidx.compose.animation.fadeOut() }
    ) {
        composable<Welcome> { WelcomeScreen(onConnect = { nav.navigate(Server) }, onLocal = onLocal) }
        composable<Server> { ServerScreen(vm, onReady = { nav.navigate(SignIn) }) }
        composable<SignIn> {
            SignInScreen(
                vm,
                onSignedIn = { onSignedIn(firstAccount) },
                onRequestAccount = { nav.navigate(RequestAccount) }
            )
        }
        composable<RequestAccount> { RequestAccountScreen(vm, onBackToSignIn = { nav.popBackStack() }) }
    }
}
