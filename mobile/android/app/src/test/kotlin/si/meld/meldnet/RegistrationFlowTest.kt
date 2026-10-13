package si.meld.meldnet

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class RegistrationFlowTest {
    private fun pending() = JSONObject().put("name", "phone").put("registered", false)
        .put("private", "synthetic-private-never-on-wire").put("credential", "synthetic-durable-credential")
        .put("enrollment", JSONObject().put("secret", "synthetic-invite"))
    @Test fun recoversLostRegistrationResponseUsingSavedCredentialFirst() {
        var calls = 0
        val response = JSONObject().put("name", "phone")
        assertSame(response, RegistrationFlow.exchange(pending(), "synthetic-public") { path, bearer, body ->
            calls++; assertEquals("/v1/network", path); assertEquals("synthetic-durable-credential", bearer); assertNull(body)
            response
        })
        assertEquals(1, calls)
    }
    @Test fun registersOnlyAfterExplicitUnauthorizedAndNeverSendsPrivateKeyOrDeviceCredential() {
        val paths = mutableListOf<String>()
        RegistrationFlow.exchange(pending(), "synthetic-public") { path, bearer, body ->
            paths.add(path)
            if (path == "/v1/network") throw ControlClient.Refused(401)
            assertEquals("synthetic-invite", bearer)
            assertEquals(setOf("name", "public_key", "credential_hash"), body!!.keys().asSequence().toSet())
            assertFalse(body.toString().contains("synthetic-private")); assertFalse(body.toString().contains("synthetic-durable"))
            assertEquals(64, body.getString("credential_hash").length)
            JSONObject()
        }
        assertEquals(listOf("/v1/network", "/v1/register"), paths)
    }
    @Test fun knownRegistrationUnauthorizedIsRevocationNeverNewEnrollment() {
        var calls = 0
        val result = runCatching { RegistrationFlow.exchange(pending().put("registered", true), "public") { _, _, _ ->
            calls++; throw ControlClient.Refused(401)
        } }
        assertTrue(result.exceptionOrNull() is RegistrationFlow.Revoked); assertEquals(1, calls)
    }
    @Test fun transportFailureDoesNotConsumeInvite() {
        var calls = 0
        val result = runCatching { RegistrationFlow.exchange(pending(), "public") { _, _, _ -> calls++; throw java.io.IOException() } }
        assertTrue(result.isFailure); assertEquals(1, calls)
    }
}
