import XCTest
@testable import Runner

final class RunnerTests: XCTestCase {
    func testSnapshotNeverContainsNativeSecrets() throws {
        var p = Profile(id: "work", name: "phone", invite: Invite(url: "https://example.test", pin: String(repeating: "a", count: 64), secret: "INVITE_SENTINEL"))
        p.privateKey = "PRIVATE_SENTINEL"
        p.credential = "CREDENTIAL_SENTINEL"
        let bytes = try JSONSerialization.data(withJSONObject: Store.publicProfile(p, state: "disconnected"))
        let output = String(decoding: bytes, as: UTF8.self)
        XCTAssertFalse(output.contains("SENTINEL"))
        XCTAssertFalse(output.contains("example.test"))
        XCTAssertTrue(output.contains("disconnected"))
    }
    func testNativeInputRejectsConfigurationInjection() {
        XCTAssertFalse(Store.validID("../keys"))
        XCTAssertFalse(Store.validName("phone\nPrivateKey=bad"))
        XCTAssertThrowsError(try Store.invite("meldnet1." + String(repeating: "a", count: 8192)))
    }
}
