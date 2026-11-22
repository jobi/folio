import SwiftUI
import WebKit

struct PaginatedReaderCommand: Equatable {
    enum Action: Equatable {
        case none
        case nextPage
        case prevPage
        case goToPage(Int)
        case goToRatio(Double)
    }
    let id = UUID()
    let action: Action
}

/// Plain UIView host with a fixed viewport-sized WKWebView underneath and a transparent,
/// vanilla UIKit UIScrollView (`isPagingEnabled = true`) on top driving horizontal paging 1:1.
final class PagingReaderHostView: UIView {
    let webView: WKWebView
    let pagingScrollView: UIScrollView
    var onBoundsSizeChanged: ((CGSize) -> Void)?
    private var lastReportedSize: CGSize = .zero

    init(webView: WKWebView) {
        self.webView = webView
        self.pagingScrollView = UIScrollView(frame: .zero)
        super.init(frame: .zero)

        backgroundColor = .clear
        clipsToBounds = true

        pagingScrollView.isPagingEnabled = true
        pagingScrollView.bounces = true
        pagingScrollView.alwaysBounceHorizontal = true
        pagingScrollView.alwaysBounceVertical = false
        pagingScrollView.showsHorizontalScrollIndicator = false
        pagingScrollView.showsVerticalScrollIndicator = false
        pagingScrollView.isDirectionalLockEnabled = true
        pagingScrollView.contentInsetAdjustmentBehavior = .never
        pagingScrollView.backgroundColor = .clear

        addSubview(webView)
        addSubview(pagingScrollView)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        let size = bounds.size
        guard size.width > 0, size.height > 0 else { return }

        if webView.bounds.size != size {
            webView.transform = .identity
            webView.frame = bounds
        }
        if pagingScrollView.frame != bounds {
            pagingScrollView.frame = bounds
        }
        if abs(size.width - lastReportedSize.width) > 0.5 || abs(size.height - lastReportedSize.height) > 0.5 {
            lastReportedSize = size
            onBoundsSizeChanged?(size)
        }
    }
}

struct PaginatedReaderWebView: UIViewRepresentable {
    let chapterURL: URL
    let readAccessRootURL: URL
    let initialScrollRatio: Double
    let startAtLastPage: Bool
    let theme: ReaderTheme
    let fontFamily: ReaderFontFamily
    let fontSize: Double
    let margin: ReaderMargin
    let command: PaginatedReaderCommand

