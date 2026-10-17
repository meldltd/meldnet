import Foundation
import NetworkExtension

func require(_ condition: @autoclosure () -> Bool, _ message: String) {
    if !condition() { fatalError(message) }
}
func key(_ value: [String: String]) throws -> String {
    let b = try JSONSerialization.data(withJSONObject: value).base64EncodedString()
    return "meldnet1." + b.replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
}
let synthetic = ["url": "https://primary.example:8443", "pin": String(repeating: "a", count: 64), "secret": String(repeating: "A", count: 43)]
let invite = try Store.invite(key(synthetic))
require(invite.url == synthetic["url"], "valid origin")
for origin in ["http://primary.example", "https://user:pass@primary.example", "https://primary.example/", "https://primary.example?token=bad", "https://primary.example#bad", "file:///tmp/test"] {
    var input = synthetic; input["url"] = origin
    do { _ = try Store.invite(key(input)); fatalError("unsafe origin accepted") } catch {}
}
for field in ["pin", "secret"] {
    var input = synthetic; input[field] = "bad"
    do { _ = try Store.invite(key(input)); fatalError("invalid credential material accepted") } catch {}
}
do { _ = try Store.invite("meldnet1." + String(repeating: "A", count: 9000)); fatalError("oversized invite accepted") } catch {}
require(!Store.validID("../network"), "path ID refused")
require(!Store.validName("phone\nprivate_key=bad"), "configuration injection refused")
var profile = Profile(id: "work", name: "phone", invite: invite)
profile.privateKey = "PRIVATE_KEY_SENTINEL"; profile.credential = "DEVICE_CREDENTIAL_SENTINEL"
let publicData = try JSONSerialization.data(withJSONObject: Store.publicProfile(profile, state: "disconnected"))
let publicJSON = String(data: publicData, encoding: .utf8)!
for secret in ["PRIVATE_KEY_SENTINEL", "DEVICE_CREDENTIAL_SENTINEL", invite.secret, invite.pin, invite.url] {
    require(!publicJSON.contains(secret), "private material escaped public projection")
}
require(publicJSON.contains("disconnected"), "honest disconnected state")
print("PASS: invite origin/material validation, injection rejection, size bound, public snapshot secret isolation")

let syntheticPublicKey = Data(repeating: 1, count: 32).base64EncodedString()
let primary = Member(name: "primary", public_key: syntheticPublicKey, ip: "10.77.0.1")
let member = Member(name: "phone", public_key: syntheticPublicKey, ip: "10.77.0.2")
let validNetwork = NetworkState(name: "phone", ip: "10.77.0.2", pool: "10.77.0.0/24", primary: primary, endpoint: "primary.example:51820", members: [primary, member], dns: DNS(domain: "meldnet.internal", server: "10.77.0.1"))
try NetworkValidation.validate(validNetwork)
var invalidNetworks = [NetworkState]()
var changed = validNetwork; changed.pool = "0.0.0.0/0"; invalidNetworks.append(changed)
changed = validNetwork; changed.pool = "8.8.0.0/16"; invalidNetworks.append(changed)
changed = validNetwork; changed.ip = "10.78.0.2"; invalidNetworks.append(changed)
changed = validNetwork; changed.ip = "10.77.0.255"; invalidNetworks.append(changed)
changed = validNetwork; changed.endpoint = "user:pass@primary.example:51820"; invalidNetworks.append(changed)
changed = validNetwork; changed.members.append(member); invalidNetworks.append(changed)
changed = validNetwork; changed.dns = DNS(domain: "", server: "8.8.8.8"); invalidNetworks.append(changed)
changed = validNetwork; changed.dns = DNS(domain: "meldnet.internal", server: "8.8.8.8"); invalidNetworks.append(changed)
for n in invalidNetworks {
    do { try NetworkValidation.validate(n); fatalError("unsafe network accepted") } catch {}
}
print("PASS: native owner rejects public/default routes, out-of-pool/broadcast addresses, duplicate members, endpoint credentials, and DNS escape")

