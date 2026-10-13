package si.meld.meldnet

import org.json.JSONObject

/** Identity has already been durably committed by the owner before this flow runs. */
internal object RegistrationFlow {
    class Revoked : Exception()
    fun exchange(profile: JSONObject, publicKey: String,
                 send: (String, String, JSONObject?) -> JSONObject): JSONObject {
        try { return send("/v1/network", profile.getString("credential"), null) }
        catch (e: ControlClient.Refused) {
            if (e.status != 401) throw e
            if (profile.optBoolean("registered")) throw Revoked()
            return send("/v1/register", profile.getJSONObject("enrollment").getString("secret"),
                JSONObject().put("name", profile.getString("name")).put("public_key", publicKey)
                    .put("credential_hash", Protocol.hex(Protocol.hash(profile.getString("credential").toByteArray()))))
        }
    }
}
