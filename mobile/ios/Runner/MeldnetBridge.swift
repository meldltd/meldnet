import Flutter
import NetworkExtension

final class MeldnetBridge {
    private var channel: FlutterMethodChannel?
    private var busy = false
    func register(_ messenger: FlutterBinaryMessenger) {
        channel = FlutterMethodChannel(name: "si.meld.meldnet/v1", binaryMessenger: messenger)
        channel?.setMethodCallHandler { [weak self] call, result in
            guard let self = self else { return }
            guard !self.busy else { result(FlutterError(code: "busy", message: "A VPN operation is in progress", details: nil)); return }
            self.busy = true
            Task { @MainActor in
                defer { self.busy = false }
                do { result(try await self.handle(call)) }
                catch { result(FlutterError(code: "owner_error", message: "The VPN operation could not finish. Check system permission and network settings.", details: nil)) }
            }
        }
    }
    private func manager() async throws -> NETunnelProviderManager {
        let all = try await NETunnelProviderManager.loadAllFromPreferences()
        if let found = all.first(where: { ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == "si.meld.meldnet.PacketTunnel" }) { return found }
        let manager = NETunnelProviderManager(); manager.localizedDescription = "Meldnet"
        let config = NETunnelProviderProtocol(); config.providerBundleIdentifier = "si.meld.meldnet.PacketTunnel"; config.serverAddress = "Meldnet primary"
        manager.protocolConfiguration = config; return manager
    }
    private func active(_ m: NETunnelProviderManager) -> Bool { [.connected, .connecting, .reasserting, .disconnecting].contains(m.connection.status) }
    private func handle(_ call: FlutterMethodCall) async throws -> [String: Any] {
        let manager = try await manager()
        let args = call.arguments as? [String: Any] ?? [:]
        switch call.method {
        case "snapshot": break
        case "join":
            guard !active(manager), let id = args["id"] as? String, Store.validID(id),
                  let name = args["name"] as? String, Store.validName(name), let key = args["key"] as? String,
                  let connect = args["connect"] as? Bool, let auto = args["autoConnect"] as? Bool, !auto else { throw OwnerError.invalid }
            let invite = try Store.invite(key)
            if let old = try Store.read(id) {
                guard !old.registered, old.name == name, old.invite.url == invite.url, old.invite.pin == invite.pin else { throw OwnerError.invalid }
                var retry = old; retry.invite = invite; try Store.save(retry)
            } else {
                guard try Store.all().count < 128 else { throw OwnerError.invalid }
                try Store.save(Profile(id: id, name: name, invite: invite))
            }
            try await start(manager, id: id, enrollmentOnly: !connect)
        case "connect":
            guard let id = args["id"] as? String, Store.validID(id), let p = try Store.read(id), !p.revoked, !active(manager) else { throw OwnerError.invalid }
            try await start(manager, id: id, enrollmentOnly: false)
        case "disconnect": manager.connection.stopVPNTunnel()
        case "setAutoConnect": throw OwnerError.invalid
        default: throw OwnerError.invalid
        }
        return try snapshot(manager)
    }
    private func start(_ manager: NETunnelProviderManager, id: String, enrollmentOnly: Bool) async throws {
        guard let config = manager.protocolConfiguration as? NETunnelProviderProtocol else { throw OwnerError.invalid }
        config.providerConfiguration = ["id": id] // No private configuration in system preferences.
        manager.isEnabled = true
        try await manager.saveToPreferences()
        try await manager.loadFromPreferences()
        try manager.connection.startVPNTunnel(options: ["enrollmentOnly": NSNumber(value: enrollmentOnly)])
    }
    private func snapshot(_ manager: NETunnelProviderManager) throws -> [String: Any] {
        let id = (manager.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration?["id"] as? String
        let state: String
        switch manager.connection.status {
        case .connected: state = "up"
        case .connecting, .reasserting: state = "connecting"
        default: state = "disconnected"
        }
        return ["version": 1, "platform": "ios", "capabilities": ["join": true, "connect": true, "autoConnect": false, "multipleActive": false],
                "activeId": active(manager) ? (id as Any? ?? NSNull()) : NSNull(), "warning": NSNull(),
                "profiles": try Store.all().map { Store.publicProfile($0, state: $0.id == id ? state : "disconnected") }]
    }
}