require(Store.validID("a"), "single letter ID")
require(Store.validID("a" + String(repeating: "b", count: 30)), "31-character ID")
require(!Store.validID("a" + String(repeating: "b", count: 31)), "32-character ID refused")
require(Store.validID("work-office"), "internal hyphen accepted")
require(!Store.validID("work-"), "trailing hyphen refused")
require(!Store.validID("work_office"), "underscore refused")
print("PASS: desktop-compatible profile ID boundaries")

require(NetworkValidation.contains(pool: "10.77.0.0/24", address: "10.77.0.1"), "control destination pool overlap")
require(NetworkValidation.contains(pool: "10.77.0.0/24", address: "::ffff:10.77.0.1"), "mapped control destination pool overlap")
require(!NetworkValidation.contains(pool: "10.77.0.0/24", address: "192.168.1.5"), "outside-pool control destination")

func tunnelSettings() -> NEPacketTunnelNetworkSettings {
    let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
    let ipv4 = NEIPv4Settings(addresses: ["10.77.0.2"], subnetMasks: ["255.255.255.255"])
    ipv4.includedRoutes = [NEIPv4Route(destinationAddress: "10.77.0.0", subnetMask: "255.255.255.0")]
    settings.ipv4Settings = ipv4
    return settings
}
let settings = tunnelSettings()
try NetworkValidation.applyDNS(validNetwork, to: settings)
require(settings.dnsSettings?.servers == ["10.77.0.1"], "DNS uses only primary")
require(settings.dnsSettings?.domainName == "meldnet.internal", "private tunnel DNS domain")
require(settings.dnsSettings?.matchDomains == ["meldnet.internal", "0.77.10.in-addr.arpa"], "forward/reverse split DNS")
require(settings.dnsSettings?.matchDomainsNoSearch == true, "no bare-name search")
require(settings.dnsSettings?.searchDomains == [], "no system search domains")
require(settings.ipv4Settings?.includedRoutes?.count == 2, "peer and resolver routes retained")
let resolverRoute = settings.ipv4Settings!.includedRoutes!.last!
require(resolverRoute.destinationAddress == "10.77.0.1" && resolverRoute.destinationSubnetMask == "255.255.255.255", "explicit DNS host route")
try NetworkValidation.applyDNS(validNetwork, to: settings)
require(settings.ipv4Settings?.includedRoutes?.count == 2, "handover does not duplicate DNS route")
var legacyNetwork = validNetwork; legacyNetwork.dns = nil
let legacySettings = tunnelSettings()
try NetworkValidation.applyDNS(legacyNetwork, to: legacySettings)
require(legacySettings.dnsSettings == nil && legacySettings.ipv4Settings?.includedRoutes?.count == 1, "older primary leaves DNS untouched")
for invalid in invalidNetworks {
    do { try NetworkValidation.applyDNS(invalid, to: tunnelSettings()); fatalError("unsafe DNS settings published") } catch {}
}
for (pool, ip, server, count, first, last) in [
    ("10.77.0.0/16", "10.77.0.2", "10.77.0.1", 1, "77.10.in-addr.arpa", "77.10.in-addr.arpa"),
    ("10.77.16.0/20", "10.77.16.2", "10.77.16.1", 16, "16.77.10.in-addr.arpa", "31.77.10.in-addr.arpa"),
    ("10.77.32.0/28", "10.77.32.2", "10.77.32.1", 1, "32.77.10.in-addr.arpa", "32.77.10.in-addr.arpa")
] {
    var n = validNetwork; n.pool = pool; n.ip = ip
    n.primary.ip = server; n.members = [n.primary]; n.dns!.server = server
    let settings = tunnelSettings(); try NetworkValidation.applyDNS(n, to: settings)
    let domains = settings.dnsSettings!.matchDomains!
    require(domains.count == count+1 && domains[1] == first && domains.last == last, "private reverse zone boundaries")
    require(!domains.contains(""), "public DNS never captured")
}
profile.network = validNetwork
require(Store.publicProfile(profile, state: "up")["dnsState"] as? String == "configured", "configuration is not lookup evidence")
require(Store.publicProfile(profile, state: "disconnected")["dnsState"] as? String == "inactive", "disconnected DNS inactive")
print("PASS: production iOS DNS settings, private-only matching, explicit resolver route, reapply, legacy primary, reverse pools and honest configuration status")
