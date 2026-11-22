import Foundation
import Network
import Observation

@Observable
@MainActor
final class ServerDiscoveryService {
    enum ConnectionState: Equatable {
        case idle
        case discovering
        case connected(baseURL: URL, via: String)
        case unavailable(String)
    }

    var connectionState: ConnectionState = .idle
    var activeBaseURL: URL?
    var serverInfo: DiscoverResponse?
    var discoveredCandidates: [URL] = []

    private let savedURLKey = "folio.savedBaseURL"
    private let defaultCandidates: [String] = [
        "https://folio",
        "http://folio:8080",
        "http://folio:3000",
        "http://folio.local:8080",
        "http://localhost:8080"
    ]

    private var bonjourBrowser: NWBrowser?

    init() {
        if let saved = UserDefaults.standard.string(forKey: savedURLKey),
           let url = URL(string: saved) {
            self.activeBaseURL = url
        }
    }

    func discoverAutomatically() async {
        connectionState = .discovering

        var candidates: [URL] = []
        if let saved = UserDefaults.standard.string(forKey: savedURLKey),
           let savedURL = URL(string: saved) {
            candidates.append(savedURL)
        }
        for raw in defaultCandidates {
            if let url = URL(string: raw), !candidates.contains(url) {
                candidates.append(url)
            }
        }
        for bonjourURL in discoveredCandidates where !candidates.contains(bonjourURL) {
            candidates.append(bonjourURL)
        }

        // Start passive Bonjour discovery in parallel for LAN Wi-Fi
        startBonjourBrowsing()

        // Race all Tailnet MagicDNS + saved candidates concurrently
        if let hit = await raceProbe(candidates: candidates) {
            setActiveServer(hit.url, info: hit.info, via: hit.via)
            return
        }

        // Brief wait in case Bonjour resolves a local LAN server within 1.5s
        try? await Task.sleep(for: .seconds(1.5))
        let bonjourOnly = discoveredCandidates.filter { !candidates.contains($0) }
        if !bonjourOnly.isEmpty, let hit = await raceProbe(candidates: bonjourOnly) {
            setActiveServer(hit.url, info: hit.info, via: "Bonjour LAN")
            return
        }

        if let existing = activeBaseURL {
            connectionState = .unavailable("Server unreachable (Offline mode using \(existing.host() ?? "cached library"))")
        } else {
            connectionState = .unavailable("Could not find Folio on your Tailnet (folio) or LAN.")
        }
    }

    func connectManually(urlString: String) async -> Bool {
        var trimmed = urlString.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.contains("://") {
            trimmed = "https://" + trimmed
        }
        trimmed = trimmed.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let url = URL(string: trimmed) else {
            connectionState = .unavailable("Invalid server URL")
            return false
        }

        connectionState = .discovering
        if let info = await probe(url: url, timeout: 3.0) {
            setActiveServer(url, info: info, via: "Manual")
            return true
        }
        connectionState = .unavailable("No Folio server responded at \(url.absoluteString)")
        return false
    }

    func handleDeepLink(_ url: URL) async {
        guard url.scheme?.lowercased() == "folio",
              let comps = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let targetStr = comps.queryItems?.first(where: { $0.name == "url" })?.value else {
            return
        }
        _ = await connectManually(urlString: targetStr)
    }

    private func setActiveServer(_ url: URL, info: DiscoverResponse, via: String) {
        activeBaseURL = url
        serverInfo = info
        UserDefaults.standard.set(url.absoluteString, forKey: savedURLKey)
        let label = info.authSource == "tailscale-serve" || info.authSource == "tailscaled-whois"
            ? "Tailscale (\(info.user ?? "Authenticated"))"
            : via
        connectionState = .connected(baseURL: url, via: label)
    }

    private struct ProbeResult: Sendable {
        let url: URL
        let info: DiscoverResponse
        let via: String
    }

    private func raceProbe(candidates: [URL]) async -> ProbeResult? {
        await withTaskGroup(of: ProbeResult?.self) { group in
            for url in candidates {
                group.addTask { [self] in
                    guard let info = await self.probe(url: url, timeout: 1.8) else { return nil }
                    let via = url.host()?.contains("folio") == true ? "Tailscale MagicDNS" : "Saved Server"
                    return ProbeResult(url: url, info: info, via: via)
                }
            }
            for await result in group {
                if let hit = result {
                    group.cancelAll()
                    return hit
                }
            }
            return nil
        }
    }

    nonisolated private func probe(url: URL, timeout: TimeInterval) async -> DiscoverResponse? {
        let endpoint = url.appendingPathComponent("api/discover")
        var req = URLRequest(url: endpoint)
        req.timeoutInterval = timeout
        req.setValue("ios", forHTTPHeaderField: "X-Folio-Client")

        do {
            let (data, response) = try await URLSession.shared.data(for: req)
            guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
                return nil
            }
            let decoded = try JSONDecoder().decode(DiscoverResponse.self, from: data)
            guard decoded.service == "folio" else { return nil }
            return decoded
        } catch {
            return nil
        }
    }

    private func startBonjourBrowsing() {
        guard bonjourBrowser == nil else { return }
        let params = NWParameters.tcp
        let browser = NWBrowser(for: .bonjour(type: "_folio._tcp", domain: "local."), using: params)
        browser.browseResultsChangedHandler = { [weak self] results, _ in
            Task { @MainActor [weak self] in
                guard let self else { return }
                for result in results {
                    self.resolveBonjourEndpoint(result.endpoint)
                }
            }
        }
        browser.start(queue: .global(qos: .utility))
        self.bonjourBrowser = browser
    }

    private func resolveBonjourEndpoint(_ endpoint: NWEndpoint) {
        let conn = NWConnection(to: endpoint, using: .tcp)
        conn.stateUpdateHandler = { [weak self] state in
            if case .ready = state,
               let remote = conn.currentPath?.remoteEndpoint,
               case let .hostPort(host, port) = remote {
                let hostStr: String
                switch host {
                case .ipv4(let addr): hostStr = "\(addr)".split(separator: "%").first.map(String.init) ?? ""
                case .ipv6(let addr): hostStr = "[\("\(addr)".split(separator: "%").first ?? "")]"
                case .name(let name, _): hostStr = name
                @unknown default: hostStr = ""
                }
                if !hostStr.isEmpty, let url = URL(string: "http://\(hostStr):\(port.rawValue)") {
                    Task { @MainActor [weak self] in
                        if let self, !self.discoveredCandidates.contains(url) {
                            self.discoveredCandidates.append(url)
                        }
                    }
                }
                conn.cancel()
            } else if case .failed = state {
                conn.cancel()
            }
        }
        conn.start(queue: .global(qos: .utility))
    }
}
