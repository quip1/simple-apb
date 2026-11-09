// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.player

import android.Manifest
import android.annotation.SuppressLint
import android.bluetooth.BluetoothClass
import android.bluetooth.BluetoothManager
import android.content.Context
import android.content.pm.PackageManager
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import androidx.core.content.ContextCompat

/**
 * Notices a car's Bluetooth audio connecting while the app is open, to open
 * car mode by itself (brief §7.2, optional). Telling a car from headphones
 * needs the Bluetooth device class, so it works only with the "nearby
 * devices" permission on Android 12+; without it nothing happens.
 */
class CarDetector(private val context: Context, private val onCar: () -> Unit) {
    private val audio = context.getSystemService(AudioManager::class.java)
    private val handled = mutableSetOf<String>()

    private val callback =
        object : AudioDeviceCallback() {
            override fun onAudioDevicesAdded(addedDevices: Array<out AudioDeviceInfo>) {
                // Once per connection: leaving car mode isn't undone by the next callback.
                val car = addedDevices.firstOrNull { isCar(it) && handled.add(key(it)) }
                if (car != null) onCar()
            }

            override fun onAudioDevicesRemoved(removedDevices: Array<out AudioDeviceInfo>) {
                removedDevices.forEach { handled.remove(key(it)) }
            }
        }

    /** One connection of one device (the Bluetooth address where Android gives it). */
    private fun key(d: AudioDeviceInfo): String = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) d.address else d.id.toString()

    fun start() = audio.registerAudioDeviceCallback(callback, Handler(Looper.getMainLooper()))

    fun stop() = audio.unregisterAudioDeviceCallback(callback)

    @SuppressLint("MissingPermission") // checked in allowed()
    private fun isCar(d: AudioDeviceInfo): Boolean {
        if (d.type != AudioDeviceInfo.TYPE_BLUETOOTH_A2DP || Build.VERSION.SDK_INT < Build.VERSION_CODES.P) return false
        if (!allowed(context)) return false
        val adapter = context.getSystemService(BluetoothManager::class.java)?.adapter ?: return false
        val cls = runCatching { adapter.getRemoteDevice(d.address).bluetoothClass }.getOrNull() ?: return false
        // Car stereos report "car audio" or "hands-free"; headphones and headsets report their own classes.
        return cls.deviceClass in CAR_CLASSES
    }

    companion object {
        /** The permission needed to read a device's class (Android 12+). */
        val PERMISSION: String? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) Manifest.permission.BLUETOOTH_CONNECT else null

        fun allowed(context: Context): Boolean =
            PERMISSION == null || ContextCompat.checkSelfPermission(context, PERMISSION) == PackageManager.PERMISSION_GRANTED

        val CAR_CLASSES = setOf(BluetoothClass.Device.AUDIO_VIDEO_CAR_AUDIO, BluetoothClass.Device.AUDIO_VIDEO_HANDSFREE)
    }
}
