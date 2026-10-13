import Foundation
import Darwin
import WireGuardKitGo

// URLSession's TLS stack cannot negotiate the existing primary's Ed25519
// certificate. Use the maintained Go TLS stack already embedded in this owner.
// Inputs and output remain native; no private key/credential reaches Flutter.
enum GoTransport {
    private final class Cancellation: @unchecked Sendable {
        private let lock = NSLock()
        private var value = false
        var cancelled: Bool { lock.lock(); defer { lock.unlock() }; return value }
        func cancel() { lock.lock(); value = true; lock.unlock() }
    }
    private final class Resolution: @unchecked Sendable {
        private let lock = NSLock()
        private var value: Result<[String], Error>?
        func set(_ result: Result<[String], Error>) { lock.lock(); value = result; lock.unlock() }
        func get() throws -> [String] { lock.lock(); defer { lock.unlock() }; return try value!.get() }
    }
    static func addresses(_ origin: String, excludedPool: String?, isCancelled: @escaping () -> Bool = { false }) throws -> [String] {
        let result = Resolution(), done = DispatchSemaphore(value: 0)
        DispatchQueue.global(qos: .utility).async {
            result.set(Result { try resolve(origin, excludedPool: excludedPool) })
            done.signal()
        }
        let deadline = DispatchTime.now().uptimeNanoseconds + 6_000_000_000
        while done.wait(timeout: .now() + .milliseconds(100)) == .timedOut {
            if isCancelled() { throw CancellationError() }
            if DispatchTime.now().uptimeNanoseconds >= deadline { throw OwnerError.network }
        }
        if isCancelled() { throw CancellationError() }
        return try result.get()
    }
    private static func resolve(_ origin: String, excludedPool: String?) throws -> [String] {
        guard Store.validOrigin(origin), var host = URLComponents(string: origin)?.host else { throw OwnerError.invalid }
        if host.hasPrefix("[") && host.hasSuffix("]") { host = String(host.dropFirst().dropLast()) }
        let name = host.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "."))
        guard name != "meldnet.internal", !name.hasSuffix(".meldnet.internal") else { throw OwnerError.invalid }
        var hints = addrinfo(); hints.ai_family = AF_UNSPEC; hints.ai_socktype = SOCK_STREAM; hints.ai_protocol = IPPROTO_TCP
        var entries: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host, nil, &hints, &entries) == 0 else { throw OwnerError.network }
        defer { freeaddrinfo(entries) }
        var cursor = entries; var result = [String]()
        while let entry = cursor {
            cursor = entry.pointee.ai_next
            guard entry.pointee.ai_family == AF_INET || entry.pointee.ai_family == AF_INET6 else { continue }
            var buffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(entry.pointee.ai_addr, entry.pointee.ai_addrlen, &buffer, socklen_t(buffer.count), nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let address = String(cString: buffer)
            if let pool = excludedPool, NetworkValidation.contains(pool: pool, address: address) { throw OwnerError.invalid }
            if !result.contains(address) { result.append(address) }
        }
        guard !result.isEmpty, result.count <= 16 else { throw OwnerError.network }
        return result
    }
    static func request(_ invite: Invite, path: String, credential: String, body: Data?, excludedPool: String?) async throws -> (Data, Int) {
        let cancellation = Cancellation()
        return try await withTaskCancellationHandler(operation: {
          try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .utility).async {
                do {
                    var request: [String: Any] = ["origin": invite.url, "path": path, "pin": invite.pin, "bearer": credential,
                                                  "addresses": try addresses(invite.url, excludedPool: excludedPool, isCancelled: { cancellation.cancelled }), "networkHandle": 1]
                    if let body = body { request["body"] = try JSONSerialization.jsonObject(with: body) }
                    let input = try JSONSerialization.data(withJSONObject: request)
                    guard input.count <= 32768, let text = String(data: input, encoding: .utf8) else { throw OwnerError.invalid }
                    if cancellation.cancelled { throw CancellationError() }
                    let output = text.withCString { meldnetControl($0) }
                    guard let output = output else { throw OwnerError.network }
                    defer { meldnetControlFree(output) }
                    if cancellation.cancelled { throw CancellationError() }
                    let data = Data(bytes: output, count: strlen(output))
                    guard let response = try JSONSerialization.jsonObject(with: data) as? [String: Any],
                          let status = response["status"] as? Int, status != 0 else { throw OwnerError.network }
                    let bytes = try response["body"].map { try JSONSerialization.data(withJSONObject: $0, options: [.fragmentsAllowed]) } ?? Data()
                    continuation.resume(returning: (bytes, status))
                } catch {
                    let failure: Error = cancellation.cancelled ? CancellationError() : OwnerError.network
                    continuation.resume(throwing: failure)
                }
            }
          }
        }, onCancel: { cancellation.cancel() })
    }
}
