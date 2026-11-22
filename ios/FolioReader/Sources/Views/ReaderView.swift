import SwiftUI

struct ReaderView: View {
    @Environment(ServerDiscoveryService.self) private var discovery
    @Environment(OfflineLibraryStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase

    let book: Book

    @State private var localBook: LocalEPUBBook?
    @State private var loadError: String?
    @State private var chapterIndex = 0
    @State private var pageIndex = 0
    @State private var totalPages = 1
    @State private var lastParaIndex = 1
    @State private var initialChapterRatio = 0.0
    @State private var startAtLastPage = false
    @State private var showChrome = true
    @State private var showTOC = false
    @State private var showTypography = false
    @State private var isSyncing = false
    @State private var syncToastMessage: String?
    @State private var remoteConflictProgress: ReadingProgress?
    @State private var command = PaginatedReaderCommand(action: .none)

    @AppStorage("folio.reader.theme") private var themeRaw = ReaderTheme.paper.rawValue
    @AppStorage("folio.reader.fontFamily") private var fontRaw = ReaderFontFamily.iowan.rawValue
    @AppStorage("folio.reader.fontSize") private var fontSize = 21.0
    @AppStorage("folio.reader.margin") private var marginRaw = ReaderMargin.standard.rawValue

    private var theme: ReaderTheme {
        ReaderTheme(rawValue: themeRaw) ?? .paper
    }

    private var fontFamily: ReaderFontFamily {
        ReaderFontFamily(rawValue: fontRaw) ?? .iowan
    }

    private var margin: ReaderMargin {
        ReaderMargin(rawValue: marginRaw) ?? .standard
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
                    margin: margin,
                    command: command,
                    onPageStateChanged: { page, total, para, userInitiated in
                        self.pageIndex = page
                        self.totalPages = total
                        self.lastParaIndex = para
                        self.startAtLastPage = false
                        self.initialChapterRatio = 0
                        // Only push progress after an actual user turn — never on initial chapter open/layout
                        guard userInitiated else { return }
                        let pct = overallPercentage
                        Task {
                            await store.recordProgress(
                                book: book,
                                percentage: pct,
                                spineIndex: chapterIndex,
                                pageIndex: page,
                                totalPages: total,
                                paraIndex: para,
                                baseURL: discovery.activeBaseURL,
                                force: false
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

                if let toast = syncToastMessage {
                    VStack {
                        Spacer()
                        Text(toast)
                            .font(.caption.weight(.medium))
                            .padding(.horizontal, 14)
                            .padding(.vertical, 8)
                            .background(.ultraThinMaterial, in: Capsule())
                            .shadow(color: .black.opacity(0.12), radius: 6, y: 2)
                            .padding(.bottom, showChrome ? 76 : 28)
                    }
                    .transition(.opacity.combined(with: .move(edge: .bottom)))
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
        .onChange(of: scenePhase) { _, newPhase in
            if newPhase == .active, localBook != nil {
                Task { await syncProgress(userTriggered: false) }
            }
        }
        .confirmationDialog(
            syncConflictTitle,
            isPresented: Binding(
                get: { remoteConflictProgress != nil },
                set: { if !$0 { remoteConflictProgress = nil } }
            ),
            titleVisibility: .visible
        ) {
            if let remote = remoteConflictProgress {
                let remotePct = Int((remote.percentage * 100).rounded())
                let currentPct = Int((overallPercentage * 100).rounded())

                Button("Jump to \(remotePct)%") {
                    jump(to: remote)
                    remoteConflictProgress = nil
                }

                Button("Override server with \(currentPct)%", role: .destructive) {
                    remoteConflictProgress = nil
                    Task { await pushCurrentPosition(force: true) }
                }

                Button("Stay here", role: .cancel) {
                    remoteConflictProgress = nil
                }
            }
        } message: {
            if let remote = remoteConflictProgress {
                let device = remote.device.isEmpty ? "another device" : remote.device
                Text("Synced position on \(device) is further along than your current page.")
            }
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

    private var syncConflictTitle: String {
        guard let remote = remoteConflictProgress else { return "Sync Progress" }
        let remotePct = Int((remote.percentage * 100).rounded())
        return "Furthest Progress: \(remotePct)%"
    }

    @ViewBuilder
    private func chromeOverlay(localBook: LocalEPUBBook, chapter: LocalEPUBChapter) -> some View {
        VStack {
            // Top bar
            HStack(spacing: 4) {
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
                    Task { await syncProgress(userTriggered: true) }
                } label: {
                    if isSyncing {
                        ProgressView()
                            .controlSize(.small)
                            .padding(8)
                    } else {
                        Image(systemName: "arrow.triangle.2.circlepath")
                            .padding(8)
                    }
                }
                .disabled(isSyncing)

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
                    Slider(value: $fontSize, in: 14...32, step: 1)
                }

                Section("Margins") {
                    Picker("Margins", selection: $marginRaw) {
                        ForEach(ReaderMargin.allCases) { m in
                            Text(m.label).tag(m.rawValue)
                        }
                    }
                    .pickerStyle(.segmented)
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

            if let lp = remoteProgress ?? book.latestProgress {
                let (spine, ratio) = resolvePosition(for: lp, in: opened)
                self.chapterIndex = spine
                self.initialChapterRatio = ratio
            } else {
                self.chapterIndex = 0
                self.initialChapterRatio = 0
            }

            self.localBook = opened
        } catch {
            self.loadError = error.localizedDescription
        }
    }

    private func resolvePosition(for progress: ReadingProgress, in opened: LocalEPUBBook) -> (Int, Double) {
        guard !opened.chapters.isEmpty else { return (0, 0) }
        let pct = max(0, min(1, progress.percentage))

        // Prefer compact spine index only when it agrees with overall percentage;
        // otherwise fall back to percentage lookup (handles CrossPoint/KOReader spine indexing differences).
        var targetSpine: Int?
        if let pos = progress.position {
            let candidate = Int(pos.spine)
            if candidate >= 0 && candidate < opened.chapters.count {
                let ch = opened.chapters[candidate]
                if pct >= ch.startPct - 0.02 && pct <= ch.endPct + 0.02 {
                    targetSpine = candidate
                }
            }
        }

        if targetSpine == nil {
            targetSpine = opened.chapters.firstIndex(where: { pct >= $0.startPct && pct <= $0.endPct })
                ?? (pct >= 0.99 ? opened.chapters.count - 1 : 0)
        }

        let spine = targetSpine ?? 0
        let ch = opened.chapters[spine]
        let ratio: Double
        if ch.endPct > ch.startPct {
            ratio = max(0, min(1, (pct - ch.startPct) / (ch.endPct - ch.startPct)))
        } else {
            ratio = 0
        }
        return (spine, ratio)
    }

    private func jump(to progress: ReadingProgress) {
        guard let localBook else { return }
        let (targetSpine, targetRatio) = resolvePosition(for: progress, in: localBook)
        startAtLastPage = false
        if targetSpine == chapterIndex {
            command = PaginatedReaderCommand(action: .goToRatio(targetRatio))
        } else {
            initialChapterRatio = targetRatio
            chapterIndex = targetSpine
        }
        showToast("Jumped to \(Int((progress.percentage * 100).rounded()))%")
    }

    private func syncProgress(userTriggered: Bool) async {
        guard discovery.activeBaseURL != nil else {
            if userTriggered { showToast("Offline — will sync when connected") }
            return
        }
        isSyncing = true
        defer { isSyncing = false }

        let latest = await store.syncLatestProgress(for: book, baseURL: discovery.activeBaseURL)
        let currentPct = overallPercentage

        if let latest, latest.percentage > currentPct + 0.005 {
            remoteConflictProgress = latest
        } else if userTriggered {
            await pushCurrentPosition(force: false)
        }
    }

    private func pushCurrentPosition(force: Bool) async {
        let pct = overallPercentage
        await store.recordProgress(
            book: book,
            percentage: pct,
            spineIndex: chapterIndex,
            pageIndex: pageIndex,
            totalPages: totalPages,
            paraIndex: lastParaIndex,
            baseURL: discovery.activeBaseURL,
            force: force
        )
        showToast("Synced \(Int((pct * 100).rounded()))%")
    }

    private func showToast(_ message: String) {
        withAnimation(.easeInOut(duration: 0.2)) {
            syncToastMessage = message
        }
        Task {
            try? await Task.sleep(for: .seconds(2.2))
            withAnimation(.easeInOut(duration: 0.25)) {
                if syncToastMessage == message {
                    syncToastMessage = nil
                }
            }
        }
    }
}
