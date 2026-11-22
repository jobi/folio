import SwiftUI

struct SettingsView: View {
    @Environment(ServerDiscoveryService.self) private var discovery
    @Environment(OfflineLibraryStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    @State private var manualURL = ""
    @State private var isConnecting = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Server Connection") {
                    LabeledContent("Status") {
                        switch discovery.connectionState {
                        case .connected(let url, let via):
                            Text("\(via) (\(url.host() ?? ""))")
                                .foregroundStyle(.green)
                        case .discovering:
                            Text("Searching...")
                        case .unavailable(let reason):
                            Text(reason)
                                .foregroundStyle(.orange)
                        case .idle:
                            Text("Idle")
                        }
                    }

                    if let info = discovery.serverInfo {
                        if let user = info.user, !user.isEmpty {
                            LabeledContent("Tailscale User", value: user)
                        }
                        if let tailnet = info.tailnet, !tailnet.isEmpty {
                            LabeledContent("Tailnet", value: tailnet)
                        }
                        LabeledContent("Sync Mode", value: info.authSource)
                    }

                    Button("Re-Scan Tailnet & Local Wi-Fi") {
                        Task {
                            await discovery.discoverAutomatically()
                            await store.refreshLibrary(baseURL: discovery.activeBaseURL)
                        }
                    }
                }

                Section("Custom Server URL (Optional)") {
                    TextField("https://folio or http://192.168.1.x:8080", text: $manualURL)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()

                    Button {
                        Task {
                            isConnecting = true
                            let ok = await discovery.connectManually(urlString: manualURL)
                            if ok {
                                await store.refreshLibrary(baseURL: discovery.activeBaseURL)
                            }
                            isConnecting = false
                        }
                    } label: {
                        if isConnecting {
                            ProgressView()
                        } else {
                            Text("Connect")
                        }
                    }
                    .disabled(manualURL.trimmingCharacters(in: .whitespaces).isEmpty || isConnecting)
                }

                Section("Offline Storage & Sync Queue") {
                    LabeledContent("Downloaded Books", value: "\(store.downloadedBookIDs.count)")
                    LabeledContent("Pending Sync Events", value: "\(store.pendingSyncOutbox.count)")
                    if !store.pendingSyncOutbox.isEmpty, let baseURL = discovery.activeBaseURL {
                        Button("Flush Sync Queue Now") {
                            Task {
                                await store.flushSyncOutbox(baseURL: baseURL)
                            }
                        }
                    }
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .onAppear {
                manualURL = discovery.activeBaseURL?.absoluteString ?? ""
            }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
        }
    }
}
