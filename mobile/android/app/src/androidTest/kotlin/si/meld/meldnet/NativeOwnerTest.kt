package si.meld.meldnet

import android.content.ContextWrapper
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Test
import org.junit.Assert.*
import com.wireguard.android.backend.GoBackend
import org.json.JSONObject
import java.io.File
import java.security.KeyStore

/** Device tests exercise real JNI and Keystore without establishing a VPN. */
class NativeOwnerTest {
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    @Test fun testDNSAwareNativeLibraryLoadsAndRejectsInvalidPolicy() {
        val backend = GoBackend(instrumentation.targetContext)
        assertEquals("meldnet-dns-v1", backend.version)
        assertEquals(-1, NativeDns.configure("{}"))
        assertEquals(0, NativeDns.configure("""{"primary":"10.77.0.1","pool":"10.77.0.0/24","upstreams":["192.0.2.53"],"networkHandle":1}"""))
        // Leave global native policy empty. No socket or TUN was opened.
        assertEquals(-1, NativeDns.configure("{}"))
    }
    @Test fun testKeystoreVaultIsEncryptedDurableAndRejectsTampering() {
        val target = instrumentation.targetContext
        val directory = File(target.cacheDir, "native-test-${System.nanoTime()}").apply { mkdirs() }
        val context = object : ContextWrapper(target) { override fun getNoBackupFilesDir() = directory }
        val alias = "meldnet.instrumentation.${System.nanoTime()}"
        try {
            val source = JSONObject().put("version", 1).put("profiles", JSONObject().put("synthetic", "private-fixture-credential"))
            Vault(context, alias).write(source)
            assertEquals(source.toString(), Vault(context, alias).read().toString())
            val encrypted = File(directory, "memberships.v1.enc")
            assertFalse(String(encrypted.readBytes()).contains("private-fixture-credential"))
            val bytes = encrypted.readBytes(); bytes[bytes.lastIndex] = (bytes.last().toInt() xor 1).toByte(); encrypted.writeBytes(bytes)
            assertTrue(runCatching { Vault(context, alias).read() }.isFailure)
        } finally {
            directory.deleteRecursively()
            KeyStore.getInstance("AndroidKeyStore").apply { load(null); deleteEntry(alias) }
        }
    }

    @Test fun testOwnerSnapshotUsesPrivateBinderWithoutFlutterEngine() {
        val target = instrumentation.targetContext
        val done = java.util.concurrent.CountDownLatch(1)
        var response: JSONObject? = null
        val replies = android.os.Messenger(object : android.os.Handler(android.os.Looper.getMainLooper()) {
            override fun handleMessage(message: android.os.Message) {
                response = JSONObject(message.data.getString("response")!!); done.countDown()
            }
        })
        val connection = object : android.content.ServiceConnection {
            override fun onServiceConnected(name: android.content.ComponentName, binder: android.os.IBinder) {
                android.os.Messenger(binder).send(android.os.Message.obtain().apply {
                    arg1 = 1; replyTo = replies
                    data = android.os.Bundle().apply { putString("request", "{\"method\":\"snapshot\"}") }
                })
            }
            override fun onServiceDisconnected(name: android.content.ComponentName) { }
        }
        assertTrue(target.bindService(android.content.Intent(target, OwnerService::class.java), connection, android.content.Context.BIND_AUTO_CREATE))
        try {
            assertTrue("native owner response", done.await(15, java.util.concurrent.TimeUnit.SECONDS))
            assertFalse(response!!.has("error"))
            val snapshot = response!!.getJSONObject("snapshot")
            assertEquals("android", snapshot.getString("platform"))
            assertEquals(1, snapshot.getInt("version"))
            assertFalse(snapshot.getJSONObject("capabilities").getBoolean("autoConnect"))
            assertFalse(snapshot.toString().contains("private_key")); assertFalse(snapshot.toString().contains("credential"))
        } finally { target.unbindService(connection) }
    }
}
