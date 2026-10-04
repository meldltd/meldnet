import Foundation

// Terminal opens a command document without Apple Events, admin authorization,
// or launching the daemon. Quote every argument; paths can contain shell syntax.
struct TUILauncher {
    static func prepare(executable: URL, socket: String) throws -> URL {
        let files = FileManager.default
        guard files.isExecutableFile(atPath: executable.path) else { throw LaunchError.missingClient }
        let directory = files.temporaryDirectory.appendingPathComponent("meldnet-tui-" + UUID().uuidString, isDirectory: true)
        try files.createDirectory(at: directory, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        let launcher = directory.appendingPathComponent("Meldnet.command")
        func quote(_ value: String) -> String { "'" + value.replacingOccurrences(of: "'", with: "'\"'\"'") + "'" }
        // Remove the private launcher after Terminal has opened it. Nothing is
        // persisted in shell history, and no enrollment or private key is involved.
        let script = "#!/bin/sh\n/bin/rm -f -- \(quote(launcher.path))\n/bin/rmdir -- \(quote(directory.path))\nexec \(quote(executable.path)) --socket \(quote(socket)) tui\n"
        do {
            try Data(script.utf8).write(to: launcher, options: .atomic)
            try files.setAttributes([.posixPermissions: 0o700], ofItemAtPath: launcher.path)
            return launcher
        } catch {
            try? files.removeItem(at: directory)
            throw error
        }
    }
    static func discard(_ launcher: URL) { try? FileManager.default.removeItem(at: launcher.deletingLastPathComponent()) }
    enum LaunchError: Error { case missingClient }
}
