import Foundation
let request = try JSONSerialization.jsonObject(with: FileHandle.standardInput.readDataToEndOfFile()) as! [String: String]
let url = try TUILauncher.prepare(executable: URL(fileURLWithPath: request["executable"]!), socket: request["socket"]!)
print(url.path)
