package si.meld.meldnet

import android.content.*
import android.net.VpnService
import android.os.*
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.net.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/** Explicit opt-in test against an isolated primary, never part of ordinary tests.
 * Enrollment is read from an app-private file, never instrumentation arguments/logs.
 */
class LivePrimaryTest {
    @Test fun encryptedPrimaryAndPrivateDNS() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val fileName = InstrumentationRegistry.getArguments().getString("inviteFile")
        assumeTrue("requires a disposable primary and app-private synthetic invite", fileName == "test-invite")
        val context = instrumentation.targetContext
        val invite = File(context.filesDir, fileName!!)
        assumeTrue("explicit emulator VPN consent must already be granted", VpnService.prepare(context) == null)
        assertTrue("app-private synthetic invite exists", invite.isFile)
        context.startActivity(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        val connected = CountDownLatch(1)
        var owner: Messenger? = null
        val connection = object : ServiceConnection {
            override fun onServiceConnected(name: ComponentName, binder: IBinder) { owner = Messenger(binder); connected.countDown() }
            override fun onServiceDisconnected(name: ComponentName) { owner = null }
        }
        assertTrue(context.bindService(Intent(context, OwnerService::class.java), connection, Context.BIND_AUTO_CREATE))
        assertTrue("native owner binds", connected.await(15, TimeUnit.SECONDS))
        val sequence = AtomicInteger()
        fun call(method: String, arguments: JSONObject = JSONObject()): JSONObject {
            val done = CountDownLatch(1)
            var answer: JSONObject? = null
            val replies = Messenger(object : Handler(Looper.getMainLooper()) {
                override fun handleMessage(message: Message) {
                    answer = JSONObject(message.data.getString("response")!!); done.countDown()
                }
            })
            owner!!.send(Message.obtain().apply {
                arg1 = sequence.incrementAndGet(); replyTo = replies
                data = Bundle().apply { putString("request", JSONObject().put("method", method).put("args", arguments).toString()) }
            })
            assertTrue("$method native response deadline", done.await(60, TimeUnit.SECONDS))
            assertFalse("$method refused: " + answer!!.optString("error"), answer!!.has("error"))
            return answer!!.getJSONObject("snapshot")
        }
        try {
            // Allow the visible activity to reach resumed state before FGS launch.
            instrumentation.waitForIdleSync()
            context.startForegroundService(Intent(context, OwnerService::class.java))
            val arguments = JSONObject().put("id", "test").put("name", "mobile-test")
                .put("key", invite.readText().trim()).put("connect", true).put("autoConnect", false)
            val snapshot = try { call("join", arguments) } finally { arguments.remove("key"); invite.delete() }
            assertEquals("test", snapshot.getString("activeId"))
            assertEquals("up", snapshot.getJSONArray("profiles").getJSONObject(0).getString("state"))
            val connectivity = context.getSystemService(android.net.ConnectivityManager::class.java)
            val routeDeadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
            while (connectivity.getNetworkCapabilities(connectivity.activeNetwork)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) != true && System.nanoTime() < routeDeadline) Thread.sleep(50)
            assertTrue("system selected VPN for app", connectivity.getNetworkCapabilities(connectivity.activeNetwork)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true)
            // Each private response has crossed real WireGuard to the container primary.
            for (transport in listOf("udp", "tcp")) {
                assertEquals("private A via $transport", 0, query("primary.meldnet.internal", transport))
                assertEquals("private NXDOMAIN via $transport", 3, query("unknown.meldnet.internal", transport))
                assertEquals("public DNS preserved via $transport", 0, query("example.org", transport))
            }
            assertEquals("system private resolver", "10.77.0.1", InetAddress.getByName("primary.meldnet.internal").hostAddress)
            assertTrue("system public resolver", InetAddress.getAllByName("example.org").isNotEmpty())
            val afterTraffic = call("snapshot")
            val peers = afterTraffic.getJSONArray("profiles").getJSONObject(0).getJSONArray("peers")
            assertTrue("actual primary handshake evidence", (0 until peers.length()).any { peers.getJSONObject(it).getString("evidence") == "handshake" })
        } finally {
            try { call("disconnect") } finally { context.unbindService(connection) }
        }
    }
    private fun query(name: String, transport: String): Int {
        val bytes = java.io.ByteArrayOutputStream()
        val out = DataOutputStream(bytes)
        out.writeShort(0x4d4e); out.writeShort(0x0100); out.writeShort(1)
        out.writeShort(0); out.writeShort(0); out.writeShort(0)
        name.split('.').forEach { out.writeByte(it.length); out.writeBytes(it) }
        out.writeByte(0); out.writeShort(1); out.writeShort(1)
        val request = bytes.toByteArray()
        val reply = if (transport == "udp") {
            DatagramSocket().use { socket ->
                // Like an OS resolver, retransmit UDP while a newly selected VPN's
                // routes become effective. A lost datagram is not a DNS response.
                socket.soTimeout = 3000
                val packet = DatagramPacket(ByteArray(4096), 4096)
                var received = false
                for (attempt in 0 until 4) {
                    socket.send(DatagramPacket(request, request.size, InetAddress.getByName("198.18.0.53"), 53))
                    try { socket.receive(packet); received = true; break }
                    catch (e: SocketTimeoutException) { if (attempt == 3) throw e }
                }
                assertTrue("DNS UDP reply after bounded retries", received)
                packet.data.copyOf(packet.length)
            }
        } else {
            Socket().use { socket ->
                socket.soTimeout = 8000; socket.connect(InetSocketAddress("198.18.0.53", 53), 8000)
                val stream = DataOutputStream(socket.getOutputStream()); stream.writeShort(request.size); stream.write(request)
                val input = DataInputStream(socket.getInputStream()); val size = input.readUnsignedShort()
                assertTrue("bounded DNS TCP reply", size in 12..65535)
                ByteArray(size).also { input.readFully(it) }
            }
        }
        assertTrue("valid DNS response", reply.size >= 12 && reply[0] == 0x4d.toByte() && reply[1] == 0x4e.toByte())
        return reply[3].toInt() and 15
    }
}
