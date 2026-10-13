package si.meld.meldnet

import android.net.Network
import org.json.JSONObject
import org.json.JSONArray
import java.net.URI

/** Native owner-only transport. Go TLS supports the primary's Ed25519 leaf on
 * Android versions whose system TLS provider does not negotiate that signature.
 * Credentials never cross the Flutter bridge. */
internal class ControlClient(private val network: Network) {
    class Refused(val status: Int) : Exception()
    fun request(profile: JSONObject, path: String, bearer: String, body: JSONObject? = null): JSONObject {
        val enrollment = profile.getJSONObject("enrollment")
        val origin = enrollment.getString("url")
        Protocol.origin(origin)
        val addresses = UnderlayResolver.resolve(network, URI(origin).host).mapNotNull { it.hostAddress }
        val input = JSONObject().put("origin", origin).put("path", path)
            .put("pin", enrollment.getString("pin")).put("bearer", bearer)
            .put("addresses", JSONArray(addresses)).put("networkHandle", network.networkHandle)
        if (body != null) input.put("body", body)
        val response = JSONObject(NativeDns.request(input.toString()))
        val status = response.getInt("status")
        if (status != 200) throw Refused(status)
        return response.getJSONObject("body")
    }
}
