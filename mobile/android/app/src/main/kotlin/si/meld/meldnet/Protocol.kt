package si.meld.meldnet

import org.json.JSONObject
import java.net.URI
import java.security.MessageDigest
import java.util.Base64

/** All remote and UI input is checked again inside the private VPN owner. */
internal object Protocol {
    val name = Regex("^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,31}$")
    val id = Regex("^[a-z]([a-z0-9-]{0,29}[a-z0-9])?$")
    fun command(method: String, args: JSONObject) {
        val strings = when (method) { "join" -> setOf("id", "name", "key"); "connect", "setAutoConnect" -> setOf("id"); "snapshot", "disconnect" -> emptySet(); else -> throw IllegalArgumentException() }
        val booleans = when (method) { "join" -> setOf("connect", "autoConnect"); "setAutoConnect" -> setOf("enabled"); else -> emptySet() }
        require(args.keys().asSequence().toSet() == strings + booleans)
        strings.forEach { require(args.get(it) is String) }
        booleans.forEach { require(args.get(it) is Boolean) }
    }
    fun requireName(value: String) { require(name.matches(value)) }
    fun hash(value: ByteArray) = MessageDigest.getInstance("SHA-256").digest(value)
    fun hex(value: ByteArray) = value.joinToString("") { "%02x".format(it) }
    fun secret(value: String): Boolean = runCatching {
        value.length == 43 && Base64.getUrlDecoder().decode(value).size == 32
    }.getOrDefault(false)
    fun origin(value: String) {
        val u = URI(value)
        require(u.scheme == "https" && !u.host.isNullOrEmpty() && u.rawUserInfo == null &&
            u.rawQuery == null && u.rawFragment == null && u.rawPath.isNullOrEmpty() &&
            (u.port == -1 || u.port in 1..65535))
    }
    fun enrollment(value: String): JSONObject {
        require(value.length in 10..8192 && value.startsWith("meldnet1."))
        val e = JSONObject(String(Base64.getUrlDecoder().decode(value.substring(9)), Charsets.UTF_8))
        origin(e.getString("url"))
        require(Regex("^[0-9a-fA-F]{64}$").matches(e.getString("pin")))
        require(secret(e.getString("secret")))
        return e
    }
    fun ipv4(value: String): Long {
        val parts = value.split('.')
        require(parts.size == 4)
        return parts.fold(0L) { address, part ->
            require(part.matches(Regex("^(0|[1-9][0-9]{0,2})$")))
            val n = part.toInt(); require(n in 0..255)
            (address shl 8) or n.toLong()
        }
    }
    fun registration(r: JSONObject, expectedName: String) {
        require(r.getString("name") == expectedName)
        val prefix = r.getString("pool").split('/')
        require(prefix.size == 2)
        val base = ipv4(prefix[0]); val bits = prefix[1].toInt(); require(bits in 16..28)
        val mask = (0xffffffffL shl (32 - bits)) and 0xffffffffL
        require(base and mask == base)
        require(base ushr 24 == 10L || base ushr 20 == 0xac1L || base ushr 16 == 0xc0a8L)
        val address = ipv4(r.getString("ip")); require(address and mask == base)
        require(address > base + 1 && address < base + (1L shl (32 - bits)) - 1)
        val primary = r.getJSONObject("primary"); requireName(primary.getString("name"))
        require(primary.getString("name") != expectedName)
        require(ipv4(primary.getString("ip")) == base + 1)
        require(Base64.getDecoder().decode(primary.getString("public_key")).size == 32)
        val endpoint = r.getString("endpoint")
        val u = URI("udp://$endpoint")
        require(!u.host.isNullOrEmpty() && u.port in 1..65535 && u.rawUserInfo == null &&
            u.rawQuery == null && u.rawFragment == null && u.rawPath.isNullOrEmpty())
        // Endpoint must stay outside the only routed private pool.
        runCatching { ipv4(u.host) }.getOrNull()?.let { require(it and mask != base) }
        r.optJSONObject("dns")?.let {
            require(it.getString("domain") == "meldnet.internal")
            require(it.getString("server") == primary.getString("ip"))
        }
        val members = r.getJSONArray("members"); require(members.length() <= 129)
        val seenNames = mutableSetOf<String>(); val seenIPs = mutableSetOf<String>(); val seenKeys = mutableSetOf<String>()
        for (i in 0 until members.length()) {
            val m = members.getJSONObject(i); val n = m.getString("name"); requireName(n)
            require(seenNames.add(n) && seenIPs.add(m.getString("ip")) && seenKeys.add(m.getString("public_key")))
            val ip = ipv4(m.getString("ip")); require(ip and mask == base && ip > base && ip < base + (1L shl (32 - bits)) - 1)
            require(Base64.getDecoder().decode(m.getString("public_key")).size == 32)
            val hostname = m.optString("hostname")
            require(hostname.isEmpty() || Regex("^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\\.meldnet\\.internal$").matches(hostname))
        }
    }
}
