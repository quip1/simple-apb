// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.core.player

import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor.AudioFormat
import androidx.media3.common.audio.BaseAudioProcessor
import androidx.media3.common.util.UnstableApi
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.exp
import kotlin.math.pow
import kotlin.math.roundToInt
import kotlin.math.sin
import kotlin.math.sqrt

/** The audio effects (brief §7.2), applied in the player itself so they work on every output. */
@kotlinx.serialization.Serializable
data class EffectsConfig(
    /** Volume boost, 0 to +12 dB; a limiter keeps the result from clipping. */
    val boostDb: Int = 0,
    /** Voice clarity: high-pass at ~100 Hz and a presence lift around 3 kHz. */
    val voiceEq: Boolean = false,
    val mono: Boolean = false,
    /** -1 (left only) … 0 … +1 (right only). */
    val balance: Float = 0f
) {
    val neutral get() = boostDb == 0 && !voiceEq && !mono && balance == 0f

    companion object {
        val BOOST_RANGE = 0..12
    }
}

/** A biquad filter (RBJ cookbook), one state per channel. */
internal class Biquad(private val b0: Double, private val b1: Double, private val b2: Double, private val a1: Double, private val a2: Double) {
    private var x1 = 0.0
    private var x2 = 0.0
    private var y1 = 0.0
    private var y2 = 0.0

    fun process(x: Double): Double {
        val y = b0 * x + b1 * x1 + b2 * x2 - a1 * y1 - a2 * y2
        x2 = x1
        x1 = x
        y2 = y1
        y1 = y
        return y
    }

    fun reset() {
        x1 = 0.0
        x2 = 0.0
        y1 = 0.0
        y2 = 0.0
    }

    companion object {
        fun highPass(rate: Int, f: Double, q: Double = 0.707): Biquad {
            val w = 2 * PI * f / rate
            val alpha = sin(w) / (2 * q)
            val c = cos(w)
            val a0 = 1 + alpha
            return Biquad((1 + c) / 2 / a0, -(1 + c) / a0, (1 + c) / 2 / a0, -2 * c / a0, (1 - alpha) / a0)
        }

        fun peaking(rate: Int, f: Double, gainDb: Double, q: Double = 1.0): Biquad {
            val a = 10.0.pow(gainDb / 40)
            val w = 2 * PI * f / rate
            val alpha = sin(w) / (2 * q)
            val c = cos(w)
            val a0 = 1 + alpha / a
            return Biquad((1 + alpha * a) / a0, -2 * c / a0, (1 - alpha * a) / a0, -2 * c / a0, (1 - alpha / a) / a0)
        }
    }
}

/**
 * The effect chain on interleaved 16-bit PCM: voice EQ → mono → balance →
 * boost → limiter. The limiter reacts instantly to peaks (no clipping) and
 * recovers over ~150 ms, so a boost raises quiet speech without distortion.
 */
class EffectsDsp(private val rate: Int, private val channels: Int) {
    private val hp = List(channels) { Biquad.highPass(rate, HIGH_PASS_HZ) }
    private val presence = List(channels) { Biquad.peaking(rate, PRESENCE_HZ, PRESENCE_DB) }
    private var limiterGain = 1.0
    private val release = exp(-1.0 / (RELEASE_S * rate))

    fun reset() {
        hp.forEach { it.reset() }
        presence.forEach { it.reset() }
        limiterGain = 1.0
    }

    /** Processes [frames] interleaved frames of [samples] in place. */
    fun process(samples: ShortArray, frames: Int, cfg: EffectsConfig) {
        if (cfg.neutral) {
            limiterGain = 1.0
            return
        }
        val gain = 10.0.pow(cfg.boostDb / 20.0)
        val left = (1.0 - cfg.balance.coerceIn(-1f, 1f)).coerceAtMost(1.0)
        val right = (1.0 + cfg.balance.coerceIn(-1f, 1f)).coerceAtMost(1.0)
        val frame = DoubleArray(channels)
        for (f in 0 until frames) {
            val base = f * channels
            for (c in 0 until channels) {
                var x = samples[base + c] / 32768.0
                if (cfg.voiceEq) x = presence[c].process(hp[c].process(x))
                frame[c] = x
            }
            if (channels == 2) {
                if (cfg.mono) {
                    val m = (frame[0] + frame[1]) / 2
                    frame[0] = m
                    frame[1] = m
                }
                frame[0] *= left
                frame[1] *= right
            }
            var peak = 0.0
            for (c in 0 until channels) {
                frame[c] *= gain
                peak = maxOf(peak, abs(frame[c]))
            }
            // Instant attack, smooth release.
            val needed = if (peak > CEILING) CEILING / peak else 1.0
            limiterGain = if (needed < limiterGain) needed else needed - (needed - limiterGain) * release
            for (c in 0 until channels) {
                samples[base + c] = (frame[c] * limiterGain * 32767.0).roundToInt().coerceIn(-32768, 32767).toShort()
            }
        }
    }

    companion object {
        const val HIGH_PASS_HZ = 100.0
        const val PRESENCE_HZ = 3_000.0
        const val PRESENCE_DB = 5.0
        const val RELEASE_S = 0.15

        /** -1 dBFS. */
        const val CEILING = 0.891

        /** Root mean square of 16-bit samples, 0–1 (for tests). */
        fun rms(s: ShortArray): Double = sqrt(s.sumOf { (it / 32768.0).let { v -> v * v } } / s.size.coerceAtLeast(1))
    }
}

/**
 * The effects as a Media3 audio processor in ExoPlayer's sink, before the
 * speed change. [config] can change at any time; it applies from the next
 * buffer. Formats it can't handle (not 16-bit, more than 2 channels) pass
 * through untouched.
 */
@UnstableApi
class EffectsProcessor : BaseAudioProcessor() {
    @Volatile var config = EffectsConfig()

    private var dsp: EffectsDsp? = null
    private var buffer = ShortArray(0)

    override fun onConfigure(inputAudioFormat: AudioFormat): AudioFormat {
        // Anything else passes through untouched (the processor stays inactive).
        if (inputAudioFormat.encoding != C.ENCODING_PCM_16BIT || inputAudioFormat.channelCount > 2) return AudioFormat.NOT_SET
        dsp = EffectsDsp(inputAudioFormat.sampleRate, inputAudioFormat.channelCount)
        return inputAudioFormat
    }

    override fun queueInput(inputBuffer: ByteBuffer) {
        val bytes = inputBuffer.remaining()
        if (bytes == 0) return
        val out = replaceOutputBuffer(bytes)
        val d = dsp
        val cfg = config
        if (d == null || cfg.neutral) {
            out.put(inputBuffer)
        } else {
            val n = bytes / 2
            if (buffer.size < n) buffer = ShortArray(n)
            inputBuffer.order(ByteOrder.LITTLE_ENDIAN).asShortBuffer().get(buffer, 0, n)
            inputBuffer.position(inputBuffer.position() + bytes)
            d.process(buffer, n / inputAudioFormat.channelCount, cfg)
            out.order(ByteOrder.LITTLE_ENDIAN)
            for (i in 0 until n) out.putShort(buffer[i])
        }
        out.flip()
    }

    override fun onFlush() {
        dsp?.reset()
    }

    override fun onReset() {
        dsp = null
    }
}