    var onPageStateChanged: (Int, Int, Int, Bool) -> Void // (pageIndex, totalPages, estimatedPara, userInitiated)
    var onRequestNextChapter: () -> Void
    var onRequestPrevChapter: () -> Void
    var onToggleChrome: () -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
    }

    func makeUIView(context: Context) -> PagingReaderHostView {
        let config = WKWebViewConfiguration()
        let contentController = WKUserContentController()
        contentController.add(context.coordinator, name: "folioReader")

        // Inject mobile viewport BEFORE didFinish so WebKit locks 1:1 point scale from the start
        let viewportScript = WKUserScript(
            source: """
            (function() {
                var head = document.head || document.getElementsByTagName('head')[0] || document.documentElement;
                var meta = document.querySelector('meta[name="viewport"]');
                if (!meta) {
                    meta = document.createElement('meta');
                    meta.setAttribute('name', 'viewport');
                    head.appendChild(meta);
                }
                meta.setAttribute('content', 'width=device-width, initial-scale=1.0, minimum-scale=1.0, maximum-scale=1.0, user-scalable=no');
            })();
            """,
            injectionTime: .atDocumentEnd,
            forMainFrameOnly: true
        )
        contentController.addUserScript(viewportScript)
        config.userContentController = contentController

        let webView = WKWebView(frame: .zero, configuration: config)
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.allowsBackForwardNavigationGestures = false
        webView.navigationDelegate = context.coordinator
        webView.isUserInteractionEnabled = false

        webView.scrollView.isScrollEnabled = false
        webView.scrollView.bounces = false
        webView.scrollView.showsHorizontalScrollIndicator = false
        webView.scrollView.showsVerticalScrollIndicator = false
        webView.scrollView.minimumZoomScale = 1.0
        webView.scrollView.maximumZoomScale = 1.0
        webView.scrollView.contentInsetAdjustmentBehavior = .never

        let host = PagingReaderHostView(webView: webView)
        host.pagingScrollView.delegate = context.coordinator
        host.onBoundsSizeChanged = { [weak coordinator = context.coordinator] size in
            coordinator?.handleBoundsChanged(size)
        }

        // Native tap zones: left 25% prev page, right 25% next page, center 50% toggle chrome
        let tap = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.handleTap(_:)))
        host.pagingScrollView.addGestureRecognizer(tap)

        context.coordinator.hostView = host
        context.coordinator.webView = webView
        loadChapter(into: webView, coordinator: context.coordinator)
        return host
    }

    func updateUIView(_ host: PagingReaderHostView, context: Context) {
        context.coordinator.parent = self
        let webView = host.webView

        let currentStyle = StyleSnapshot(
            theme: theme,
            fontFamily: fontFamily,
            fontSize: Int(fontSize),
            marginPx: margin.horizontalPx
        )

        if context.coordinator.loadedChapterURL != chapterURL {
            context.coordinator.lastAppliedStyle = currentStyle
            loadChapter(into: webView, coordinator: context.coordinator)
        } else if context.coordinator.lastAppliedStyle != currentStyle {
            context.coordinator.lastAppliedStyle = currentStyle
            context.coordinator.applyStyle(preserveRatio: context.coordinator.currentRatio)
        }

        if context.coordinator.lastCommandID != command.id {
            context.coordinator.lastCommandID = command.id
            switch command.action {
            case .nextPage:
                context.coordinator.stepPage(by: 1)
            case .prevPage:
                context.coordinator.stepPage(by: -1)
            case .goToPage(let p):
                context.coordinator.scrollToPage(p, animated: true, userInitiated: true)
            case .goToRatio(let r):
                let target = context.coordinator.totalPages > 1
                    ? Int(round(max(0, min(1, r)) * Double(context.coordinator.totalPages - 1)))
                    : 0
                context.coordinator.scrollToPage(target, animated: true, userInitiated: false)
            case .none:
                break
            }
        }
    }

    private func loadChapter(into webView: WKWebView, coordinator: Coordinator) {
        coordinator.loadedChapterURL = chapterURL
        coordinator.currentPage = 0
        coordinator.totalPages = 1
        coordinator.isChapterReady = false
        coordinator.hasFinishedNavigation = false
        coordinator.hostView?.pagingScrollView.setContentOffset(.zero, animated: false)
        webView.transform = .identity
        webView.scrollView.setContentOffset(.zero, animated: false)
        webView.loadFileURL(chapterURL, allowingReadAccessTo: readAccessRootURL)
    }

    struct StyleSnapshot: Equatable {
        let theme: ReaderTheme
        let fontFamily: ReaderFontFamily
        let fontSize: Int
        let marginPx: Int
    }

    final class Coordinator: NSObject, WKNavigationDelegate, WKScriptMessageHandler, UIScrollViewDelegate {
        var parent: PaginatedReaderWebView
        weak var hostView: PagingReaderHostView?
        weak var webView: WKWebView?
        var loadedChapterURL: URL?
        var lastCommandID: UUID?
        var lastAppliedStyle: StyleSnapshot?

        var currentPage: Int = 0
        var totalPages: Int = 1
        var totalParas: Int = 1
        var isChapterReady: Bool = false
        var hasFinishedNavigation: Bool = false
        private var pendingUserInitiatedScroll: Bool = false

        var currentRatio: Double {
            totalPages > 1 ? Double(currentPage) / Double(totalPages - 1) : 0
        }

        init(_ parent: PaginatedReaderWebView) {
            self.parent = parent
        }

        func handleBoundsChanged(_ size: CGSize) {
            guard hasFinishedNavigation, size.width > 0, size.height > 0 else { return }
            if isChapterReady {
                applyStyle(preserveRatio: currentRatio)
            } else {
                injectInitialPagination()
            }
        }

        func applyStyle(preserveRatio: Double) {
            guard let webView, let hostView else { return }
            let w = max(1, Int(round(hostView.bounds.width)))
            let h = max(1, Int(round(hostView.bounds.height)))
            let js = """
            window.__folioApplyStyle && window.__folioApplyStyle({
                bg: '\(parent.theme.cssBackgroundHex)',
                fg: '\(parent.theme.cssTextHex)',
                fontFamily: "\(parent.fontFamily.rawValue)",
                fontSize: \(Int(parent.fontSize)),
                marginPx: \(parent.margin.horizontalPx),
                viewW: \(w),
                viewH: \(h),
                preserveRatio: \(preserveRatio)
            });
            """
            webView.evaluateJavaScript(js, completionHandler: nil)
        }

        // MARK: - Native Tap & UIScrollView Paging

        @objc func handleTap(_ gesture: UITapGestureRecognizer) {
            guard let hostView else { return }
            let screenX = gesture.location(in: hostView).x
            let width = max(1, hostView.bounds.width)
            if screenX < width * 0.25 {
                stepPage(by: -1)
            } else if screenX > width * 0.75 {
                stepPage(by: 1)
            } else {
                parent.onToggleChrome()
            }
        }

        func stepPage(by delta: Int) {
            guard isChapterReady else { return }
            let target = currentPage + delta
            if target < 0 {
                parent.onRequestPrevChapter()
            } else if target >= totalPages {
                parent.onRequestNextChapter()
            } else {
                scrollToPage(target, animated: true, userInitiated: true)
            }
        }

        func scrollToPage(_ idx: Int, animated: Bool, userInitiated: Bool) {
            guard let hostView, let webView else { return }
            let scrollView = hostView.pagingScrollView
            let pageWidth = max(1, hostView.bounds.width)
            let pageHeight = max(1, hostView.bounds.height)
            let clamped = max(0, min(totalPages - 1, idx))
            let targetX = CGFloat(clamped) * pageWidth

            let contentWidth = CGFloat(totalPages) * pageWidth
            if abs(scrollView.contentSize.width - contentWidth) > 0.5 || abs(scrollView.contentSize.height - pageHeight) > 0.5 {
                scrollView.contentSize = CGSize(width: contentWidth, height: pageHeight)
            }
            if abs(webView.scrollView.contentSize.width - contentWidth) > 0.5 {
                webView.scrollView.contentSize = CGSize(width: contentWidth, height: pageHeight)
            }

            currentPage = clamped
            if animated {
                pendingUserInitiatedScroll = userInitiated
                scrollView.setContentOffset(CGPoint(x: targetX, y: 0), animated: true)
            } else {
                scrollView.setContentOffset(CGPoint(x: targetX, y: 0), animated: false)
                syncWebViewOffset(to: targetX, pageWidth: pageWidth)
                reportPageState(userInitiated: userInitiated)
            }
        }

        private func syncWebViewOffset(to rawX: CGFloat, pageWidth: CGFloat) {
            guard let webView else { return }
            let maxOffsetX = CGFloat(max(0, totalPages - 1)) * pageWidth
            let clampedX = max(0, min(maxOffsetX, rawX))

            if webView.scrollView.contentOffset.x != clampedX || webView.scrollView.contentOffset.y != 0 {
                webView.scrollView.contentOffset = CGPoint(x: clampedX, y: 0)
            }

            // Native rubber-band translation when pulling past page 0 or the last page
            let overscroll = rawX - clampedX
            if abs(overscroll) > 0.1 {
                webView.transform = CGAffineTransform(translationX: -overscroll, y: 0)
            } else if !webView.transform.isIdentity {
                webView.transform = .identity
            }
        }

        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            if scrollView.contentOffset.y != 0 {
                scrollView.contentOffset.y = 0
            }
            let pageWidth = max(1, scrollView.bounds.width)
            syncWebViewOffset(to: scrollView.contentOffset.x, pageWidth: pageWidth)
        }

        func scrollViewDidEndDragging(_ scrollView: UIScrollView, willDecelerate decelerate: Bool) {
            guard isChapterReady else { return }
            let pageWidth = max(1, scrollView.bounds.width)
            let maxOffsetX = CGFloat(max(0, totalPages - 1)) * pageWidth
            let pullThreshold: CGFloat = 55

            if scrollView.contentOffset.x < -pullThreshold {
                parent.onRequestPrevChapter()
                return
            }
            if scrollView.contentOffset.x > maxOffsetX + pullThreshold {
                parent.onRequestNextChapter()
                return
            }

            if !decelerate {
                syncPageFromScrollOffset(scrollView, userInitiated: true)
            }
        }

        func scrollViewDidEndDecelerating(_ scrollView: UIScrollView) {
            guard isChapterReady else { return }
            syncPageFromScrollOffset(scrollView, userInitiated: true)
        }

        func scrollViewDidEndScrollingAnimation(_ scrollView: UIScrollView) {
            guard isChapterReady else { return }
            let initiated = pendingUserInitiatedScroll
            pendingUserInitiatedScroll = false
            syncPageFromScrollOffset(scrollView, userInitiated: initiated)
        }

        private func syncPageFromScrollOffset(_ scrollView: UIScrollView, userInitiated: Bool) {
            let pageWidth = max(1, scrollView.bounds.width)
            let rawPage = Int(round(scrollView.contentOffset.x / pageWidth))
            let clamped = max(0, min(totalPages - 1, rawPage))
            let changed = clamped != currentPage
            currentPage = clamped
            if changed || !userInitiated {
                reportPageState(userInitiated: userInitiated)
            }
        }

        private func reportPageState(userInitiated: Bool) {
            let ratio = totalPages > 1 ? Double(currentPage) / Double(totalPages - 1) : 0
            let estPara = max(1, min(totalParas, Int(round(ratio * Double(max(0, totalParas - 1)))) + 1))
            parent.onPageStateChanged(currentPage, totalPages, estPara, userInitiated)
        }

        // MARK: - WKNavigationDelegate

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            hasFinishedNavigation = true
            injectInitialPagination()
        }

        private func injectInitialPagination() {
            guard let webView, let hostView,
                  hostView.bounds.width > 0, hostView.bounds.height > 0 else { return }

            lastAppliedStyle = StyleSnapshot(
                theme: parent.theme,
                fontFamily: parent.fontFamily,
                fontSize: Int(parent.fontSize),
                marginPx: parent.margin.horizontalPx
            )
            let viewW = max(1, Int(round(hostView.bounds.width)))
            let viewH = max(1, Int(round(hostView.bounds.height)))
            let initJS = makePaginationInjectionJS(
                bg: parent.theme.cssBackgroundHex,
                fg: parent.theme.cssTextHex,
                fontFamily: parent.fontFamily.rawValue,
                fontSize: Int(parent.fontSize),
                marginPx: parent.margin.horizontalPx,
                viewW: viewW,
                viewH: viewH,
                initialRatio: parent.initialScrollRatio,
                startAtLast: parent.startAtLastPage
            )
            webView.evaluateJavaScript(initJS, completionHandler: nil)
        }

        // MARK: - WKScriptMessageHandler

        func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            guard let body = message.body as? [String: Any],
                  let event = body["event"] as? String else { return }

            DispatchQueue.main.async {
                switch event {
                case "layoutComplete":
                    let total = max(1, body["total"] as? Int ?? 1)
                    let targetPage = max(0, min(total - 1, body["page"] as? Int ?? 0))
                    let paras = max(1, body["paras"] as? Int ?? 1)
                    self.totalPages = total
                    self.totalParas = paras
                    self.isChapterReady = true
                    self.scrollToPage(targetPage, animated: false, userInitiated: false)
                default:
                    break
                }
            }
        }

        // MARK: - Pagination CSS & Layout Injection

        private func makePaginationInjectionJS(
            bg: String,
            fg: String,
            fontFamily: String,
            fontSize: Int,
            marginPx: Int,
            viewW: Int,
            viewH: Int,
            initialRatio: Double,
            startAtLast: Bool
        ) -> String {
            return """
            (function() {
                const head = document.head || document.getElementsByTagName('head')[0] || document.documentElement;

                const oldStyle = document.getElementById('__folio_style');
                if (oldStyle) oldStyle.remove();

                const style = document.createElement('style');
                style.id = '__folio_style';
                style.textContent = `
                    html, body {
                        margin: 0 !important;
                        padding: 0 !important;
                        height: \(viewH)px !important;
                        overflow: visible !important;
                        background-color: \(bg) !important;
                        color: \(fg) !important;
                        -webkit-text-size-adjust: 100% !important;
                    }
                    #__folio_stage {
                        position: relative;
                        height: \(viewH)px;
                        margin: 0;
                        padding: 0;
                        overflow: visible;
                    }
                    #__folio_content {
                        position: absolute;
                        top: 20px;
                        left: \(marginPx)px;
                        box-sizing: border-box;
                        column-fill: auto;
                        -webkit-column-fill: auto;
                        font-family: \(fontFamily) !important;
                        font-size: \(fontSize)px !important;
                        line-height: 1.58 !important;
                        color: \(fg) !important;
                        -webkit-hyphens: auto;
                        hyphens: auto;
                        word-wrap: break-word;
                    }
                    #__folio_content p, #__folio_content li, #__folio_content blockquote {
                        color: inherit !important;
                        font-family: inherit !important;
                        font-size: 1em !important;
                        line-height: inherit !important;
                        orphans: 2;
                        widows: 2;
                    }
                    #__folio_content p {
                        margin-top: 0.4em;
                        margin-bottom: 0.65em;
                    }
                    #__folio_content img, #__folio_content svg, #__folio_content figure {
                        max-width: 100% !important;
                        max-height: \(max(100, viewH - 80))px !important;
                        height: auto !important;
                        object-fit: contain;
                        break-inside: avoid !important;
                        -webkit-column-break-inside: avoid !important;
                        display: block;
                        margin: 0.8em auto;
                    }
                    #__folio_content h1, #__folio_content h2, #__folio_content h3 {
                        break-after: avoid !important;
                        -webkit-column-break-after: avoid !important;
                        color: inherit !important;
                        font-family: inherit !important;
                        line-height: 1.3 !important;
                    }
                    #__folio_content pre, #__folio_content table {
                        max-width: 100% !important;
                        overflow: hidden !important;
                    }
                    #__folio_content a { color: #C84B31 !important; }
                `;
                head.appendChild(style);

                window.__folioState = {
                    marginPx: \(marginPx),
                    viewW: \(viewW),
                    viewH: \(viewH),
                    total: 1
                };

                if (!document.getElementById('__folio_stage')) {
                    const stage = document.createElement('div');
                    stage.id = '__folio_stage';
                    const content = document.createElement('div');
                    content.id = '__folio_content';
                    while (document.body.firstChild) {
                        content.appendChild(document.body.firstChild);
                    }
                    stage.appendChild(content);
                    document.body.appendChild(stage);
                }

                function layout(targetRatio, goToLast) {
                    const stage = document.getElementById('__folio_stage');
                    const content = document.getElementById('__folio_content');
                    if (!stage || !content) return;

                    const W = window.__folioState.viewW;
                    const H = window.__folioState.viewH;
                    const M = window.__folioState.marginPx;
                    const colWidth = Math.max(100, W - 2 * M);
                    const gap = 2 * M;

                    content.style.left = M + 'px';
                    content.style.width = colWidth + 'px';
                    content.style.height = Math.max(100, H - 44) + 'px';
                    content.style.columnWidth = colWidth + 'px';
                    content.style.webkitColumnWidth = colWidth + 'px';
                    content.style.columnGap = gap + 'px';
                    content.style.webkitColumnGap = gap + 'px';

                    const sw = content.scrollWidth;
                    let total = 1;
                    if (sw > colWidth + 2 && W > 0) {
                        total = Math.max(1, Math.round((sw + gap) / W));
                    }
                    window.__folioState.total = total;

                    // Size document and stage to exact total * W with overflow: visible so
                    // webView.scrollView renders every page smoothly without clipping to a single viewport
                    const totalWidth = total * W;
                    document.documentElement.style.setProperty('width', totalWidth + 'px', 'important');
                    document.documentElement.style.setProperty('height', H + 'px', 'important');
                    document.body.style.setProperty('width', totalWidth + 'px', 'important');
                    document.body.style.setProperty('height', H + 'px', 'important');
                    stage.style.width = totalWidth + 'px';
                    stage.style.height = H + 'px';

                    const paras = Math.max(1, document.querySelectorAll('#__folio_content p, #__folio_content h2, #__folio_content li').length);
                    let targetPage = 0;
                    if (goToLast) {
                        targetPage = total - 1;
                    } else if (typeof targetRatio === 'number') {
                        targetPage = total > 1 ? Math.min(total - 1, Math.max(0, Math.round(targetRatio * (total - 1)))) : 0;
                    }

                    requestAnimationFrame(() => {
                        window.webkit.messageHandlers.folioReader.postMessage({
                            event: 'layoutComplete',
                            total: total,
                            page: targetPage,
                            paras: paras
                        });
                    });
                }

                window.__folioApplyStyle = function(opts) {
                    document.documentElement.style.setProperty('background-color', opts.bg, 'important');
                    document.body.style.setProperty('background-color', opts.bg, 'important');
                    document.body.style.setProperty('color', opts.fg, 'important');
                    if (typeof opts.marginPx === 'number') window.__folioState.marginPx = opts.marginPx;
                    if (typeof opts.viewW === 'number' && opts.viewW > 0) window.__folioState.viewW = opts.viewW;
                    if (typeof opts.viewH === 'number' && opts.viewH > 0) window.__folioState.viewH = opts.viewH;
                    const c = document.getElementById('__folio_content');
                    if (c) {
                        c.style.setProperty('font-family', opts.fontFamily, 'important');
                        c.style.setProperty('font-size', opts.fontSize + 'px', 'important');
                        c.style.setProperty('color', opts.fg, 'important');
                    }
                    setTimeout(() => layout(opts.preserveRatio ?? 0, false), 30);
                };

                setTimeout(() => {
                    layout(\(initialRatio), \(startAtLast ? "true" : "false"));
                }, 40);
            })();
            """
        }
    }
}
