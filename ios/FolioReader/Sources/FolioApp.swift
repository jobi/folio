import SwiftUI

@main
struct FolioApp: App {
    @State private var discovery = ServerDiscoveryService()
    @State private var offlineStore = OfflineLibraryStore()

    var body: some Scene {
        WindowGroup {
            LibraryView()
                .environment(discovery)
                .environment(offlineStore)
                .task {
                    await discovery.discoverAutomatically()
                    await offlineStore.refreshLibrary(baseURL: discovery.activeBaseURL)
                }
                .onOpenURL { url in
                    Task {
                        await discovery.handleDeepLink(url)
                        await offlineStore.refreshLibrary(baseURL: discovery.activeBaseURL)
                    }
                }
        }
    }
}
