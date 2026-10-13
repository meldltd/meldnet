import Foundation

enum NetworkValidation {
    static func contains(pool: String, address: String) -> Bool {
        let fields = pool.split(separator: "/")
        guard fields.count == 2, let bits = Int(fields[1]), (1...32).contains(bits) else { return true }
        func number(_ value: String) -> UInt32? {
            let values = value.split(separator: "."); guard values.count == 4 else { return nil }
            var result: UInt32 = 0
            for value in values { guard let byte = UInt8(value) else { return nil }; result = result << 8 | UInt32(byte) }
            return result
        }
        var normalized = address
        if normalized.hasPrefix("::ffff:") { normalized = String(normalized.dropFirst(7)) }
        guard let value = number(normalized), let base = number(String(fields[0])) else { return false }
        let mask = UInt32.max << (32-bits)
        return value & mask == base & mask
    }
    static func validKey(_ value: String) -> Bool {
        guard value.count == 44, let decoded = Data(base64Encoded: value) else { return false }
        return decoded.count == 32 && decoded.base64EncodedString() == value
    }
    static func validEndpoint(_ value: String) -> Bool {
        guard value.count <= 1024, let endpoint = URLComponents(string: "udp://" + value),
              endpoint.host?.isEmpty == false, let port = endpoint.port, (1...65535).contains(port),
              endpoint.path.isEmpty, endpoint.user == nil, endpoint.password == nil,
              endpoint.query == nil, endpoint.fragment == nil else { return false }
        return true
    }
    static func validate(_ n: NetworkState) throws {
        func ipv4(_ s: String) -> UInt32? {
            let fields = s.split(separator: ".", omittingEmptySubsequences: false)
            guard fields.count == 4 else { return nil }
            var result: UInt32 = 0
            for f in fields { guard let byte = UInt8(f), String(byte) == f else { return nil }; result = result << 8 | UInt32(byte) }
            return result
        }
        let parts = n.pool.split(separator: "/")
        guard parts.count == 2, let bits = Int(parts[1]), (16...28).contains(bits), let pool = ipv4(String(parts[0])),
              pool >> 24 == 10 || pool >> 20 == 0xac1 || pool >> 16 == 0xc0a8 else { throw OwnerError.invalid }
        let mask = UInt32.max << (32-bits)
        guard pool & mask == pool, let ip = ipv4(n.ip), ip & mask == pool, ip > pool+1, ip < pool | ~mask,
              let primary = ipv4(n.primary.ip), primary == pool+1, Store.validName(n.primary.name), validKey(n.primary.public_key),
              validEndpoint(n.endpoint), n.members.count <= 128 else { throw OwnerError.invalid }
        var seen = Set<String>()
        for m in n.members {
            guard Store.validName(m.name), let address = ipv4(m.ip), address & mask == pool,
                  address > pool, address < pool | ~mask, validKey(m.public_key), seen.insert(m.ip).inserted else { throw OwnerError.invalid }
        }
        if let dns = n.dns { guard dns.server == n.primary.ip, dns.domain == "meldnet.internal" else { throw OwnerError.invalid } }
    }
}
