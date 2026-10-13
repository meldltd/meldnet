import Foundation

@main struct HTTPSChecks {
    static func require(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() { fatalError(message) }
    }
    static func main() async throws {
        let data = try Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1]))
        let values = try JSONDecoder().decode([String: String].self, from: data)
        func client(_ variant: String, wrongPin: Bool = false) -> Invite {
            Invite(url: values[variant + "URL"]!, pin: wrongPin ? String(repeating: "0", count: 64) : values[variant + "Pin"]!, secret: "")
        }
        let credential = String(repeating: "A", count: 43)
        func request(_ invite: Invite) async throws -> (Data, Int) {
            try await GoTransport.request(invite, path: "/v1/network", credential: credential, body: nil, excludedPool: nil)
        }
        let (body, status) = try await request(client("valid"))
        let object = try JSONSerialization.jsonObject(with: body) as? [String: Bool]
        require(status == 200 && object?["okay"] == true, "actual TLS 1.3 Ed25519 primary handshake")
        let (_, redirectStatus) = try await request(client("redirect"))
        require(redirectStatus == 302, "redirect must never be followed")
        for bad in [client("valid", wrongPin: true), client("expired"), client("future")] {
            do { _ = try await request(bad); fatalError("invalid certificate accepted over real TLS") } catch {}
        }
        do { _ = try await request(client("overflow")); fatalError("streaming response bound bypassed") } catch {}
        do { _ = try GoTransport.addresses("https://primary.meldnet.internal", excludedPool: nil); fatalError("private control DNS name leaked") } catch {}
        do { _ = try GoTransport.addresses("https://127.0.0.1", excludedPool: nil, isCancelled: { true }); fatalError("cancelled resolution accepted") } catch {}
        let cancelled = Task { try await request(client("valid")) }
        cancelled.cancel()
        do { _ = try await cancelled.value; fatalError("cancelled native request accepted") } catch {}
        print("PASS: real loopback native Go TLS1.3 with primary-style Ed25519, wrong pin/expired/future refusal, redirect refusal, streamed response bound, private DNS rejection, cancellation")
    }
}
