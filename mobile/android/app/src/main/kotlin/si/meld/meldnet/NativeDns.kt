package si.meld.meldnet

/** Implemented by the pinned WireGuard Go JNI build with Meldnet's DNS TUN adapter. */
internal object NativeDns {
    init { System.loadLibrary("wg-go") }
    external fun request(request: String): String
    external fun configure(policy: String): Int
}
