package si.meld.meldnet

import android.app.*
import android.content.Intent
import android.net.*
import android.os.*
import com.wireguard.android.backend.GoBackend
import com.wireguard.android.backend.Tunnel
import com.wireguard.config.Config
import com.wireguard.crypto.KeyPair
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.security.SecureRandom
import java.time.Instant
import java.util.Base64
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** Private Binder owner in :vpn. It never loads Flutter and remains started while a tunnel is active. */
class OwnerService : Service() {
    private val queue = Executors.newSingleThreadScheduledExecutor()
    private lateinit var vault: Vault
    private lateinit var state: JSONObject
    private lateinit var backend: GoBackend
    private var loadFailed = false
    private var active: String? = null
    private var tunnel: Tunnel? = null
    private val issues = mutableMapOf<String, String>()
    private val revoked = mutableSetOf<String>()
    private var retrySeconds = 15L
    private var lastConfig: String? = null
    private var lastNetworkHandle = 0L
    private var vpnNetwork: Network? = null
    private lateinit var connectivity: ConnectivityManager
    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { queue.execute { nextSync = 0; reconcile() } }
        override fun onLost(network: Network) { queue.execute { nextSync = 0; reconcile() } }
        override fun onLinkPropertiesChanged(network: Network, properties: LinkProperties) { queue.execute { nextSync = 0; reconcile() } }
    }
    private val incoming = Messenger(object : Handler(Looper.getMainLooper()) {
        override fun handleMessage(msg: Message) {
            if (msg.sendingUid != applicationInfo.uid) return
            val reply = msg.replyTo ?: return
            val request = msg.data.getString("request") ?: return
            val sequence = msg.arg1
            queue.execute {
                val response = JSONObject()
                try {
                    check(!loadFailed)
                    val command = JSONObject(request); mutate(command.getString("method"), command.optJSONObject("args") ?: JSONObject())
                    response.put("snapshot", snapshot())
                } catch (_: Throwable) {
                    response.put("error", if (loadFailed) "Saved membership could not be opened. Preserve app data for recovery." else "The operation could not be completed. Check membership status and try again.")
                    if (!loadFailed) { idle(); response.put("snapshot", snapshot()) }
                }
                try { reply.send(Message.obtain().apply { arg1 = sequence; data = Bundle().apply { putString("response", response.toString()) } }) } catch (_: RemoteException) { }
            }
        }
    })
    override fun onCreate() {
        super.onCreate()
        connectivity = getSystemService(ConnectivityManager::class.java)
        queue.execute {
            try { vault = Vault(this); state = vault.read(); backend = GoBackend(this); validateSaved() }
            catch (_: Throwable) { loadFailed = true }
        }
        connectivity.registerNetworkCallback(NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build(), callback)
        queue.scheduleWithFixedDelay({ reconcile() }, 15, 15, TimeUnit.SECONDS)
    }
    override fun onBind(intent: Intent): IBinder = incoming.binder
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel("vpn", "Meldnet VPN", NotificationManager.IMPORTANCE_LOW))
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        startForeground(71, Notification.Builder(this, "vpn").setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentTitle("Meldnet").setContentText("VPN owner is managing your membership").setContentIntent(open).setOngoing(true).build())
        return START_NOT_STICKY
    }
    private fun profiles() = state.getJSONObject("profiles")
    private fun validateSaved() {
        require(profiles().length() <= 16)
        profiles().keys().forEach { id ->
            require(Protocol.id.matches(id)); val p = profiles().getJSONObject(id)
            Protocol.requireName(p.getString("name")); Protocol.origin(p.getJSONObject("enrollment").getString("url"))
            require(Protocol.secret(p.getString("credential")))
            require(Regex("^[0-9a-fA-F]{64}$").matches(p.getJSONObject("enrollment").getString("pin")))
            if (!p.optBoolean("registered")) require(Protocol.secret(p.getJSONObject("enrollment").getString("secret")))
            com.wireguard.crypto.Key.fromBase64(p.getString("private"))
            p.optJSONObject("network")?.let { Protocol.registration(it, p.getString("name")) }
        }
    }
    private fun mutate(method: String, args: JSONObject) {
        Protocol.command(method, args)
        when (method) {
            "snapshot" -> Unit
            "join" -> {
                val id = args.getString("id"); require(Protocol.id.matches(id))
                val name = args.getString("name"); Protocol.requireName(name)
                require(!args.getBoolean("autoConnect"))
                val enrollment = Protocol.enrollment(args.getString("key"))
                var profile = profiles().optJSONObject(id)
                if (profile == null) {
                    require(profiles().length() < 16)
                    val key = KeyPair()
                    val credential = ByteArray(32).also { SecureRandom().nextBytes(it) }
                    profile = JSONObject().put("name", name).put("private", key.privateKey.toBase64())
                        .put("credential", Base64.getUrlEncoder().withoutPadding().encodeToString(credential))
                        .put("enrollment", enrollment).put("registered", false)
                    profiles().put(id, profile)
                    // Durable identity and credential must precede the first registration request.
                    try { vault.write(state) } catch (e: Exception) { profiles().remove(id); throw e }
                } else {
                    require(profile.getString("name") == name)
                    val old = profile.getJSONObject("enrollment")
                    require(old.getString("url") == enrollment.getString("url") && old.getString("pin").equals(enrollment.getString("pin"), true))
                    if (!profile.optBoolean("registered")) { profile.put("enrollment", enrollment); vault.write(state) }
                }
                sync(id)
                if (args.getBoolean("connect")) connect(id)
                else idle()
            }
            "connect" -> connect(args.getString("id"))
            "disconnect" -> { disconnect(); idle() }
            "setAutoConnect" -> { require(profiles().has(args.getString("id"))); require(!args.getBoolean("enabled")) }
            else -> throw IllegalArgumentException()
        }
    }
    private fun underlay(): Network = (listOfNotNull(connectivity.activeNetwork) + connectivity.allNetworks.sortedByDescending {
        connectivity.getNetworkCapabilities(it)?.hasCapability(NetworkCapabilities.NET_CAPABILITY_FOREGROUND) == true
    }).distinct().firstOrNull {
        connectivity.getNetworkCapabilities(it)?.let { c -> c.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) && c.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) } == true
    } ?: throw IllegalStateException()
    private fun sync(id: String) {
        val p = profiles().getJSONObject(id)
        try {
            val client = ControlClient(underlay())
            val publicKey = KeyPair(com.wireguard.crypto.Key.fromBase64(p.getString("private"))).publicKey.toBase64()
            val r = try { RegistrationFlow.exchange(p, publicKey) { path, bearer, body -> client.request(p, path, bearer, body) } }
            catch (e: RegistrationFlow.Revoked) {
                revoked.add(id)
                try { if (active == id) { disconnect(); idle() } }
                catch (cleanup: Throwable) { issues[id] = "Authorization revoked; VPN cleanup failed. Disconnect again."; throw cleanup }
                issues[id] = "Authorization revoked; tunnel stopped."
                throw e
            }
            Protocol.registration(r, p.getString("name"))
            val next = JSONObject(state.toString()); val saved = next.getJSONObject("profiles").getJSONObject(id)
            saved.put("network", r).put("registered", true).put("syncedAt", System.currentTimeMillis())
            saved.getJSONObject("enrollment").put("secret", "")
            vault.write(next); state = next; issues.remove(id); revoked.remove(id)
        } catch (e: Exception) {
            if (!revoked.contains(id)) issues[id] = "Primary unavailable or returned an invalid response. Saved identity is preserved."
            throw e
        }
    }
    private fun ownVPN(r: JSONObject): Network? {
        val address = r.getString("ip")
        return connectivity.allNetworks.filter { candidate ->
            val caps = connectivity.getNetworkCapabilities(candidate)
            val properties = connectivity.getLinkProperties(candidate)
            caps?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true &&
                caps.ownerUid == applicationInfo.uid &&
                properties?.linkAddresses?.any { it.address.hostAddress == address && it.prefixLength == 32 } == true &&
                properties.dnsServers.any { it.hostAddress == "198.18.0.53" }
        }.maxByOrNull { it.networkHandle }
    }
    private fun bindPrivateDNS(r: JSONObject, underlay: Network) {
        // GoBackend establishes before ConnectivityService publishes the VPN Network.
        // Until publication, native policy has vpnHandle=0 and private DNS fails closed.
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        var found: Network? = null
        while (System.nanoTime() < deadline) {
            found = ownVPN(r)
            if (found != null) break
            Thread.sleep(50)
        }
        check(found != null)
        prepareDNS(r, underlay, found)
        vpnNetwork = found
    }
    private fun prepareDNS(r: JSONObject, network: Network, vpn: Network? = vpnNetwork) {
        check(backend.version == "meldnet-dns-v1")
        val dns = r.optJSONObject("dns") ?: throw IllegalStateException("Private DNS policy is required")
        val properties = connectivity.getLinkProperties(network) ?: throw IllegalStateException()
        // Strict system Private DNS can bypass per-VPN plaintext DNS policy. Fail closed.
        require(!properties.isPrivateDnsActive || properties.privateDnsServerName.isNullOrEmpty())
        val upstreams = properties.dnsServers.mapNotNull { it.hostAddress }
        require(upstreams.isNotEmpty())
        val policy = JSONObject().put("primary", dns.getString("server")).put("pool", r.getString("pool"))
            .put("upstreams", JSONArray(upstreams)).put("networkHandle", network.networkHandle)
            .put("vpnHandle", vpn?.networkHandle ?: 0L)
        check(NativeDns.configure(policy.toString()) == 0)
    }
    private fun config(p: JSONObject, network: Network): Config {
        val r = p.getJSONObject("network"); Protocol.registration(r, p.getString("name"))
        prepareDNS(r, network, null)
        vpnNetwork = null
        val endpoint = java.net.URI("udp://" + r.getString("endpoint"))
        val addresses = UnderlayResolver.resolve(network, endpoint.host)
        val address = addresses.firstOrNull() ?: throw IllegalStateException()
        val host = address.hostAddress ?: throw IllegalStateException()
        runCatching { Protocol.ipv4(host) }.getOrNull()?.let { n ->
            val pool = r.getString("pool").split('/'); val mask = (0xffffffffL shl (32 - pool[1].toInt())) and 0xffffffffL
            require(n and mask != Protocol.ipv4(pool[0]))
        }
        val formatted = if (host.contains(':')) "[$host]" else host
        val raw = "[Interface]\nPrivateKey = ${p.getString("private")}\nAddress = ${r.getString("ip")}/32\nDNS = 198.18.0.53\nMTU = 1280\n[Peer]\nPublicKey = ${r.getJSONObject("primary").getString("public_key")}\nEndpoint = $formatted:${endpoint.port}\nAllowedIPs = ${r.getString("pool")}, 198.18.0.53/32\nPersistentKeepalive = 25\n"
        return Config.parse(ByteArrayInputStream(raw.toByteArray()))
    }
    private fun configIdentity(r: JSONObject) = listOf(r.getString("ip"), r.getString("pool"), r.getString("endpoint"), r.getJSONObject("primary").getString("public_key"), r.optJSONObject("dns")?.toString()).joinToString("|")
    private fun connect(id: String) {
        require(profiles().has(id) && (active == null || active == id))
        if (active == id && tunnel?.let { backend.getState(it) == Tunnel.State.UP } == true) return
        check(android.net.VpnService.prepare(this) == null)
        sync(id)
        val network = underlay()
        val p = profiles().getJSONObject(id)
        try {
            val configuration = config(p, network)
            val instance = object : Tunnel {
                override fun getName() = id
                override fun onStateChange(newState: Tunnel.State) {
                    if (newState == Tunnel.State.DOWN) queue.execute {
                        if (tunnel === this && backend.getState(this) == Tunnel.State.DOWN) { active = null; tunnel = null; vpnNetwork = null; NativeDns.configure("{}"); lastConfig = null; idle() }
                    }
                }
            }
            backend.setState(instance, Tunnel.State.UP, configuration)
            tunnel = instance; active = id; lastConfig = configIdentity(p.getJSONObject("network")); lastNetworkHandle = network.networkHandle
            bindPrivateDNS(p.getJSONObject("network"), network)
            issues.remove(id)
        } catch (e: Throwable) {
            if (active == id) {
                try { disconnect() } catch (cleanup: Throwable) { issues[id] = "VPN start failed and cleanup failed. Disconnect again."; throw cleanup }
            }
            issues[id] = "VPN could not start with protected private DNS. No connection is claimed."; idle(); throw e
        }
    }
    private fun disconnect() {
        tunnel?.let { backend.setState(it, Tunnel.State.DOWN, null) }
        active = null; tunnel = null; vpnNetwork = null; lastConfig = null
        NativeDns.configure("{}")
    }
    private fun idle() { if (active == null) { stopForeground(STOP_FOREGROUND_REMOVE); stopSelf() } }
    private var nextSync = 0L
    private fun reconcile() {
        if (loadFailed || !::state.isInitialized || active == null) return
        val id = active ?: return
        // Local DNS protection must be checked even while the remote primary is offline
        // or control reconciliation is backing off. A dead underlay cannot carry DNS.
        val network = try { underlay() } catch (_: Exception) { issues[id] = "Waiting for an underlying network."; return }
        try {
            val saved = profiles().getJSONObject(id)
            vpnNetwork = ownVPN(saved.getJSONObject("network"))
            check(vpnNetwork != null)
            prepareDNS(saved.getJSONObject("network"), network)
            if (network.networkHandle != lastNetworkHandle) {
                backend.setState(tunnel!!, Tunnel.State.UP, config(saved, network))
                bindPrivateDNS(saved.getJSONObject("network"), network)
                lastNetworkHandle = network.networkHandle
            }
        } catch (_: Throwable) {
            try { disconnect(); idle() } catch (_: Throwable) { issues[id] = "VPN cleanup failed. Disconnect again before continuing."; return }
            issues[id] = "Private DNS protection is unavailable; tunnel stopped. Check system Private DNS and underlay connectivity."
            return
        }
        if (System.currentTimeMillis() < nextSync) return
        try {
            sync(id)
            if (active == null) return
            val p = profiles().getJSONObject(id)
            try { prepareDNS(p.getJSONObject("network"), network) }
            catch (e: Throwable) {
                disconnect(); idle(); issues[id] = "Private DNS protection is unavailable; tunnel stopped."
                throw e
            }
            if (lastConfig != configIdentity(p.getJSONObject("network"))) {
                backend.setState(tunnel!!, Tunnel.State.UP, config(p, network))
                bindPrivateDNS(p.getJSONObject("network"), network)
                lastConfig = configIdentity(p.getJSONObject("network"))
            }
            retrySeconds = 15
        } catch (_: Throwable) {
            // A failed tunnel replacement must not leave a live TUN with an unbound
            // private DNS policy. Ordinary primary outages retain a valid bound TUN.
            if (active != null && vpnNetwork == null) {
                try { disconnect(); idle(); issues[id] = "VPN reconfiguration failed; tunnel stopped." }
                catch (_: Throwable) { issues[id] = "VPN reconfiguration and cleanup failed. Disconnect again." }
            }
            retrySeconds = (retrySeconds * 2).coerceAtMost(300)
        }
        nextSync = System.currentTimeMillis() + retrySeconds * 1000
    }
    private fun snapshot(): JSONObject {
        val output = JSONArray(); val now = System.currentTimeMillis()
        profiles().keys().forEach { id ->
            val p = profiles().getJSONObject(id); val r = p.optJSONObject("network")
            val up = active == id && tunnel?.let { backend.getState(it) == Tunnel.State.UP } == true
            val peers = JSONArray()
            val handshake = if (up) runCatching {
                backend.getStatistics(tunnel!!).peer(com.wireguard.crypto.Key.fromBase64(r!!.getJSONObject("primary").getString("public_key")))?.latestHandshakeEpochMillis() ?: 0L
            }.getOrDefault(0L) else 0L
            r?.optJSONArray("members")?.let { members -> for (i in 0 until members.length()) {
                val m = members.getJSONObject(i)
                val seen = runCatching { Instant.parse(m.optString("last_seen")).toEpochMilli() }.getOrDefault(0)
                val evidence = when {
                    m.getString("name") == p.getString("name") -> "self"
                    !up -> "disconnected"
                    m.getString("public_key") == r.getJSONObject("primary").getString("public_key") && now - handshake in 0..180000 -> "handshake"
                    now - p.optLong("syncedAt") !in 0..90000 -> "stale"
                    now - seen in 0..90000 -> "control"
                    else -> "stale"
                }
                peers.put(JSONObject().put("name", m.getString("name")).put("hostname", m.optString("hostname"))
                    .put("address", m.getString("ip")).put("evidence", evidence))
            } }
            output.put(JSONObject().put("id", id).put("name", p.getString("name"))
                .put("state", if (up) "up" else if (revoked.contains(id)) "revoked" else if (issues.containsKey(id)) "error" else "disconnected")
                .put("autoConnect", false).put("pending", !p.optBoolean("registered"))
                .put("address", r?.optString("ip") ?: JSONObject.NULL).put("error", issues[id] ?: JSONObject.NULL)
                .put("dnsState", if (up && vpnNetwork != null) "ready" else if (up) "unavailable" else "inactive").put("peers", peers))
        }
        return JSONObject().put("version", 1).put("platform", "android")
            .put("capabilities", JSONObject().put("join", true).put("connect", true).put("autoConnect", false).put("multipleActive", false))
            .put("activeId", active ?: JSONObject.NULL).put("warning", JSONObject.NULL)
            .put("profiles", output)
    }
    override fun onDestroy() {
        connectivity.unregisterNetworkCallback(callback)
        // A started active service is not destroyed merely by unbinding the Flutter UI.
        queue.execute { runCatching { disconnect() }; queue.shutdown() }
        super.onDestroy()
    }
}
