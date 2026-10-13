import Foundation
import CoreGraphics
import ImageIO
import UniformTypeIdentifiers

// Reuse the existing Meld menu icon vector; do not modify downloaded artwork.
let root = URL(fileURLWithPath: CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : FileManager.default.currentDirectoryPath)
func render(_ size: Int, _ file: URL) throws {
    let context = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: size * 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
    context.setFillColor(CGColor(red: 8/255, green: 8/255, blue: 10/255, alpha: 1))
    context.fill(CGRect(x: 0, y: 0, width: size, height: size))
    let scale = CGFloat(size) * 0.66 / 117
    context.translateBy(x: (CGFloat(size) - 117 * scale) / 2, y: (CGFloat(size) + 72 * scale) / 2)
    context.scaleBy(x: scale, y: -scale)
    context.translateBy(x: 0, y: -28)
        let m = CGMutablePath()
        m.move(to: CGPoint(x: 0, y: 28))
        m.addLine(to: CGPoint(x: 16, y: 28))
        m.addLine(to: CGPoint(x: 16, y: 37))
        m.addCurve(to: CGPoint(x: 36, y: 28), control1: CGPoint(x: 22, y: 31), control2: CGPoint(x: 28, y: 28))
        m.addCurve(to: CGPoint(x: 61, y: 39), control1: CGPoint(x: 47, y: 28), control2: CGPoint(x: 55, y: 31))
        m.addCurve(to: CGPoint(x: 86, y: 28), control1: CGPoint(x: 68, y: 31), control2: CGPoint(x: 76, y: 28))
        m.addCurve(to: CGPoint(x: 117, y: 62), control1: CGPoint(x: 105, y: 28), control2: CGPoint(x: 117, y: 41))
        m.addLine(to: CGPoint(x: 117, y: 72))
        m.addLine(to: CGPoint(x: 96, y: 72))
        m.addLine(to: CGPoint(x: 96, y: 60))
        m.addCurve(to: CGPoint(x: 83, y: 46), control1: CGPoint(x: 96, y: 51), control2: CGPoint(x: 91, y: 46))
        m.addCurve(to: CGPoint(x: 69, y: 60), control1: CGPoint(x: 75, y: 46), control2: CGPoint(x: 69, y: 52))
        m.addLine(to: CGPoint(x: 69, y: 100))
        m.addLine(to: CGPoint(x: 48, y: 100))
        m.addLine(to: CGPoint(x: 48, y: 60))
        m.addCurve(to: CGPoint(x: 35, y: 46), control1: CGPoint(x: 48, y: 51), control2: CGPoint(x: 43, y: 46))
        m.addCurve(to: CGPoint(x: 21, y: 60), control1: CGPoint(x: 27, y: 46), control2: CGPoint(x: 21, y: 52))
        m.addLine(to: CGPoint(x: 21, y: 100))
        m.addLine(to: CGPoint(x: 0, y: 100))
        m.closeSubpath()

    context.addPath(m)
    context.setFillColor(CGColor(red: 241/255, green: 237/255, blue: 229/255, alpha: 1))
    context.fillPath()
    context.setFillColor(CGColor(red: 34/255, green: 190/255, blue: 198/255, alpha: 1))
    context.fill(CGRect(x: 95, y: 79, width: 22, height: 21))
    let image = context.makeImage()!
    let destination = CGImageDestinationCreateWithURL(file as CFURL, UTType.png.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(destination, image, nil)
    guard CGImageDestinationFinalize(destination) else { throw CocoaError(.fileWriteUnknown) }

}
let ios = root.appendingPathComponent("mobile/ios/Runner/Assets.xcassets/AppIcon.appiconset")
let object = try JSONSerialization.jsonObject(with: Data(contentsOf: ios.appendingPathComponent("Contents.json"))) as! [String: Any]
for item in object["images"] as! [[String: Any]] {
    guard let file = item["filename"] as? String, let dimensions = item["size"] as? String, let factor = item["scale"] as? String else { continue }
    let points = Double(dimensions.components(separatedBy: "x")[0])!
    let scale = Double(factor.replacingOccurrences(of: "x", with: ""))!
    try render(Int(points * scale), ios.appendingPathComponent(file))
}
for (density, size) in [("mdpi", 48), ("hdpi", 72), ("xhdpi", 96), ("xxhdpi", 144), ("xxxhdpi", 192)] {
    try render(size, root.appendingPathComponent("mobile/android/app/src/main/res/mipmap-\(density)/ic_launcher.png"))
}
print("Rendered mobile launcher icons from the existing Meld vector.")
