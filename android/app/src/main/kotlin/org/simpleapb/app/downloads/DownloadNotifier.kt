// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.downloads

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.work.ForegroundInfo
import org.simpleapb.app.MainActivity
import org.simpleapb.core.data.download.DownloadNotifications
import org.simpleapb.core.ui.R as UiR

/** The "Downloading …" notification a running download shows (Android requires one). */
class DownloadNotifier(private val context: Context) : DownloadNotifications {
    init {
        val nm = context.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, context.getString(UiR.string.app_downloads_channel), NotificationManager.IMPORTANCE_LOW)
        )
    }

    override fun foreground(title: String, percent: Int?): ForegroundInfo =
        info(context.getString(UiR.string.app_downloads_notification, title), percent, android.R.drawable.stat_sys_download, ID)

    override fun uploadForeground(title: String, percent: Int?): ForegroundInfo =
        info(context.getString(UiR.string.app_local_upload_notification, title), percent, android.R.drawable.stat_sys_upload, UPLOAD_ID)

    private fun info(text: String, percent: Int?, icon: Int, id: Int): ForegroundInfo {
        val open =
            PendingIntent.getActivity(
                context,
                0,
                Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
            )
        val n =
            NotificationCompat
                .Builder(context, CHANNEL)
                .setSmallIcon(icon)
                .setContentTitle(text)
                .setProgress(100, percent ?: 0, percent == null)
                .setOnlyAlertOnce(true)
                .setOngoing(true)
                .setSilent(true)
                .setContentIntent(open)
                .build()
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ForegroundInfo(id, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            ForegroundInfo(id, n)
        }
    }

    private companion object {
        const val CHANNEL = "downloads"
        const val ID = 4201
        const val UPLOAD_ID = 4202
    }
}
