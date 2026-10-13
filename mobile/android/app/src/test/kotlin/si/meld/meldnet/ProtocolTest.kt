package si.meld.meldnet

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import java.util.Base64

class ProtocolTest {
    private fun invite(origin: String = "https://primary.example:8443") = "meldnet1." + Base64.getUrlEncoder().withoutPadding().encodeToString(JSONObject()
        .put("url", origin).put("pin", "a".repeat(64)).put("secret", Base64.getUrlEncoder().withoutPadding().encodeToString(ByteArray(32) { 7 })).toString().toByteArray())
    @Test fun acceptsProtocolInvite() { assertEquals("https://primary.example:8443", Protocol.enrollment(invite()).getString("url")) }
    @Test fun rejectsOriginSmuggling() {
        listOf("http://primary.example", "https://user:pass@primary.example", "https://primary.example/", "https://primary.example?key=x", "https://primary.example#x").forEach {
            assertTrue(runCatching { Protocol.enrollment(invite(it)) }.isFailure)
        }
    }
    @Test fun rejectsSecretsAndNamesOutsideProtocol() {
        assertFalse(Protocol.secret("test")); assertFalse(Protocol.name.matches("phone\nPrivateKey=x"))
        assertFalse(Protocol.id.matches("../state")); assertTrue(runCatching { Protocol.enrollment("meldnet1." + "a".repeat(9000)) }.isFailure)
    }
    @Test fun rejectsNonCanonicalIPv4() {
        listOf("127.1", "010.0.0.1", "256.0.0.1", "1.2.3.4 ", "localhost").forEach { assertTrue(runCatching { Protocol.ipv4(it) }.isFailure) }
        assertEquals(0x0a4d0002L, Protocol.ipv4("10.77.0.2"))
    }

    private fun registration(): JSONObject {
        val primary = JSONObject().put("name", "primary").put("ip", "10.77.0.1").put("public_key", Base64.getEncoder().encodeToString(ByteArray(32) { 2 }))
        return JSONObject().put("name", "phone").put("ip", "10.77.0.2").put("pool", "10.77.0.0/24")
            .put("primary", primary).put("endpoint", "primary.example:51820")
            .put("dns", JSONObject().put("domain", "meldnet.internal").put("server", "10.77.0.1"))
            .put("members", org.json.JSONArray().put(primary))
    }
    @Test fun acceptsCanonicalAssignment() { Protocol.registration(registration(), "phone") }
    @Test fun rejectsUnsafeAssignments() {
        listOf("pool" to "0.0.0.0/0", "pool" to "10.77.0.1/24", "pool" to "8.8.0.0/16",
            "ip" to "10.77.0.1", "ip" to "10.78.0.2", "endpoint" to "10.77.0.9:51820",
            "endpoint" to "host:51820/path", "name" to "other").forEach { (key, value) ->
            assertTrue("rejected $key", runCatching { Protocol.registration(registration().put(key, value), "phone") }.isFailure)
        }
    }
    @Test fun rejectsWrongDNSAuthorityAndZone() {
        listOf("server" to "8.8.8.8", "domain" to "example.com").forEach { (key, value) ->
            val r = registration(); r.getJSONObject("dns").put(key, value)
            assertTrue(runCatching { Protocol.registration(r, "phone") }.isFailure)
        }
    }
    @Test fun rejectsUntypedNativeMutation() {
        assertTrue(runCatching { Protocol.command("setAutoConnect", JSONObject().put("id", "work").put("enabled", "false")) }.isFailure)
        assertTrue(runCatching { Protocol.command("disconnect", JSONObject().put("shell", "bad")) }.isFailure)
        Protocol.command("setAutoConnect", JSONObject().put("id", "work").put("enabled", false))
    }
    @Test fun profileIDsMatchDesktopBoundaries() {
        assertTrue(Protocol.id.matches("a")); assertTrue(Protocol.id.matches("a".repeat(31)))
        assertFalse(Protocol.id.matches("a".repeat(32))); assertFalse(Protocol.id.matches("a_b"))
        assertFalse(Protocol.id.matches("a-")); assertFalse(Protocol.id.matches("-a"))
        assertTrue(Protocol.id.matches("a-b"))
    }
}
