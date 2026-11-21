import SwiftUI

struct ReaderView: View {
    @Environment(ServerDiscoveryService.self) private var discovery
    @Environment(OfflineLibraryStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    let book: Book

    @State private var localBook: LocalEPUBBook?
    @State private var loadError: String?
    @State private var chapterIndex = 0
    @State private var pageIndex = 0
    @State private var totalPages = 1
    @State private var initialChapterRatio = 0.0
    @State private var startAtLastPage = false
    @State private var showChrome = true
    @State private var showTOC = false
    @State private var showTypography = false
    @State private var command = PaginatedReaderCommand(action: .none)

    @AppStorage("folio.reader.theme") private var themeRaw = ReaderTheme.paper.rawValue
    @AppStorage("folio.reader.fontFamily") private var fontRaw = ReaderFontFamily.serif.rawValue
    @AppStorage("folio.reader.fontSize") private var fontSize = 19.0

    private var theme: ReaderTheme {
        ReaderTheme(rawValue: themeRaw) ?? .paper
    }

    private var fontFamily: ReaderFontFamily {
        ReaderFontFamily(rawValue: fontRaw) ?? .serif
    }

    private var overallPercentage: Double {
        guard let localBook, !localBook.chapters.isEmpty else { return 0 }
        let ch = localBook.chapters[min(chapterIndex, localBook.chapters.count - 1)]
        let ratio = totalPages > 1 ? Double(pageIndex) / Double(totalPages - 1) : 0
        return ch.startPct + ratio * (ch.endPct - ch.startPct)
    }

    var body: some View {
        ZStack {
            theme.backgroundColor.ignoresSafeArea()

            if let localBook {
                let chapter = localBook.chapters[min(chapterIndex, localBook.chapters.count - 1)]

                PaginatedReaderWebView(
                    chapterURL: chapter.fileURL,
                    readAccessRootURL: localBook.extractedRootURL,
                    initialScrollRatio: initialChapterRatio,
                    startAtLastPage: startAtLastPage,
                    theme: theme,
                    fontFamily: fontFamily,
                    fontSize: fontSize,
                    command: command,
                    onPageStateChanged: { page, total, para in
                        self.pageIndex = page
                        self.totalPages = total
                        self.startAtLastPage = false
                        self.initialChapterRatio = 0
                        Task {
                            await store.recordProgress(
                                book: book,
                                percentage: overallPercentage,
                                spineIndex: chapterIndex,
                                pageIndex: page,
                                totalPages: total,
                                paraIndex: para,
                                baseURL: discovery.activeBaseURL
                            )
                        }
                    },
                    onRequestNextChapter: {
                        if chapterIndex < localBook.chapters.count - 1 {
                            startAtLastPage = false
                            initialChapterRatio = 0
                            chapterIndex += 1
                        }
                    },
                    onRequestPrevChapter: {
                        if chapterIndex > 0 {
                            startAtLastPage = true
                            initialChapterRatio = 1
                            chapterIndex -= 1
                        }
                    },
                    onToggleChrome: {
                        withAnimation(.easeInOut(duration: 0.2)) {
                            showChrome.toggle()
                        }
                    }
                )
                .ignoresSafeArea(edges: .horizontal)

                if showChrome {
                    chromeOverlay(localBook: localBook, chapter: chapter)
                }
            } else if let loadError {
                VStack(spacing: 14) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.largeTitle)
                        .foregroundStyle(.orange)
                    Text(loadError)
                        .multilineTextAlignment(.center)
                        .foregroundStyle(theme.foregroundColor)
                    Button("Close") { dismiss() }
                        .buttonStyle(.borderedProminent)
                }
                .padding()
            } else {
                VStack(spacing: 12) {
                    ProgressView()
                    Text("Preparing offline EPUB...")
                        .font(.subheadline)
                        .foregroundStyle(theme.foregroundColor.opacity(0.7))
                }
            }
        }
        .statusBarHidden(!showChrome)
        .task {
            await initializeReader()
        }
        .sheet(isPresented: $showTOC) {
            if let localBook {
                NavigationStack {
                    List(localBook.tocItems) { item in
                        Button {
                            startAtLastPage = false
                            initialChapterRatio = 0
                            chapterIndex = item.spineIndex
                            showTOC = false
                        } label: {
                            HStack {
                                Text(item.title)
                                    .foregroundStyle(.primary)
                                Spacer()
                                if item.spineIndex == chapterIndex {
                                    Image(systemName: "checkmark")
                                        .foregroundStyle(Color(red: 0.78, green: 0.29, blue: 0.19))
                                }
                            }
                        }
                    }
                    .navigationTitle("Contents")
                    .navigationBarTitleDisplayMode(.inline)
                    .toolbar {
                        ToolbarItem(placement: .topBarTrailing) {
                            Button("Close") { showTOC = false }
                        }
                    }
                }
            }
        }
        .sheet(isPresented: $showTypography) {
            typographySheet
                .presentationDetents([.medium])
        }
    }

    @ViewBuilder
    private func chromeOverlay(localBook: LocalEPUBBook, chapter: LocalEPUBChapter) -> some View {
        VStack {
            // Top bar
            HStack {
                Button {
                    dismiss()
                } label: {
                    Image(systemName: "xmark")
                        .font(.body.weight(.semibold))
                        .padding(8)
                }

                VStack(spacing: 2) {
                    Text(book.title)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Text(chapter.title)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                .frame(maxWidth: .infinity)

                Button {
                    showTOC = true
                } label: {
                    Image(systemName: "list.bullet")
                        .padding(8)
                }

                Button {
                    showTypography = true
                } label: {
                    Text("Aa")
                        .font(.subheadline.weight(.semibold))
                        .padding(8)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(.ultraThinMaterial)

            Spacer()

            // Bottom progress scrubber
            VStack(spacing: 6) {
                ProgressView(value: overallPercentage)
                    .tint(Color(red: 0.78, green: 0.29, blue: 0.19))

                HStack {
                    Text("Page \(pageIndex + 1) of \(totalPages)")
                        .font(.caption.monospacedDigit().weight(.medium))
                    Spacer()
                    Text("Ch. \(chapterIndex + 1)/\(localBook.chapters.count) • \(Int((overallPercentage * 100).rounded()))%")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(.ultraThinMaterial)
        }
        .transition(.opacity)
    }

    private var typographySheet: some View {
        NavigationStack {
            Form {
                Section("Surface Theme") {
                    Picker("Theme", selection: $themeRaw) {
                        ForEach(ReaderTheme.allCases) { t in
                            Text(t.label).tag(t.rawValue)
                        }
                    }
                    .pickerStyle(.segmented)
                }

                Section("Typeface") {
                    Picker("Font", selection: $fontRaw) {
                        ForEach(ReaderFontFamily.allCases) { f in
                            Text(f.displayName).tag(f.rawValue)
                        }
                    }
                }

                Section("Font Size (\(Int(fontSize)) pt)") {
                    Slider(value: $fontSize, in: 14...28, step: 1)
                }
            }
            .navigationTitle("Typography")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { showTypography = false }
                }
            }
        }
    }

    private func initializeReader() async {
        do {
            let opened = try await store.openLocalEPUB(for: book, baseURL: discovery.activeBaseURL)
            let remoteProgress = await store.syncLatestProgress(for: book, baseURL: discovery.activeBaseURL)

            var targetSpine = 0
            var targetRatio = 0.0

            if let lp = remoteProgress ?? book.latestProgress {
                if let pos = lp.position {
                    targetSpine = min(max(0, Int(pos.spine)), opened.chapters.count - 1)
                } else if let idx = opened.chapters.firstIndex(where: { lp.percentage >= $0.startPct && lp.percentage <= $0.endPct }) {
                    targetSpine = idx
                }
                let ch = opened.chapters[targetSpine]
                if ch.endPct > ch.startPct {
                    targetRatio = max(0, min(1, (lp.percentage - ch.startPct) / (ch.endPct - ch.startPct)))
                }
            }

            self.chapterIndex = targetSpine
            self.initialChapterRatio = targetRatio
            self.localBook = opened
        } catch {
            self.loadError = error.localizedDescription
        }
    }
}
