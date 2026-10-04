import AppKit

// Vector adaptation of the first letter in https://meld.si's wordmark
// (/assets/meld-logo-2KoF4xHz.png, inspected 2026-10-04). The detached accent
// square remains part of the silhouette. A template lets macOS choose contrast.
func meldMenuIcon() -> NSImage {
    let image = NSImage(size: NSSize(width: 22, height: 18), flipped: false) { rect in
        let transform = NSAffineTransform()
        let scale = rect.width / 117
        transform.translateX(by: 0, yBy: (rect.height + 72 * scale) / 2)
        transform.scaleX(by: scale, yBy: -scale)
        transform.translateX(by: 0, yBy: -28)
        let m = NSBezierPath()
        m.move(to: NSPoint(x: 0, y: 28))
        m.line(to: NSPoint(x: 16, y: 28))
        m.line(to: NSPoint(x: 16, y: 37))
        m.curve(to: NSPoint(x: 36, y: 28), controlPoint1: NSPoint(x: 22, y: 31), controlPoint2: NSPoint(x: 28, y: 28))
        m.curve(to: NSPoint(x: 61, y: 39), controlPoint1: NSPoint(x: 47, y: 28), controlPoint2: NSPoint(x: 55, y: 31))
        m.curve(to: NSPoint(x: 86, y: 28), controlPoint1: NSPoint(x: 68, y: 31), controlPoint2: NSPoint(x: 76, y: 28))
        m.curve(to: NSPoint(x: 117, y: 62), controlPoint1: NSPoint(x: 105, y: 28), controlPoint2: NSPoint(x: 117, y: 41))
        m.line(to: NSPoint(x: 117, y: 72))
        m.line(to: NSPoint(x: 96, y: 72))
        m.line(to: NSPoint(x: 96, y: 60))
        m.curve(to: NSPoint(x: 83, y: 46), controlPoint1: NSPoint(x: 96, y: 51), controlPoint2: NSPoint(x: 91, y: 46))
        m.curve(to: NSPoint(x: 69, y: 60), controlPoint1: NSPoint(x: 75, y: 46), controlPoint2: NSPoint(x: 69, y: 52))
        m.line(to: NSPoint(x: 69, y: 100))
        m.line(to: NSPoint(x: 48, y: 100))
        m.line(to: NSPoint(x: 48, y: 60))
        m.curve(to: NSPoint(x: 35, y: 46), controlPoint1: NSPoint(x: 48, y: 51), controlPoint2: NSPoint(x: 43, y: 46))
        m.curve(to: NSPoint(x: 21, y: 60), controlPoint1: NSPoint(x: 27, y: 46), controlPoint2: NSPoint(x: 21, y: 52))
        m.line(to: NSPoint(x: 21, y: 100))
        m.line(to: NSPoint(x: 0, y: 100))
        m.close()
        m.appendRect(NSRect(x: 95, y: 79, width: 22, height: 21))
        m.transform(using: transform as AffineTransform)
        NSColor.black.setFill()
        m.fill()
        return true
    }
    image.isTemplate = true
    image.accessibilityDescription = "Meldnet"
    return image
}
