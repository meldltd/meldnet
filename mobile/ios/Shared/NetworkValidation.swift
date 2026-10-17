import Foundation
import NetworkExtension

enum NetworkValidation {
    // Called for every WireGuardKit settings publication, including handover.
    // Give the system resolver an explicit route to its private IPv4 authority;
    // do not rely on the broader peer route when choosing a scoped DNS path.
    static func applyDNS(_ n: NetworkState, to settings: NEPacketTunnelNetworkSettings) throws {
        try validate(n)
        guard let dns = n.dns else { settings.dnsSettings = nil; return }
        guard let ipv4 = settings.ipv4Settings else { throw OwnerError.invalid }
        let resolver = NEDNSSettings(servers: [dns.server])
        let fields = n.pool.split(separator: "/")
        let octets = fields[0].split(separator: ".")
        guard let bits = Int(fields[1]), let first = Int(octets[2]) else { throw OwnerError.invalid }
        let zones: [String]
        if bits == 16 { zones = ["\(octets[1]).\(octets[0]).in-addr.arpa"] }
        else if bits < 24 {
            zones = (first..<(first + (1 << (24-bits)))).map { "\($0).\(octets[1]).\(octets[0]).in-addr.arpa" }
        } else { zones = ["\(octets[2]).\(octets[1]).\(octets[0]).in-addr.arpa"] }
        resolver.domainName = dns.domain
        resolver.matchDomains = [dns.domain] + zones
        resolver.matchDomainsNoSearch = true
        resolver.searchDomains = []
        settings.dnsSettings = resolver
        var routes = ipv4.includedRoutes ?? []
        if !routes.contains(where: { $0.destinationAddress == dns.server && $0.destinationSubnetMask == "255.255.255.255" }) {
            routes.append(NEIPv4Route(destinationAddress: dns.server, subnetMask: "255.255.255.255"))
        }
        ipv4.includedRoutes = routes
    }
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
