// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.ui

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/** The two first-class themes (brief §11). */
enum class AppTheme { DARK, EINK }

/** True in the e-ink theme: no animations, paged lists, slow progress updates. */
val LocalEink = staticCompositionLocalOf { false }

/** Same tokens as the web app (web/src/styles/app.css), Fern accent (ADR-0038). */
object Palette {
    val Bg = Color(0xFF0F1115)
    val Surface = Color(0xFF171A20)
    val Surface2 = Color(0xFF1F232B)
    val Border = Color(0xFF2C313B)
    val Text = Color(0xFFECEBE8)
    val Muted = Color(0xFFA8ACB5)
    val Accent = Color(0xFF94D68F)
    val AccentText = Color(0xFF0B230C)
    val Danger = Color(0xFFFF8A8A)
    val DangerBg = Color(0xFF3A1C1F)
    val Ok = Color(0xFF7FD6A2)
    val Focus = Color(0xFFFFD479)
}

private val dark: ColorScheme =
    darkColorScheme(
        primary = Palette.Accent,
        onPrimary = Palette.AccentText,
        primaryContainer = Palette.Surface2,
        onPrimaryContainer = Palette.Text,
        secondary = Palette.Accent,
        onSecondary = Palette.AccentText,
        secondaryContainer = Palette.Surface2,
        onSecondaryContainer = Palette.Text,
        tertiary = Palette.Ok,
        background = Palette.Bg,
        onBackground = Palette.Text,
        surface = Palette.Bg,
        onSurface = Palette.Text,
        surfaceVariant = Palette.Surface,
        onSurfaceVariant = Palette.Muted,
        surfaceContainerLowest = Palette.Bg,
        surfaceContainerLow = Palette.Surface,
        surfaceContainer = Palette.Surface,
        surfaceContainerHigh = Palette.Surface2,
        surfaceContainerHighest = Palette.Surface2,
        outline = Palette.Border,
        outlineVariant = Palette.Border,
        error = Palette.Danger,
        onError = Palette.Bg,
        errorContainer = Palette.DangerBg,
        onErrorContainer = Palette.Danger
    )

/** Pure black on white: no greys except hairlines, no tints (brief §11). */
private val eink: ColorScheme =
    lightColorScheme(
        primary = Color.Black,
        onPrimary = Color.White,
        primaryContainer = Color.White,
        onPrimaryContainer = Color.Black,
        secondary = Color.Black,
        onSecondary = Color.White,
        secondaryContainer = Color.White,
        onSecondaryContainer = Color.Black,
        tertiary = Color.Black,
        background = Color.White,
        onBackground = Color.Black,
        surface = Color.White,
        onSurface = Color.Black,
        surfaceVariant = Color.White,
        onSurfaceVariant = Color.Black,
        surfaceTint = Color.White,
        surfaceContainerLowest = Color.White,
        surfaceContainerLow = Color.White,
        surfaceContainer = Color.White,
        surfaceContainerHigh = Color.White,
        surfaceContainerHighest = Color.White,
        outline = Color.Black,
        outlineVariant = Color.Black,
        error = Color.Black,
        onError = Color.White,
        errorContainer = Color.White,
        onErrorContainer = Color.Black,
        scrim = Color.White
    )

private val shapes =
    Shapes(
        small = RoundedCornerShape(8.dp),
        medium = RoundedCornerShape(12.dp),
        large = RoundedCornerShape(16.dp)
    )

@Composable
fun SimpleApbTheme(theme: AppTheme = AppTheme.DARK, content: @Composable () -> Unit) {
    CompositionLocalProvider(LocalEink provides (theme == AppTheme.EINK)) {
        MaterialTheme(
            colorScheme = if (theme == AppTheme.EINK) eink else dark,
            shapes = shapes,
            content = content
        )
    }
}
