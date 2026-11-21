import Foundation
import Network
import Observation

@Observable
@MainActor
final class OfflineLibraryStore {
    var books: [Book] = []
    var downloadedBookIDs: Set<Int64> = []
    var activeDownloadIDs: Set<Int64> = []
    var pendingSyncOutbox: [SyncOutboxItem] = []
    var isRefreshing: Bool = false
    var lastErrorMessage: String?

    private let api = FolioAPIClient()
    private let fm = FileManager.default
    private let pathMonitor = NWPathMonitor()

    private var rootDir: URL {
        let appSupport = fm.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        let dir = appSupport.appendingPathComponent("FolioOffline", isDirectory: true)
        try? fm.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    private var booksDir: URL {
        let dir = rootDir.appendingPathComponent("Books", isDirectory: true)
        try? fm.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    private var catalogCacheURL: URL { rootDir.appendingPathComponent("catalog.json") }
    private var outboxCacheURL: URL { rootDir.appendingPathComponent("sync_outbox.json") }

    init() {
        loadCachedState()
        scanDownloadedBooks()
        startNetworkMonitor()
    }

    func localEPUBURL(for bookID: Int64) -> URL {
        booksDir.appendingPathComponent("\(bookID).epub")
    }

    func extractedDirURL(for bookID: Int64) -> URL {
        booksDir.appendingPathComponent("\(bookID)_extracted", isDirectory: true)
    }

    func isDownloaded(_ bookID: Int64) -> Bool {
        downloadedBookIDs.contains(bookID)
    }

    func refreshLibrary(baseURL: URL?) async {
        guard let baseURL else { return }
        isRefreshing = true
        defer { isRefreshing = false }

        await flushSyncOutbox(baseURL: baseURL)

        do {
            let fetched = try await api.fetchBooks(baseURL: baseURL)
            self.books = fetched
            self.lastErrorMessage = nil
            persistCatalogCache()
        } catch {
            self.lastErrorMessage = error.localizedDescription
        }
    }

    func downloadForOffline(_ book: Book, baseURL: URL?) async throws {
        guard let baseURL else { throw FolioAPIError.noServerConfigured }
        activeDownloadIDs.insert(book.id)
        defer { activeDownloadIDs.remove(book.id) }

        let tempFile = try await api.downloadEPUB(baseURL: baseURL, bookID: book.id)
        let destEPUB = localEPUBURL(for: book.id)
        if fm.fileExists(atPath: destEPUB.path) {
            try fm.removeItem(at: destEPUB)
        }
        try fm.moveItem(at: tempFile, to: destEPUB)

        // Pre-extract EPUB contents for instant offline opening
        let extractDir = extractedDirURL(for: book.id)
        _ = try EPUBParser.prepareExtractedEPUB(epubURL: destEPUB, destinationDir: extractDir)

        downloadedBookIDs.insert(book.id)
    }

    func removeDownload(for bookID: Int64) {
        try? fm.removeItem(at: localEPUBURL(for: bookID))
        try? fm.removeItem(at: extractedDirURL(for: bookID))
        downloadedBookIDs.remove(bookID)
    }

    func openLocalEPUB(for book: Book, baseURL: URL?) async throws -> LocalEPUBBook {
        if !isDownloaded(book.id) {
            try await downloadForOffline(book, baseURL: baseURL)
        }
        let epubURL = localEPUBURL(for: book.id)
        let extractDir = extractedDirURL(for: book.id)
        return try EPUBParser.prepareExtractedEPUB(epubURL: epubURL, destinationDir: extractDir)
    }

    func recordProgress(
        book: Book,
        percentage: Double,
        spineIndex: Int,
        pageIndex: Int,
        totalPages: Int,
        paraIndex: Int,
        baseURL: URL?
    ) async {
        let clamped = max(0.001, min(1.0, percentage))
        let docFrag = spineIndex + 1
        let xpath = "/body/DocFragment[\(docFrag)]/body/section[1]/p[\(max(1, paraIndex))]/text().0"

        let compact = CompactPosition(
            pctQ: UInt32((clamped * 1_000_000).rounded()),
            spine: UInt16(max(0, spineIndex)),
            page: UInt16(max(1, pageIndex + 1)),
            pages: UInt16(max(1, totalPages)),
            para: UInt16(max(1, paraIndex)),
            li: nil,
            anchor: nil,
            xpath: xpath
        )

        let localProgress = ReadingProgress(
            id: nil,
            document: book.koMD5Partial,
            bookID: book.id,
            bookTitle: book.title,
            device: FolioAPIClient.currentDeviceName,
            deviceID: FolioAPIClient.currentDeviceID,
            progress: xpath,
            percentage: clamped,
            position: compact,
            metadata: nil,
            timestamp: Int64(Date().timeIntervalSince1970)
        )

        // Update local cached book immediately
        if let idx = books.firstIndex(where: { $0.id == book.id }) {
            books[idx].latestProgress = localProgress
            persistCatalogCache()
        }

        let payload = PutProgressRequest(
            document: book.koMD5Partial,
            bookID: book.id,
            progress: xpath,
            percentage: (clamped * 10000).rounded() / 10000,
            device: FolioAPIClient.currentDeviceName,
            deviceID: FolioAPIClient.currentDeviceID,
            position: compact,
            metadata: SyncMetadata(
                filename: book.fileName,
                title: book.title,
                authors: book.authors,
                language: book.language
            )
        )

        if let baseURL {
            do {
                try await api.pushProgress(baseURL: baseURL, payload: payload)
                return
            } catch {
                // Fall through to offline outbox queue
            }
        }

        enqueueSyncOutbox(payload)
    }

    func syncLatestProgress(for book: Book, baseURL: URL?) async -> ReadingProgress? {
        guard let baseURL else { return book.latestProgress }
        await flushSyncOutbox(baseURL: baseURL)
        if let remote = try? await api.pullProgress(baseURL: baseURL, documentHash: book.koMD5Partial) {
            if let idx = books.firstIndex(where: { $0.id == book.id }) {
                books[idx].latestProgress = remote
                persistCatalogCache()
            }
            return remote
        }
        return book.latestProgress
    }

    func flushSyncOutbox(baseURL: URL) async {
        guard !pendingSyncOutbox.isEmpty else { return }
        var remaining: [SyncOutboxItem] = []
        for item in pendingSyncOutbox {
            do {
                try await api.pushProgress(baseURL: baseURL, payload: item.payload)
            } catch {
                remaining.append(item)
            }
        }
        pendingSyncOutbox = remaining
        persistOutbox()
    }

    private func enqueueSyncOutbox(_ payload: PutProgressRequest) {
        // Deduplicate by document hash, keeping latest position
        pendingSyncOutbox.removeAll { $0.payload.document == payload.document }
        pendingSyncOutbox.append(SyncOutboxItem(id: UUID(), payload: payload, createdAt: Date()))
        persistOutbox()
    }

    private func scanDownloadedBooks() {
        let files = (try? fm.contentsOfDirectory(at: booksDir, includingPropertiesForKeys: nil)) ?? []
        var ids = Set<Int64>()
        for url in files where url.pathExtension.lowercased() == "epub" {
            let stem = url.deletingPathExtension().lastPathComponent
            if let id = Int64(stem) {
                ids.insert(id)
            }
        }
        downloadedBookIDs = ids
    }

    private func loadCachedState() {
        if let data = try? Data(contentsOf: catalogCacheURL),
           let decoded = try? JSONDecoder().decode([Book].self, from: data) {
            self.books = decoded
        }
        if let data = try? Data(contentsOf: outboxCacheURL),
           let decoded = try? JSONDecoder().decode([SyncOutboxItem].self, from: data) {
            self.pendingSyncOutbox = decoded
        }
    }

    private func persistCatalogCache() {
        if let data = try? JSONEncoder().encode(books) {
            try? data.write(to: catalogCacheURL, options: .atomic)
        }
    }

    private func persistOutbox() {
        if let data = try? JSONEncoder().encode(pendingSyncOutbox) {
            try? data.write(to: outboxCacheURL, options: .atomic)
        }
    }

    private func startNetworkMonitor() {
        pathMonitor.pathUpdateHandler = { [weak self] path in
            if path.status == .satisfied {
                Task { @MainActor [weak self] in
                    guard let self else { return }
                    if let saved = UserDefaults.standard.string(forKey: "folio.savedBaseURL"),
                       let url = URL(string: saved) {
                        await self.flushSyncOutbox(baseURL: url)
                    }
                }
            }
        }
        pathMonitor.start(queue: .global(qos: .utility))
    }
}
