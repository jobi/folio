import SwiftUI

struct BookDetailSheet: View {
    @Environment(ServerDiscoveryService.self) private var discovery
    @Environment(OfflineLibraryStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    let book: Book
    let onOpenReader: (Book) -> Void

    @State private var isDownloading = false
    @State private var errorMessage: String?

    private let api = FolioAPIClient()

    private var isDownloaded: Bool {
        store.isDownloaded(book.id)
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    HStack(alignment: .top, spacing: 16) {
                        Group {
                            if let baseURL = discovery.activeBaseURL,
                               let url = api.coverURL(baseURL: baseURL, book: book) {
                                AsyncImage(url: url) { phase in
                                    if case .success(let img) = phase {
                                        img.resizable().scaledToFill()
                                    } else {
                                        RoundedRectangle(cornerRadius: 8).fill(.secondary.opacity(0.2))
                                    }
                                }
                            } else {
                                RoundedRectangle(cornerRadius: 8).fill(.secondary.opacity(0.2))
                            }
                        }
                        .frame(width: 115, height: 170)
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                        .shadow(radius: 5)

                        VStack(alignment: .leading, spacing: 6) {
                            if let series = book.series, !series.isEmpty {
                                Text(series.uppercased())
                                    .font(.caption2.weight(.bold))
                                    .foregroundStyle(Color(red: 0.78, green: 0.29, blue: 0.19))
                            }
                            Text(book.title)
                                .font(.title3.weight(.semibold))
                            Text(book.displayAuthors)
                                .font(.subheadline)
                                .foregroundStyle(.secondary)

                            Spacer()

                            HStack(spacing: 12) {
                                Label(book.formattedFileSize, systemImage: "doc")
                                if book.chapterCount > 0 {
                                    Label("\(book.chapterCount) ch", systemImage: "book.pages")
                                }
                            }
                            .font(.caption)
                            .foregroundStyle(.secondary)

                            if book.progressPercentage > 0 {
                                ProgressView(value: book.progressPercentage)
                                    .tint(Color(red: 0.78, green: 0.29, blue: 0.19))
                                Text("\(Int((book.progressPercentage * 100).rounded()))% completed")
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }

                    VStack(spacing: 10) {
                        Button {
                            onOpenReader(book)
                        } label: {
                            Label(
                                book.progressPercentage > 0 ? "Continue Reading" : "Read Now",
                                systemImage: "book.fill"
                            )
                            .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(Color(red: 0.78, green: 0.29, blue: 0.19))
                        .controlSize(.large)

                        if isDownloaded {
                            Button(role: .destructive) {
                                store.removeDownload(for: book.id)
                            } label: {
                                Label("Remove Offline Copy", systemImage: "trash")
                                    .frame(maxWidth: .infinity)
                            }
                            .buttonStyle(.bordered)
                        } else {
                            Button {
                                Task {
                                    isDownloading = true
                                    errorMessage = nil
                                    do {
                                        try await store.downloadForOffline(book, baseURL: discovery.activeBaseURL)
                                    } catch {
                                        errorMessage = error.localizedDescription
                                    }
                                    isDownloading = false
                                }
                            } label: {
                                if isDownloading {
                                    ProgressView().frame(maxWidth: .infinity)
                                } else {
                                    Label("Download for Offline Reading", systemImage: "arrow.down.circle")
                                        .frame(maxWidth: .infinity)
                                }
                            }
                            .buttonStyle(.bordered)
                            .disabled(isDownloading || discovery.activeBaseURL == nil)
                        }
                    }

                    if let errorMessage {
                        Text(errorMessage)
                            .font(.caption)
                            .foregroundStyle(.red)
                    }

                    if let desc = book.description, !desc.isEmpty {
                        VStack(alignment: .leading, spacing: 6) {
                            Text("Synopsis")
                                .font(.headline)
                            Text(desc.replacingOccurrences(of: "<[^>]+>", with: "", options: .regularExpression))
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                .padding()
            }
            .navigationTitle("Book Details")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
        }
    }
}
