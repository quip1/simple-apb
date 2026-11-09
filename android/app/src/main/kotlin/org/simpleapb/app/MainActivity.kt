// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app

import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dagger.hilt.android.AndroidEntryPoint
import org.simpleapb.app.ui.AppRoot
import org.simpleapb.app.ui.AppViewModel
import org.simpleapb.core.ui.AppTheme
import org.simpleapb.core.ui.SimpleApbTheme

@AndroidEntryPoint
class MainActivity : ComponentActivity() {
    private val app: AppViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            val theme by app.theme.collectAsStateWithLifecycle()
            // Light system-bar icons on the dark theme, dark ones on e-ink white.
            LaunchedEffect(theme) {
                val style =
                    if (theme == AppTheme.EINK) {
                        SystemBarStyle.light(Color.WHITE, Color.WHITE)
                    } else {
                        SystemBarStyle.dark(Color.TRANSPARENT)
                    }
                enableEdgeToEdge(statusBarStyle = style, navigationBarStyle = style)
            }
            SimpleApbTheme(theme) {
                AppRoot(app)
            }
        }
    }
}
