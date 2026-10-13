import Foundation
import CryptoKit
import Security
import WireGuardKit

final class Control {
    let invite: Invite
    private let persist: (Profile) throws -> Void
    private var excludedPool: String?
    init(_ invite: Invite, persist: @escaping (Profile) throws -> Void = Store.save) { self.invite = invite; self.persist = persist }
    func request(_ path: String, credential: String, body: Data? = nil) async throws -> (Data, Int) {
        try await GoTransport.request(invite, path: path, credential: credential, body: body, excludedPool: excludedPool)
    }
    func reconcile(_ input: Profile) async throws -> Profile {
        var p = input
        excludedPool = p.network?.pool
        guard Store.validID(p.id), Store.validName(p.name), Store.validOrigin(p.invite.url),
              p.invite.pin.range(of: "^[0-9a-fA-F]{64}$", options: .regularExpression) != nil,
              p.registered || Store.decodeURLBase64(p.invite.secret)?.count == 32, !p.revoked else { throw OwnerError.invalid }
        if p.privateKey == nil { p.privateKey = PrivateKey().base64Key }
        if p.credential == nil {
            var bytes = [UInt8](repeating: 0, count: 32)
            guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else { throw OwnerError.storage }
            p.credential = Data(bytes).base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        }
        try persist(p) // Durable identity/credential BEFORE any registration request.
        guard let credential = p.credential, Store.decodeURLBase64(credential)?.count == 32, let privateKey = p.privateKey, let key = PrivateKey(base64Key: privateKey) else { throw OwnerError.storage }
        var (data, status) = try await request("/v1/network", credential: credential)
        if status == 401 && !p.registered {
            let hash = SHA256.hash(data: Data(credential.utf8)).map { String(format: "%02x", $0) }.joined()
            let body = try JSONSerialization.data(withJSONObject: ["name": p.name, "public_key": key.publicKey.base64Key, "credential_hash": hash])
            (data, status) = try await request("/v1/register", credential: p.invite.secret, body: body)
        }
        if status == 401 && p.registered { p.revoked = true; p.error = "Device authorization revoked"; try? persist(p); throw OwnerError.revoked }
        guard status == 200 else { throw OwnerError.network }
        let network = try JSONDecoder().decode(NetworkState.self, from: data)
        try validate(network)
        guard network.name == p.name else { throw OwnerError.invalid }
        _ = try GoTransport.addresses(invite.url, excludedPool: network.pool)
        p.lastControl = Date()
        p.network = network; p.registered = true; p.invite.secret = ""; p.error = nil
        try persist(p); return p
    }
    func validate(_ n: NetworkState) throws { try NetworkValidation.validate(n) }
}
