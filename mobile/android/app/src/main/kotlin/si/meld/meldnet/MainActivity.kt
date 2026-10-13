package si.meld.meldnet

import android.app.Activity
import android.content.*
import android.net.VpnService
import android.os.*
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import org.json.JSONArray
import org.json.JSONObject

/** Presentation-only Binder bridge: no private files or key-bearing responses. */
class MainActivity : FlutterActivity() {
    private var owner: Messenger? = null
    private var bound = false
    private var sequence = 0
    private val pending = mutableMapOf<Int, MethodChannel.Result>()
    private val waiting = mutableListOf<Pair<MethodCall, MethodChannel.Result>>()
    private var consent: Pair<MethodCall, MethodChannel.Result>? = null
    private val handler = Handler(Looper.getMainLooper())
    private val replies = Messenger(object : Handler(Looper.getMainLooper()) {
        override fun handleMessage(msg: Message) {
            val result = pending.remove(msg.arg1) ?: return
            try {
                val response = JSONObject(msg.data.getString("response") ?: "")
                if (response.has("error")) result.error("owner_error", response.getString("error"), null)
                else result.success(publicValue(response.getJSONObject("snapshot")))
            } catch (_: Exception) { result.error("owner_error", "VPN owner returned an invalid response.", null) }
        }
    })
    private val connection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName, binder: IBinder) {
            owner = Messenger(binder)
            val requests = waiting.toList(); waiting.clear(); requests.forEach { send(it.first, it.second) }
        }
        override fun onServiceDisconnected(name: ComponentName) { owner = null; failPending() }
    }
    override fun configureFlutterEngine(engine: FlutterEngine) {
        super.configureFlutterEngine(engine)
        bound = bindService(Intent(this, OwnerService::class.java), connection, Context.BIND_AUTO_CREATE)
        MethodChannel(engine.dartExecutor.binaryMessenger, "si.meld.meldnet/v1").setMethodCallHandler { call, result ->
            if (call.method !in setOf("snapshot", "join", "connect", "disconnect", "setAutoConnect")) { result.notImplemented(); return@setMethodCallHandler }
            if (call.method == "connect" || call.method == "join" && call.argument<Boolean>("connect") == true) {
                val intent = VpnService.prepare(this)
                if (intent != null) {
                    if (consent != null) result.error("busy", "VPN consent is already pending.", null)
                    else { consent = call to result; startActivityForResult(intent, 701) }
                    return@setMethodCallHandler
                }
            }
            send(call, result)
        }
    }
    private fun send(call: MethodCall, result: MethodChannel.Result) {
        val remote = owner
        if (remote == null) {
            if (!bound) { result.error("owner_unavailable", "VPN owner could not be reached.", null); return }
            waiting.add(call to result); return
        }
        if (call.method == "join" || call.method == "connect") {
            try { startForegroundService(Intent(this, OwnerService::class.java)) }
            catch (_: Exception) { result.error("background_denied", "Open the app to start this operation.", null); return }
        }
        val request = JSONObject().put("method", call.method).put("args", JSONObject(call.arguments as? Map<*, *> ?: emptyMap<String, Any>()))
        val id = ++sequence; pending[id] = result
        try {
            remote.send(Message.obtain().apply { arg1 = id; replyTo = replies; data = Bundle().apply { putString("request", request.toString()) } })
            handler.postDelayed({ pending.remove(id)?.error("timeout", "Operation is still uncertain. Refresh membership status before retrying.", null) }, 45000)
        } catch (_: Exception) { pending.remove(id)?.error("owner_unavailable", "VPN owner could not be reached.", null) }
    }
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 701) {
            val request = consent ?: return; consent = null
            if (resultCode == Activity.RESULT_OK) send(request.first, request.second)
            else request.second.error("permission_denied", "VPN permission was not granted.", null)
        }
    }
    private fun failPending() {
        pending.values.forEach { it.error("owner_unavailable", "VPN owner disconnected. Refresh observed status.", null) }; pending.clear()
        waiting.forEach { it.second.error("owner_unavailable", "VPN owner disconnected.", null) }; waiting.clear()
    }
    override fun onDestroy() {
        if (bound) unbindService(connection)
        bound = false; owner = null; failPending(); consent?.second?.error("cancelled", "Screen closed during VPN consent.", null); consent = null
        super.onDestroy()
    }
    private fun publicValue(value: Any?): Any? = when (value) {
        null, JSONObject.NULL -> null
        is JSONObject -> value.keys().asSequence().associateWith { publicValue(value.get(it)) }
        is JSONArray -> (0 until value.length()).map { publicValue(value.get(it)) }
        else -> value
    }
}
