// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.simpleapb.app.update.AppUpdates

class AppUpdatesTest {
    @Test
    fun versionsCompareAsSemver() {
        assertTrue(AppUpdates.newer("0.5.0", "0.4.9"))
        assertTrue(AppUpdates.newer("0.10.0", "0.9.0"))
        assertTrue(AppUpdates.newer("0.5.0", "0.5.0-rc.1"))
        assertFalse(AppUpdates.newer("0.5.0", "0.5.0"))
        assertFalse(AppUpdates.newer("0.5.0-rc.1", "0.5.0"))
        assertFalse(AppUpdates.newer("v0.6.0", "0.5.0"))
    }

    private fun release(json: String) = AppUpdates.parse(Json.parseToJsonElement(json).jsonObject)

    @Test
    fun findsTheReleaseApk() {
        val r =
            release(
                """{"tag_name":"v0.6.0","html_url":"https://github.com/o/r/releases/tag/v0.6.0","assets":[
                {"name":"simple-apb-windows-0.6.0-setup.exe","browser_download_url":"https://github.com/o/r/w.exe"},
                {"name":"simple-apb-android-0.6.0-play.aab","browser_download_url":"https://github.com/o/r/a.aab"},
                {"name":"simple-apb-android-0.6.0.apk","browser_download_url":"https://github.com/o/r/a.apk"}]}"""
            )
        assertEquals("0.6.0", r?.version)
        assertEquals("https://github.com/o/r/a.apk", r?.apkUrl)
        // No APK (a release without Android signing keys), a pre-release, or a foreign host: nothing offered.
        assertNull(release("""{"tag_name":"v0.6.0","assets":[]}"""))
        assertNull(
            release(
                """{"tag_name":"v0.7.0-rc.1","prerelease":true,"assets":[{"name":"simple-apb-android-0.7.0-rc.1.apk","browser_download_url":"https://github.com/x"}]}"""
            )
        )
        assertNull(
            release(
                """{"tag_name":"v0.6.0","assets":[{"name":"simple-apb-android-0.6.0.apk","browser_download_url":"https://evil.example/a.apk"}]}"""
            )
        )
    }
}
