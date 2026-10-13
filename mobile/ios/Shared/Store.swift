import Foundation
import Security

struct Invite: Codable { var url: String; var pin: String; var secret: String }
struct Member: Codable, Equatable { var name: String; var hostname: String?; var public_key: String; var ip: String }
struct DNS: Codable, Equatable { var domain: String; var server: String }
struct NetworkState: Codable, Equatable { var name: String; var ip: String; var pool: String; var primary: Member; var endpoint: String; var members: [Member]; var dns: DNS? }
struct Profile: Codable {
    var id: String; var name: String; var invite: Invite
    var privateKey: String?; var credential: String?; var registered = false
    var network: NetworkState?; var error: String?; var revoked = false; var lastControl: Date?
}
enum OwnerError: Error { case invalid, storage, network, revoked, busy }
struct Store {
    static let service = "si.meld.meldnet.memberships.v1"
    static var group: String { Bundle.main.object(forInfoDictionaryKey: "MeldnetKeychainGroup") as! String }
    static func query(_ id: String) -> [String: Any] { [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: id, kSecAttrAccessGroup as String: group] }
    static func read(_ id: String) throws -> Profile? {
        var q = query(id); q[kSecReturnData as String] = true
        var data: CFTypeRef?; let result = SecItemCopyMatching(q as CFDictionary, &data)
        if result == errSecItemNotFound { return nil }
        guard result == errSecSuccess, let bytes = data as? Data else { throw OwnerError.storage }
        return try JSONDecoder().decode(Profile.self, from: bytes)
    }
    static func all() throws -> [Profile] {
        var q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccessGroup as String: group, kSecReturnData as String: true, kSecMatchLimit as String: kSecMatchLimitAll]
        q[kSecAttrSynchronizable as String] = false
        var items: CFTypeRef?; let result = SecItemCopyMatching(q as CFDictionary, &items)
        if result == errSecItemNotFound { return [] }
        guard result == errSecSuccess, let values = items as? [Data] else { throw OwnerError.storage }
        return try values.map { try JSONDecoder().decode(Profile.self, from: $0) }.sorted { $0.id < $1.id }
    }
    static func save(_ profile: Profile) throws {
        let data = try JSONEncoder().encode(profile)
        let q = query(profile.id)
        let update = SecItemUpdate(q as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if update == errSecSuccess { return }
        guard update == errSecItemNotFound else { throw OwnerError.storage }
        var item = q; item[kSecValueData as String] = data
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        item[kSecAttrSynchronizable as String] = false
        guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else { throw OwnerError.storage }
    }
    static func validID(_ id: String) -> Bool { id.range(of: "^[a-z]([a-z0-9-]{0,29}[a-z0-9])?$", options: .regularExpression) != nil }
    static func validName(_ name: String) -> Bool { name.range(of: "^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$", options: .regularExpression) != nil }
    static func decodeURLBase64(_ s: String) -> Data? {
        let b = s.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        return Data(base64Encoded: b + String(repeating: "=", count: (4-b.count%4)%4))
    }
    static func validOrigin(_ origin: String) -> Bool {
        guard let u = URLComponents(string: origin) else { return false }
        return u.scheme == "https" && u.host?.isEmpty == false && u.user == nil && u.password == nil && u.query == nil && u.fragment == nil && u.path.isEmpty && (u.port.map { (1...65535).contains($0) } ?? true)
    }
    static func invite(_ key: String) throws -> Invite {
        guard key.count <= 8192, key.hasPrefix("meldnet1."), let data = decodeURLBase64(String(key.dropFirst(9))) else { throw OwnerError.invalid }
        let value = try JSONDecoder().decode(Invite.self, from: data)
        guard validOrigin(value.url),
              value.pin.range(of: "^[0-9a-fA-F]{64}$", options: .regularExpression) != nil,
              decodeURLBase64(value.secret)?.count == 32 else { throw OwnerError.invalid }
        return value
    }
    static func publicProfile(_ p: Profile, state: String) -> [String: Any] {
        ["id": p.id, "name": p.name, "state": p.revoked ? "revoked" : state, "autoConnect": false,
         "pending": !p.registered, "address": p.network?.ip as Any? ?? NSNull(), "error": p.error as Any? ?? NSNull(),
         "dnsState": state == "up" && p.network?.dns != nil ? "ready" : "inactive",
         "peers": (p.network?.members ?? []).map { m in ["name": m.name, "hostname": m.hostname ?? "", "address": m.ip,
          "evidence": m.ip == p.network?.ip ? "self" : (state == "up" ? ((p.lastControl.map { Date().timeIntervalSince($0) < 15 } ?? false) ? "control" : "stale") : "disconnected")] }]
    }
}
