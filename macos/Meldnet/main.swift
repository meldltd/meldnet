import AppKit

let args = CommandLine.arguments
// The system LaunchAgent is present for all logins, but opens only for the
// account explicitly authorized by the administrator during installation.
if let index = args.firstIndex(of: "--only-uid") {
    guard index + 1 < args.count, let uid = UInt32(args[index + 1]), uid == getuid() else { exit(0) }
}
let socketPath: String
if let index = args.firstIndex(of: "--socket"), index + 1 < args.count {
    socketPath = args[index + 1]
} else { socketPath = "/var/run/meldnet/control.sock" }
let api = LocalAPI(path: socketPath)

// Read-only diagnostics also allow integration tests without a graphical session.
if args.contains("--check") {
    do {
        let (status, network) = try api.snapshot()
        let data = try JSONEncoder().encode(peerRows(status, network))
        print(String(decoding: data, as: UTF8.self))
        exit(0)
    } catch { fputs("Meldnet: daemon unavailable or invalid response\n", stderr); exit(1) }
}

final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var item: NSStatusItem!
    private let menu = NSMenu()
    private var timer: Timer?
    private var refreshing = false
    private var changing = false
    private var joining = false
    private var openingTUI = false
    private let requests = DispatchQueue(label: "si.meldnet.api", qos: .utility)
    private var networks: [NetworkProfile]?
    private var unavailable = true
    private var feedback: String?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        // Standard responder-chain editing shortcuts also work in the join form.
        let mainMenu = NSMenu()
        let editItem = NSMenuItem(title: "Edit", action: nil, keyEquivalent: "")
        let editMenu = NSMenu(title: "Edit")
        for (title, action, key) in [("Cut", "cut:", "x"), ("Copy", "copy:", "c"), ("Paste", "paste:", "v"), ("Select All", "selectAll:", "a")] {
            editMenu.addItem(NSMenuItem(title: title, action: Selector(action), keyEquivalent: key))
        }
        editItem.submenu = editMenu
        mainMenu.addItem(editItem)
        NSApp.mainMenu = mainMenu
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.image = meldMenuIcon()
        item.button?.image?.isTemplate = true
        item.button?.toolTip = "Meldnet — checking daemon"
        item.menu = menu
        menu.delegate = self
        menu.autoenablesItems = false
        render()
        refresh()
        let timer = Timer(timeInterval: 3, target: self, selector: #selector(refresh), userInfo: nil, repeats: true)
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
    }
    func menuWillOpen(_ menu: NSMenu) { render(); refresh() }

    @objc private func refresh() {
        guard !refreshing && !changing else { return }
        refreshing = true
        requests.async {
            let result = try? api.profiles()
            DispatchQueue.main.async {
                self.networks = result
                self.unavailable = result == nil
                self.refreshing = false
                self.render()
            }
        }
    }
    private func label(_ title: String) {
        let row = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        row.isEnabled = false
        menu.addItem(row)
    }
    private func render() {
        menu.removeAllItems()
        let title = unavailable ? "Meldnet · Daemon unavailable" : "Meldnet · Networks"
        label(title)
        if let profiles = networks, !unavailable {
            for profile in profiles {
                let status = profile.status
                let state = status.backend == "simulation" ? "Simulation" : (status.tunnel.up ? "Interface up" : "Disconnected")
                let row = NSMenuItem(title: "\(profile.id) — \(state)", action: nil, keyEquivalent: "")
                let submenu = NSMenu()
                submenu.autoenablesItems = false
                func note(_ text: String) { let item = NSMenuItem(title: text, action: nil, keyEquivalent: ""); item.isEnabled = false; submenu.addItem(item) }
                let toggle = NSMenuItem(title: status.tunnel.up ? "Disconnect" : "Connect", action: #selector(toggleVPN(_:)), keyEquivalent: "")
                toggle.target = self
                toggle.representedObject = profile.id
                toggle.isEnabled = !changing && !joining && status.node != nil && (status.tunnel.up || (profile.warning ?? "").isEmpty)
                submenu.addItem(toggle)
                let auto = NSMenuItem(title: "Auto-connect at startup", action: #selector(toggleAuto(_:)), keyEquivalent: "")
                auto.target = self
                auto.representedObject = profile.id
                auto.state = profile.auto_connect ? .on : .off
                auto.isEnabled = !changing && !joining
                submenu.addItem(auto)
                if let warning = profile.warning, !warning.isEmpty { note("Conflict: " + warning) }
                if let error = status.error, !error.isEmpty { note(error) }
                if let error = status.dns?.error, !error.isEmpty { note("DNS: " + error) }
                if let error = profile.network.error, !error.isEmpty { note(error) }
                if profile.network.pending == true { note("Enrollment pending · retry Join with this network name") }
                submenu.addItem(.separator())
                let peers = peerRows(status, profile.network)
                if peers.isEmpty { note("No peers yet") }
                for peer in peers {
                    let item = NSMenuItem(title: "\(peer.hostname ?? peer.name) — \(peer.status)", action: #selector(copyHostname(_:)), keyEquivalent: "")
                    item.target = self
                    item.representedObject = peer.hostname
                    item.isEnabled = peer.hostname != nil
                    submenu.addItem(item)
                }
                note("Control activity does not prove VPN reachability")
                row.submenu = submenu
                menu.addItem(row)
            }
            let join = NSMenuItem(title: "Join Network…", action: #selector(joinNetwork), keyEquivalent: "j")
            join.target = self
            join.isEnabled = !changing && !joining
            menu.addItem(join)
        } else {
            label("Check the background service and socket access")
        }
        item.button?.toolTip = title
        menu.addItem(.separator())
        let tui = NSMenuItem(title: "Open TUI", action: #selector(openTUI), keyEquivalent: "t")
        tui.target = self
        tui.isEnabled = !openingTUI
        tui.toolTip = "Open the terminal interface as your current user"
        menu.addItem(tui)
        label(feedback ?? "Click a peer to copy its hostname")
        let refresh = NSMenuItem(title: "Refresh", action: #selector(refresh), keyEquivalent: "r")
        refresh.target = self
        menu.addItem(refresh)
        let quit = NSMenuItem(title: "Quit menu bar app", action: #selector(quitApp), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)
        label("VPN keeps running when this app quits")
    }
    @objc private func openTUI() {
        guard !openingTUI else { return }
        guard let resources = Bundle.main.resourceURL,
              let terminal = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.Terminal") else {
            showMessage("Could not open TUI", "Terminal or the bundled client is unavailable. Reinstall Meldnet and try again.")
            return
        }
        do {
            let launcher = try TUILauncher.prepare(executable: resources.appendingPathComponent("meldnet"), socket: socketPath)
            openingTUI = true
            render()
            let configuration = NSWorkspace.OpenConfiguration()
            configuration.activates = true
            NSWorkspace.shared.open([launcher], withApplicationAt: terminal, configuration: configuration) { _, error in
                DispatchQueue.main.async {
                    self.openingTUI = false
                    self.render()
                    if error != nil {
                        TUILauncher.discard(launcher)
                        self.showMessage("Could not open TUI", "Terminal could not open the client. Please try again.")
                    }
                }
            }
        } catch {
            showMessage("Could not open TUI", "The bundled client or its temporary launcher is unavailable. Reinstall Meldnet and try again.")
        }
    }
    private func showMessage(_ title: String, _ message: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }
    private func change(_ operation: @escaping () throws -> Void) {
        guard !changing else { return }
        changing = true
        feedback = nil
        render()
        requests.async {
            let failure: String?
            do { try operation(); failure = nil }
            catch let error as APIError { failure = error.localizedDescription }
            catch { failure = "The operation could not be completed. Refresh and check the network status." }
            // Fetch observed state after every outcome, including ambiguous timeouts.
            let result = try? api.profiles()
            DispatchQueue.main.async {
                self.changing = false
                self.networks = result
                self.unavailable = result == nil
                self.feedback = failure == nil ? "Request completed" : "Request failed · check network status"
                self.render()
                if let failure = failure { self.showMessage("Could not update Meldnet", ([failure] + (result ?? []).compactMap { $0.warning }).joined(separator: "\n\n")) }
            }
        }
    }
    @objc private func toggleVPN(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String,
              let profile = networks?.first(where: { $0.id == id }), !changing, !joining else { return }
        change { try api.setConnected(!profile.status.tunnel.up, id: id) }
    }
    @objc private func toggleAuto(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String,
              let profile = networks?.first(where: { $0.id == id }), !changing, !joining else { return }
        change { try api.setAutoConnect(!profile.auto_connect, id: id) }
    }
    @objc private func joinNetwork() {
        guard !unavailable, !changing, !joining else { return }
        joining = true
        render()
        defer { joining = false; render() }
        let alert = NSAlert()
        alert.messageText = "Join Network"
        alert.informativeText = "Choose a network name, a device name, and an enrollment key. Each network has independent connection and startup settings."
        alert.addButton(withTitle: "Join")
        alert.addButton(withTitle: "Cancel")
        let fields = NSView(frame: NSRect(x: 0, y: 0, width: 390, height: 220))
        let profileLabel = NSTextField(labelWithString: "Network name (e.g. work, home)")
        profileLabel.frame = NSRect(x: 0, y: 194, width: 390, height: 20)
        let profileName = NSTextField(frame: NSRect(x: 0, y: 168, width: 390, height: 24))
        let nameLabel = NSTextField(labelWithString: "Device name")
        nameLabel.frame = NSRect(x: 0, y: 142, width: 390, height: 20)
        let name = NSTextField(frame: NSRect(x: 0, y: 116, width: 390, height: 24))
        name.placeholderString = "e.g. macbook"
        let keyLabel = NSTextField(labelWithString: "Enrollment key")
        keyLabel.frame = NSRect(x: 0, y: 90, width: 390, height: 20)
        let key = NSSecureTextField(frame: NSRect(x: 0, y: 64, width: 390, height: 24))
        key.placeholderString = "Paste enrollment key"
        let auto = NSButton(checkboxWithTitle: "Auto-connect at startup", target: nil, action: nil)
        auto.frame = NSRect(x: 0, y: 32, width: 390, height: 24)
        auto.state = .on
        let connect = NSButton(checkboxWithTitle: "Connect now", target: nil, action: nil)
        connect.frame = NSRect(x: 0, y: 4, width: 390, height: 24)
        connect.state = .on
        for field in [profileLabel, profileName, nameLabel, name, keyLabel, key, auto, connect] as [NSView] { fields.addSubview(field) }
        alert.accessoryView = fields
        alert.window.initialFirstResponder = profileName
        NSApp.activate(ignoringOtherApps: true)
        while alert.runModal() == .alertFirstButtonReturn {
            let id = profileName.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
            let deviceName = name.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
            let enrollment = key.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
            guard id.range(of: "^[a-z]([a-z0-9-]{0,29}[a-z0-9])?$", options: .regularExpression) != nil, deviceName.range(of: "^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,31}$", options: .regularExpression) != nil,
                  enrollment.hasPrefix("meldnet1."), enrollment.utf8.count <= 8192 else {
                alert.informativeText = "Use a lowercase network name (1–31 letters, digits or hyphens, starting with a letter), a 1–32 character device name (letters, numbers, _, . or -; start with a letter or number) and a valid meldnet1. enrollment key."
                continue
            }
            // No defaults, files, logging or command arguments contain the key.
            key.stringValue = ""
            let request = ProfileJoin(id: id, name: deviceName, key: enrollment, auto_connect: auto.state == .on, connect: connect.state == .on)
            change { try api.joinProfile(request) }
            return
        }
        key.stringValue = ""
    }
    @objc private func copyHostname(_ sender: NSMenuItem) {
        guard let hostname = sender.representedObject as? String else { return }
        NSPasteboard.general.clearContents()
        feedback = NSPasteboard.general.setString(hostname, forType: .string) ? "Copied \(hostname)" : "Could not copy hostname"
        item.button?.image = NSImage(systemSymbolName: "checkmark", accessibilityDescription: feedback)
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
            self.item.button?.image = meldMenuIcon()
            self.item.button?.image?.isTemplate = true
        }
    }
    @objc private func quitApp() { NSApp.terminate(nil) }
}

let app = NSApplication.shared
let existingApps = NSRunningApplication.runningApplications(withBundleIdentifier: "si.meldnet.menubar").filter { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }
if !existingApps.isEmpty {
    if Bundle.main.bundlePath == "/Applications/Meldnet.app" {
        // Prefer the packaged app over a legacy per-user development install.
        for existing in existingApps { existing.terminate() }
    } else { exit(0) }
}
let delegate = AppDelegate()
app.delegate = delegate
app.run()
