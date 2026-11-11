// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.onboarding

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import java.util.concurrent.Executors
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.ui.R as UiR

/**
 * Reads a server address from a QR code (brief §7.4 onboarding), such as
 * the one the web app shows under Settings. CameraX and the ZXing decoder,
 * both open source, so the F-Droid build has it too. The camera is used only
 * while this is on screen.
 */
@Composable
fun QrScanner(onResult: (String) -> Unit, onCancel: () -> Unit) {
    val context = LocalContext.current
    var allowed by remember {
        mutableStateOf(
            ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED
        )
    }
    var denied by remember { mutableStateOf(false) }
    val ask =
        rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
            allowed = it
            denied = !it
        }
    LaunchedEffect(Unit) { if (!allowed) ask.launch(Manifest.permission.CAMERA) }
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(UiR.string.app_server_scan_help))
        if (denied) Notice(stringResource(UiR.string.app_server_scan_denied))
        if (allowed) CameraPreview(onResult)
        OutlinedButton(onClick = onCancel, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.common_cancel))
        }
    }
}

@Composable
private fun CameraPreview(onResult: (String) -> Unit) {
    val context = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val executor = remember { Executors.newSingleThreadExecutor() }
    var done by remember { mutableStateOf(false) }
    val label = stringResource(UiR.string.app_server_scan)
    val reader = remember { qrReader() }
    DisposableEffect(Unit) { onDispose { executor.shutdown() } }
    AndroidView(
        modifier = Modifier.fillMaxWidth().aspectRatio(1f).semantics { contentDescription = label },
        factory = { ctx ->
            val view = PreviewView(ctx)
            val providerFuture = ProcessCameraProvider.getInstance(ctx)
            providerFuture.addListener(
                {
                    val provider = providerFuture.get()
                    val preview = Preview.Builder().build().also { it.surfaceProvider = view.surfaceProvider }
                    val analysis =
                        ImageAnalysis
                            .Builder()
                            .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                            .build()
                    analysis.setAnalyzer(executor) { image ->
                        val text = decode(reader, image)
                        image.close()
                        if (text != null && !done) {
                            done = true
                            ContextCompat.getMainExecutor(context).execute {
                                provider.unbindAll()
                                onResult(text)
                            }
                        }
                    }
                    runCatching {
                        provider.unbindAll()
                        provider.bindToLifecycle(owner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
                    }
                },
                ContextCompat.getMainExecutor(ctx)
            )
            view
        }
    )
}

/** The text of a QR code in the camera frame's brightness plane, if there is one. */
private fun decode(reader: MultiFormatReader, image: ImageProxy): String? {
    val plane = image.planes.firstOrNull() ?: return null
    val buf = plane.buffer
    val data = ByteArray(buf.remaining()).also { buf.get(it) }
    return decodeLuminance(reader, data, plane.rowStride, image.width, image.height)
}

/** Decodes a QR code from 8-bit brightness values ([stride] bytes per row). */
internal fun decodeLuminance(reader: MultiFormatReader, data: ByteArray, stride: Int, width: Int, height: Int): String? {
    val source = PlanarYUVLuminanceSource(data, stride, height, 0, 0, width, height, false)
    return try {
        reader.decodeWithState(BinaryBitmap(HybridBinarizer(source))).text
    } catch (_: Exception) {
        null
    } finally {
        reader.reset()
    }
}

/** A reader for QR codes only. */
internal fun qrReader() = MultiFormatReader().apply {
    setHints(
        mapOf(
            DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE),
            DecodeHintType.TRY_HARDER to true
        )
    )
}
