// SPDX-License-Identifier: AGPL-3.0-or-later

package org.simpleapb.api

import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Test
import org.simpleapb.api.infrastructure.Serializer
import org.simpleapb.api.model.Error
import org.simpleapb.api.model.ProgressConflict

/** Checks that the generated models decode real server payloads. */
class SerializationTest {
    private val json = Serializer.kotlinxSerializationJson

    @Test
    fun decodesErrorWithFreeFormDetails() {
        val e = json.decodeFromString<Error>(
            """{"code":"rate_limited","message":"slow down","details":{"retry_after":5,"scope":"login"}}""",
        )
        assertEquals("rate_limited", e.code)
        assertEquals(JsonPrimitive(5), e.details?.get("retry_after"))
        assertEquals(JsonPrimitive("login"), e.details?.get("scope"))
    }

    @Test
    fun decodesProgressConflict() {
        val p = """{"book_id":"0190c3b4-0000-7000-8000-000000000001","position_ms":%d,
            "device_id":"0190c3b4-0000-7000-8000-00000000000%d","client_ts":"2026-11-07T20:00:00Z",
            "updated_at":"2026-11-07T20:00:01Z","speed":1.25}"""
        val c = json.decodeFromString<ProgressConflict>(
            """{"code":"progress_conflict","conflict_id":"c1",
               "server":${p.format(15_000_000, 2)},"incoming":${p.format(14_000_000, 3)}}""",
        )
        assertEquals("c1", c.conflictId)
        assertEquals(15_000_000L, c.server.positionMs)
        assertEquals(1.25f, c.incoming.speed)
    }
}
