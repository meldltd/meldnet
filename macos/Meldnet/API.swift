import Foundation
import Darwin

struct Peer: Decodable {
    let public_key: String
    let last_handshake: String?
}
struct Settings: Decodable { let name: String }
struct Node: Decodable { let public_key: String; let settings: Settings }
struct Tunnel: Decodable { let up: Bool; let peers: [Peer]? }
struct DNS: Decodable { let error: String? }
struct Status: Decodable {
    let backend: String
    let initialized: Bool?
    let node: Node?
    let tunnel: Tunnel
    let error: String?
    let dns: DNS?
}
struct Member: Decodable {
    let name: String
    let hostname: String?
    let public_key: String
    let last_seen: String
}
struct Network: Decodable {
    let role: String
    let members: [Member]?
    let error: String?
    let pending: Bool?
}
struct NetworkProfile: Decodable {
    let id: String
    let auto_connect: Bool
    let status: Status
    let network: Network
    let warning: String?
}
struct ProfileJoin: Encodable {
    let id: String
    let name: String
    let key: String
    let auto_connect: Bool
    let connect: Bool
}
struct MenuPeer: Encodable {
    let hostname: String?
    let name: String
    let status: String
}

func recent(_ stamp: String?, within seconds: TimeInterval, now: Date) -> Bool {
    guard let stamp = stamp else { return false }
    let format = ISO8601DateFormatter()
    format.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    var date = format.date(from: stamp)
    if date == nil {
        format.formatOptions = [.withInternetDateTime]
        date = format.date(from: stamp)
    }
    guard let date = date else { return false }
    let age = now.timeIntervalSince(date)
    return age >= -5 && age < seconds
}

func peerRows(_ status: Status, _ network: Network, now: Date = Date()) -> [MenuPeer] {
    return (network.members ?? []).sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }.map { member in
        let state: String
        if status.backend == "simulation" { state = "Simulation" }
        else if member.public_key == status.node?.public_key { state = "This Mac" }
        else if !status.tunnel.up { state = "VPN disconnected" }
        else if let peer = status.tunnel.peers?.first(where: { $0.public_key == member.public_key }),
                recent(peer.last_handshake, within: 180, now: now) { state = "Recent handshake" }
        else if !(network.error ?? "").isEmpty { state = "Status unknown" }
        else if recent(member.last_seen, within: 15, now: now) { state = "Active · control" }
        else { state = "Inactive · control" }
        return MenuPeer(hostname: member.hostname.flatMap { $0.isEmpty ? nil : $0 }, name: member.name, status: state)
    }
}

// HTTP/1.0 with Connection: close avoids chunked framing. Only the public,
// versioned daemon API is used; this process never opens private state files.
struct LocalAPI {
    let path: String
    private func request(_ method: String, _ route: String, body: Data = Data()) throws -> Data {
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw APIError.unavailable }
        defer { Darwin.close(fd) }
        var timeout = timeval(tv_sec: method == "GET" ? 6 : 40, tv_usec: 0)
        var one: Int32 = 1
        guard setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout.size(ofValue: timeout))) == 0,
              setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout.size(ofValue: timeout))) == 0,
              setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout.size(ofValue: one))) == 0 else { throw APIError.unavailable }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8) + [0]
        guard path.hasPrefix("/"), bytes.count <= MemoryLayout.size(ofValue: address.sun_path) else { throw APIError.unavailable }
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { dest in dest.copyBytes(from: bytes) }
        let connected = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        guard connected == 0 else { throw APIError.unavailable }
        var request = Data("\(method) \(route) HTTP/1.0\r\nHost: meldnet\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: \(body.count)\r\n\r\n".utf8)
        request.append(body)
        try request.withUnsafeBytes { buffer in
            var sent = 0
            while sent < buffer.count {
                let n = Darwin.write(fd, buffer.baseAddress!.advanced(by: sent), buffer.count - sent)
                if n < 0 && errno == EINTR { continue }
                guard n > 0 else { throw APIError.unavailable }
                sent += n
            }
        }
        var response = Data()
        var buffer = [UInt8](repeating: 0, count: 8192)
        let deadline = Date().addingTimeInterval(method == "GET" ? 8 : 45)
        while true {
            guard Date() < deadline else { throw APIError.unavailable }
            let n = Darwin.read(fd, &buffer, buffer.count)
            if n < 0 && errno == EINTR { continue }
            guard n >= 0 else { throw APIError.unavailable }
            if n == 0 { break }
            response.append(contentsOf: buffer.prefix(n))
            guard response.count <= 1_048_576 else { throw APIError.invalidResponse }
        }
        guard let split = response.range(of: Data("\r\n\r\n".utf8)),
              let header = String(data: response[..<split.lowerBound], encoding: .utf8),
              header.hasPrefix("HTTP/1.0 ") || header.hasPrefix("HTTP/1.1 "),
              let code = Int(header.split(separator: " ").dropFirst().first ?? "") else { throw APIError.invalidResponse }
        // Never surface raw response bodies: a failed enrollment must not echo a key.
        guard code == 200 else { throw APIError.rejected(code) }
        return Data(response[split.upperBound...])
    }
    func get<T: Decodable>(_ route: String, as type: T.Type) throws -> T {
        try JSONDecoder().decode(type, from: request("GET", route))
    }
    func setConnected(_ enabled: Bool) throws {
        try mutate(enabled ? "/v1/up" : "/v1/down")
    }
    func join(name: String, key: String) throws {
        let body = try JSONEncoder().encode(JoinRequest(name: name, key: key))
        try mutate("/v1/network/join", body: body)
    }
    private func mutate(_ route: String, body: Data = Data()) throws {
        let reply = try JSONDecoder().decode(Acknowledgement.self, from: request("POST", route, body: body))
        guard reply.ok else { throw APIError.invalidResponse }
    }
    func profiles() throws -> [NetworkProfile] { try get("/v1/networks", as: [NetworkProfile].self) }
    private func profileRoute(_ id: String, _ action: String) throws -> String {
        guard id.range(of: "^[a-z]([a-z0-9-]{0,29}[a-z0-9])?$", options: .regularExpression) != nil else { throw APIError.invalidResponse }
        return "/v1/networks/\(id)/\(action)"
    }
    func setConnected(_ enabled: Bool, id: String) throws { try mutate(profileRoute(id, enabled ? "up" : "down")) }
    func setAutoConnect(_ enabled: Bool, id: String) throws {
        let data = try JSONEncoder().encode(["auto_connect": enabled])
        let reply = try JSONDecoder().decode(Acknowledgement.self, from: request("PUT", profileRoute(id, "autoconnect"), body: data))
        guard reply.ok else { throw APIError.invalidResponse }
    }
    func joinProfile(_ profile: ProfileJoin) throws { try mutate("/v1/networks/join", body: JSONEncoder().encode(profile)) }
    func snapshot() throws -> (Status, Network) {
        (try get("/v1/status", as: Status.self), try get("/v1/network", as: Network.self))
    }
}
private struct JoinRequest: Encodable { let name: String; let key: String }
private struct Acknowledgement: Decodable { let ok: Bool }

enum APIError: LocalizedError {
    case unavailable, invalidResponse, rejected(Int)
    var errorDescription: String? {
        switch self {
        case .unavailable: return "The daemon did not respond. Check its status before trying again; the operation may still have completed."
        case .invalidResponse: return "The daemon returned an unexpected response. Refresh to check the current state."
        case .rejected(let code): return "The daemon rejected the request (HTTP \(code)). Check the network status and enrollment details before retrying."
        }
    }
}
