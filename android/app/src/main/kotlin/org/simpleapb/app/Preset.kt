// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app

/**
 * A build for one server (ADR-0050): the address is baked in at build time
 * (SIMPLEAPB_PRESET_SERVER), so people sign in without any setup. Such a build
 * shows no server screen, no "add another server" and no files-only mode.
 * Null in every ordinary build.
 */
object Preset {
    val server: String? = BuildConfig.PRESET_SERVER.ifEmpty { null }
}
