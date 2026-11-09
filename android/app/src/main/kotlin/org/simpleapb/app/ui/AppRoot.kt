// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import kotlinx.serialization.Serializable
import org.simpleapb.app.home.HomeScreen
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
    val snackbar = remember { SnackbarHostState() }
    val signedOutText = signedOut?.let { stringResource(UiR.string.app_account_signed_out, it) }
    LaunchedEffect(signedOutText) {
        if (signedOutText != null) {
            snackbar.showSnackbar(signedOutText)
            app.dismissSignedOut()
        }
    }
    Scaffold(snackbarHost = { SnackbarHost(snackbar) }) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            val state = accounts ?: return@Box // still loading from disk
            val active = state.active
            when {
                active == null || adding ->
                    Onboarding(
                        firstAccount = state.accounts.isEmpty(),
                        canCancel = active != null,
                        onCancel = app::stopAdding,
                        onSignedIn = app::signedIn
                    )
                tour -> TourScreen(active.serverName, onDone = app::tourDone)
                else -> HomeScreen(app, active, state.accounts)
            }
        }
    }
}

@Composable
private fun Onboarding(firstAccount: Boolean, canCancel: Boolean, onCancel: () -> Unit, onSignedIn: (Boolean) -> Unit) {
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
        composable<Welcome> { WelcomeScreen(onConnect = { nav.navigate(Server) }) }
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
