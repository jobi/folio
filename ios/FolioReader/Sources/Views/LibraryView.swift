import SwiftUI

struct LibraryView: View {
    @Environment(ServerDiscoveryService.self) private var discovery
    @Environment(OfflineLibraryStore.self) private var store

    @State private var searchText = ""
    @State private var selectedFilter: LibraryFilter = .all
    @State private var selectedBook: Book?
    @State private var activeReaderTarget: ReaderLaunchTarget?
    @State private var showSettings = false

    enum LibraryFilter: String, CaseIterable, Identifiable {
        case all = "All"
        case offline = "Downloaded"
        case reading = "Reading"
        case unread = "Unread"

        var id: String { rawValue }
    }

    private var filteredBooks: [Book] {
        store.books.filter { book in
            let matchesSearch = searchText.isEmpty ||
                book.title.localizedCaseInsensitiveContains(searchText) ||
                book.authors.localizedCaseInsensitiveContains(searchText) ||
                (book.series?.localizedCaseInsensitiveContains(searchText) ?? false)

            guard matchesSearch else { return false }

            switch selectedFilter {
            case .all: return true
            case .offline: return store.isDownloaded(book.id)
            case .reading: return book.readingStatus == "reading"
            case .unread: return book.readingStatus == "unread" || book.readingStatus.isEmpty
            }
        }
    }

    private let columns = [
        GridItem(.adaptive(minimum: 150, maximum: 200), spacing: 18)
    ]

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    connectionBanner

                    Picker("Filter", selection: $selectedFilter) {
                        ForEach(LibraryFilter.allCases) { filter in
                            Text(filter.rawValue).tag(filter)
                        }
                    }
                    .pickerStyle(.segmented)
                    .padding(.horizontal)

                    if filteredBooks.isEmpty {
                        emptyStateView
                    } else {
                        LazyVGrid(columns: columns, spacing: 22) {
                            ForEach(filteredBooks) { book in
                                BookGridCard(
                                    book: book,
                                    baseURL: discovery.activeBaseURL,
                                    isDownloaded: store.isDownloaded(book.id),
                                    isDownloading: store.activeDownloadIDs.contains(book.id)
                                )
                                .onTapGesture {
                                    selectedBook = book
                                }
                            }
                        }
                        .padding(.horizontal)
                        .padding(.bottom, 24)
                    }
                }
                .padding(.top, 8)
            }
            .navigationTitle("Folio")
            .searchable(text: $searchText, prompt: "Title, author, or series")
            .refreshable {
                if discovery.activeBaseURL == nil {
                    await discovery.discoverAutomatically()
                }
                await store.refreshLibrary(baseURL: discovery.activeBaseURL)
            }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        showSettings = true
                    } label: {
                        Image(systemName: "gearshape")
                    }
                }
            }
            .sheet(item: $selectedBook) { book in
                BookDetailSheet(book: book) { targetBook in
                    selectedBook = nil
                    activeReaderTarget = ReaderLaunchTarget(book: targetBook)
                }
            }
            .sheet(isPresented: $showSettings) {
                SettingsView()
            }
            .fullScreenCover(item: $activeReaderTarget) { target in
                ReaderView(book: target.book)
            }
        }
    }

    @ViewBuilder
    private var connectionBanner: some View {
        HStack(spacing: 10) {
            switch discovery.connectionState {
            case .idle, .discovering:
                ProgressView()
                    .controlSize(.small)
                Text("Searching Tailnet (folio) & local Wi-Fi...")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            case .connected(_, let via):
                Circle()
                    .fill(.green)
                    .frame(width: 8, height: 8)
                Text("Connected via \(via)")
                    .font(.caption.weight(.medium))
                Spacer()
                if !store.pendingSyncOutbox.isEmpty {
                    Text("\(store.pendingSyncOutbox.count) syncing")
                        .font(.caption2)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(.orange.opacity(0.15), in: Capsule())
                        .foregroundStyle(.orange)
                }
            case .unavailable(let message):
                Circle()
                    .fill(.orange)
                    .frame(width: 8, height: 8)
                Text(message)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                Spacer()
                Button("Retry") {
                    Task {
                        await discovery.discoverAutomatically()
                        await store.refreshLibrary(baseURL: discovery.activeBaseURL)
                    }
                }
                .font(.caption.weight(.semibold))
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    private var emptyStateView: some View {
        ContentUnavailableView(
            store.books.isEmpty ? "No Books Yet" : "No Matching Books",
            systemImage: "books.vertical",
            description: Text(
                store.books.isEmpty
                ? "Connect to your Folio server on Tailscale or pull down to refresh."
                : "Try clearing your search or switching filters."
            )
        )
        .padding(.top, 48)
    }
}

struct ReaderLaunchTarget: Identifiable {
    let id = UUID()
    let book: Book
}

struct BookGridCard: View {
    let book: Book
    let baseURL: URL?
    let isDownloaded: Bool
    let isDownloading: Bool

    private static let api = FolioAPIClient()

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ZStack(alignment: .bottomLeading) {
                CachedCoverImage(
                    book: book,
                    url: baseURL.flatMap { Self.api.coverURL(baseURL: $0, book: book) }
                ) {
                    coverPlaceholder
                }
                .frame(height: 225)
                .frame(maxWidth: .infinity)
                .clipShape(RoundedRectangle(cornerRadius: 8))
                .shadow(color: .black.opacity(0.14), radius: 6, x: 0, y: 3)

                if book.progressPercentage > 0 {
                    GeometryReader { geo in
                        ZStack(alignment: .leading) {
                            Rectangle().fill(.black.opacity(0.35))
                            Rectangle()
                                .fill(Color(red: 0.78, green: 0.29, blue: 0.19))
                                .frame(width: geo.size.width * min(1, book.progressPercentage))
                        }
                    }
                    .frame(height: 4)
                    .clipShape(RoundedRectangle(cornerRadius: 2))
                }
            }
            .overlay(alignment: .topTrailing) {
                if isDownloading {
                    ProgressView()
                        .padding(6)
                        .background(.ultraThinMaterial, in: Circle())
                        .padding(6)
                } else if isDownloaded {
                    Image(systemName: "arrow.down.circle.fill")
                        .font(.system(size: 18))
                        .foregroundStyle(.white, Color(red: 0.78, green: 0.29, blue: 0.19))
                        .padding(6)
                }
            }

            if let series = book.series, !series.isEmpty {
                Text(series.uppercased())
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(Color(red: 0.78, green: 0.29, blue: 0.19))
                    .lineLimit(1)
            }

            Text(book.title)
                .font(.subheadline.weight(.semibold))
                .lineLimit(2)

            Text(book.displayAuthors)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
    }

    private var coverPlaceholder: some View {
        ZStack {
            LinearGradient(
                colors: [Color(red: 0.18, green: 0.16, blue: 0.15), Color(red: 0.28, green: 0.24, blue: 0.21)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
            VStack(spacing: 8) {
                Text(book.title)
                    .font(.headline.bold())
                    .foregroundStyle(.white)
                    .multilineTextAlignment(.center)
                    .lineLimit(4)
                Text(book.displayAuthors)
                    .font(.caption)
                    .foregroundStyle(.white.opacity(0.75))
                    .lineLimit(2)
            }
            .padding(14)
        }
    }
}
