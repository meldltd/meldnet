package si.meld.meldnet

import android.net.DnsResolver
import android.net.InetAddresses
import android.net.Network
import android.os.CancellationSignal
import java.io.IOException
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** Resolves only through the selected underlay, with a cancellable whole-query bound. */
internal object UnderlayResolver {
    private val callbacks = Executors.newSingleThreadExecutor { task ->
        Thread(task, "meldnet-underlay-dns").apply { isDaemon = true }
    }
    fun resolve(network: Network, host: String): Array<InetAddress> {
        runCatching { InetAddresses.parseNumericAddress(host.removePrefix("[").removeSuffix("]")) }
            .getOrNull()?.let { return arrayOf(it) }
        val done = CountDownLatch(1)
        val cancel = CancellationSignal()
        var addresses: List<InetAddress>? = null
        DnsResolver.getInstance().query(network, host, DnsResolver.FLAG_EMPTY, callbacks, cancel,
            object : DnsResolver.Callback<List<InetAddress>> {
                override fun onAnswer(answer: List<InetAddress>, rcode: Int) {
                    if (rcode == 0) addresses = answer
                    done.countDown()
                }
                override fun onError(error: DnsResolver.DnsException) { done.countDown() }
            })
        try {
            if (!done.await(5, TimeUnit.SECONDS)) throw IOException("Underlay resolution timed out")
            return addresses?.takeIf { it.isNotEmpty() }?.toTypedArray() ?: throw IOException("Underlay resolution failed")
        } finally { cancel.cancel() }
    }
}
