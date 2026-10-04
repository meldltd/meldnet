// Test-only driver: synthetic enrollment arrives on stdin, never in arguments.
import Foundation
struct Input: Decodable { let action: String; let name: String?; let key: String?; let id: String? }
do {
    let input = try JSONDecoder().decode(Input.self, from: FileHandle.standardInput.readDataToEndOfFile())
    let api = LocalAPI(path: CommandLine.arguments[1])
    switch input.action {
    case "profile-up": try api.setConnected(true, id: input.id!)
    case "profile-down": try api.setConnected(false, id: input.id!)
    case "profile-auto": try api.setAutoConnect(false, id: input.id!)
    case "profile-join": try api.joinProfile(ProfileJoin(id: input.id!, name: input.name!, key: input.key!, auto_connect: false, connect: true))
    case "profiles": _ = try api.profiles()
    case "up": try api.setConnected(true)
    case "down": try api.setConnected(false)
    case "join": try api.join(name: input.name!, key: input.key!)
    default: exit(2)
    }
    print("ok")
} catch let error as APIError {
    fputs(error.localizedDescription + "\n", stderr)
    exit(1)
} catch {
    fputs("Request failed\n", stderr)
    exit(1)
}
