package si.meld.meldnet

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Only constructed in :vpn; no backups or plaintext private state. */
internal class Vault(context: Context, private val alias: String = "meldnet.owner.v1") {
    private val file = AtomicFile(File(context.noBackupFilesDir, "memberships.v1.enc"))
    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
            generateKey()
        }
    }
    fun read(): JSONObject {
        if (!file.baseFile.exists()) return JSONObject().put("version", 1).put("profiles", JSONObject())
        val bytes = file.readFully(); require(bytes.size in 29..(2 * 1024 * 1024))
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, bytes.copyOfRange(0, 12)))
        c.updateAAD("meldnet.owner.v1".toByteArray())
        val clear = c.doFinal(bytes.copyOfRange(12, bytes.size))
        return try { JSONObject(String(clear, Charsets.UTF_8)).also { require(it.getInt("version") == 1) } } finally { clear.fill(0) }
    }
    fun write(value: JSONObject) {
        val c = Cipher.getInstance("AES/GCM/NoPadding"); c.init(Cipher.ENCRYPT_MODE, key())
        c.updateAAD("meldnet.owner.v1".toByteArray())
        val clear = value.toString().toByteArray(); val bytes = try { c.iv + c.doFinal(clear) } finally { clear.fill(0) }
        val out = file.startWrite()
        try { out.write(bytes); file.finishWrite(out) } catch (e: Exception) { file.failWrite(out); throw e }
    }
}
