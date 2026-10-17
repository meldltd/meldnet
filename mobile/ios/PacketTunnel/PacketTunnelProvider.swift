import NetworkExtension
import WireGuardKit

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private lazy var adapter = WireGuardAdapter(with: self) { _, _ in /* Never log backend configuration. */ }
    private let stateLock = NSLock()
    private var worker: Task<Void, Never>?
    private var generation: UUID?
    private var savedNetwork: NetworkState?
    private var savedID: String?
    private func locked<T>(_ body: () throws -> T) rethrows -> T {
        stateLock.lock(); defer { stateLock.unlock() }; return try body()
    }
    private var network: NetworkState? {
        get { locked { savedNetwork } }
        set { locked { savedNetwork = newValue } }
    }
    private var currentID: String? { locked { savedID } }

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        guard let configuration = protocolConfiguration as? NETunnelProviderProtocol,
              let id = configuration.providerConfiguration?["id"] as? String, Store.validID(id) else {
            completionHandler(OwnerError.invalid); return
        }
        let token = UUID()
        locked { worker?.cancel(); savedID = id; generation = token }
        let enrollmentOnly = options?["enrollmentOnly"] as? NSNumber == true
        let task = Task {
            var startCompleted = false
            do {
                guard let saved = try Store.read(id) else { throw OwnerError.storage }
                let control = Control(saved.invite) { [weak self] profile in
                    guard let self = self else { throw CancellationError() }
                    try self.locked {
                        guard self.generation == token else { throw CancellationError() }
                        try Store.save(profile)
                    }
                }
                var profile = try await control.reconcile(saved)
                try Task.checkCancellation()
                guard let n = profile.network else { throw OwnerError.network }
                if enrollmentOnly {
                    startCompleted = true
                    completionHandler(nil)
                    cancelTunnelWithError(nil)
                    return
                }
                network = n
                try await startAdapter(profile, token: token)
                try Task.checkCancellation()
                startCompleted = true
                completionHandler(nil)
                var delay: UInt64 = 3
                while !Task.isCancelled {
                    try await Task.sleep(nanoseconds: delay * 1_000_000_000)
                    do {
                        let refreshed = try await control.reconcile(profile)
                        try Task.checkCancellation()
                        let changed = profile.network != refreshed.network
                        if changed {
                            network = refreshed.network
                            try await updateAdapter(refreshed, token: token)
                        }
                        profile = refreshed; delay = 3
                    } catch OwnerError.revoked {
                        cancelTunnelWithError(NSError(domain: "Meldnet", code: 401)); return
                    } catch is CancellationError { return }
                    catch {
                        profile.error = "Primary control is unavailable; the last valid tunnel is retained."
                        locked { if generation == token { try? Store.save(profile) } }
                        delay = min(delay * 2, 60)
                    }
                }
            } catch is CancellationError {
                if !startCompleted { completionHandler(NSError(domain: "Meldnet", code: NSUserCancelledError)) }
            } catch {
                if var p = try? Store.read(id), !p.revoked { p.error = "Cannot establish the VPN. Check the enrollment and primary."; locked { if generation == token { try? Store.save(p) } } }
                if !startCompleted { completionHandler(NSError(domain: "Meldnet", code: 1)) }
                else { cancelTunnelWithError(NSError(domain: "Meldnet", code: 1)) }
            }
        }
        locked {
            if generation == token { worker = task } else { task.cancel() }
        }
    }
    private func configuration(_ p: Profile) throws -> TunnelConfiguration {
        guard let n = p.network, let privateString = p.privateKey, let privateKey = PrivateKey(base64Key: privateString),
              let address = IPAddressRange(from: n.ip + "/32"), let pool = IPAddressRange(from: n.pool),
              let publicKey = PublicKey(base64Key: n.primary.public_key), let endpoint = Endpoint(from: n.endpoint) else { throw OwnerError.invalid }
        try Control(p.invite).validate(n)
        var local = InterfaceConfiguration(privateKey: privateKey); local.addresses = [address]; local.mtu = 1280
        var peer = PeerConfiguration(publicKey: publicKey); peer.allowedIPs = [pool]; peer.endpoint = endpoint; peer.persistentKeepAlive = 25
        return TunnelConfiguration(name: p.id, interface: local, peers: [peer])
    }
    private func startAdapter(_ p: Profile, token: UUID) async throws {
        let c = try configuration(p)
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            locked {
                guard generation == token else { continuation.resume(throwing: CancellationError()); return }
                adapter.start(tunnelConfiguration: c) { error in
                    if let e = error { continuation.resume(throwing: e) } else { continuation.resume() }
                }
            }
        }
    }
    private func updateAdapter(_ p: Profile, token: UUID) async throws {
        let c = try configuration(p)
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            locked {
                guard generation == token else { continuation.resume(throwing: CancellationError()); return }
                adapter.update(tunnelConfiguration: c) { error in
                    if let e = error { continuation.resume(throwing: e) } else { continuation.resume() }
                }
            }
        }
    }
    override func setTunnelNetworkSettings(_ tunnelNetworkSettings: NETunnelNetworkSettings?, completionHandler: ((Error?) -> Void)? = nil) {
        if let settings = tunnelNetworkSettings, let n = network {
            guard let packetSettings = settings as? NEPacketTunnelNetworkSettings else {
                completionHandler?(OwnerError.invalid); return
            }
            do { try NetworkValidation.applyDNS(n, to: packetSettings) }
            catch { completionHandler?(OwnerError.invalid); return }
        }
        super.setTunnelNetworkSettings(tunnelNetworkSettings, completionHandler: completionHandler)
    }
    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        locked {
            generation = nil
            worker?.cancel(); worker = nil
            // Enqueue stop under the same lock as start/update, so a cancelled
            // enrollment cannot enqueue a late start after teardown.
            adapter.stop { _ in completionHandler() }
        }
    }
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        // Read-only, public IPC. No raw WireGuard runtime configuration is returned.
        guard messageData == Data("snapshot-v1".utf8), let id = currentID else { completionHandler?(nil); return }
        completionHandler?(try? JSONSerialization.data(withJSONObject: ["version": 1, "activeId": id, "interfaceUp": locked { adapter.interfaceName != nil }]))
    }
}
