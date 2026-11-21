import SwiftUI
import WebKit

struct PaginatedReaderCommand: Equatable {
    enum Action: Equatable {
        case none
        case nextPage
        case prevPage
        case goToPage(Int)
    }
    let id = UUID()
    let action: Action
}

struct PaginatedReaderWebView: UIViewRepresentable {
    let chapterURL: URL
    let readAccessRootURL: URL
    let initialScrollRatio: Double
    let startAtLastPage: Bool
    let theme: ReaderTheme
    let fontFamily: ReaderFontFamily
    let fontSize: Double
    let command: PaginatedReaderCommand

    var onPageStateChanged: (Int, Int, Int) -> Void // (pageIndex, totalPages, estimatedPara)
    var onRequestNextChapter: () -> Void
    var onRequestPrevChapter: () -> Void
    var onToggleChrome: () -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
    }

    func makeUIView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        let contentController = WKUserContentController()
        contentController.add(context.coordinator, name: "folioReader")
        config.userContentController = contentController

        let webView = WKWebView(frame: .zero, configuration: config)
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.scrollView.isScrollEnabled = false
        webView.scrollView.bounces = false
        webView.navigationDelegate = context.coordinator

        // Native iOS swipe left / right page turn recognizers
        let swipeLeft = UISwipeGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.handleSwipe(_:)))
        swipeLeft.direction = .left
        let swipeRight = UISwipeGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.handleSwipe(_:)))
        swipeRight.direction = .right
        webView.addGestureRecognizer(swipeLeft)
        webView.addGestureRecognizer(swipeRight)

        context.coordinator.webView = webView
        loadChapter(into: webView, coordinator: context.coordinator)
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        context.coordinator.parent = self

        if context.coordinator.loadedChapterURL != chapterURL {
            loadChapter(into: webView, coordinator: context.coordinator)
        } else {
            // Live update typography & theme
            let js = """
            window.__folioApplyStyle && window.__folioApplyStyle({
                bg: '\(theme.cssBackgroundHex)',
                fg: '\(theme.cssTextHex)',
                fontFamily: "\(fontFamily.rawValue)",
                fontSize: \(Int(fontSize))
            });
            """
            webView.evaluateJavaScript(js, completionHandler: nil)
        }

        if context.coordinator.lastCommandID != command.id {
            context.coordinator.lastCommandID = command.id
            switch command.action {
            case .nextPage:
                webView.evaluateJavaScript("window.__folioNextPage && window.__folioNextPage();", completionHandler: nil)
            case .prevPage:
                webView.evaluateJavaScript("window.__folioPrevPage && window.__folioPrevPage();", completionHandler: nil)
            case .goToPage(let p):
                webView.evaluateJavaScript("window.__folioGoToPage && window.__folioGoToPage(\(p), true);", completionHandler: nil)
            case .none:
                break
            }
        }
    }

    private func loadChapter(into webView: WKWebView, coordinator: Coordinator) {
        coordinator.loadedChapterURL = chapterURL
        webView.loadFileURL(chapterURL, allowingReadAccessTo: readAccessRootURL)
    }

    final class Coordinator: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
        var parent: PaginatedReaderWebView
        weak var webView: WKWebView?
        var loadedChapterURL: URL?
        var lastCommandID: UUID?

        init(_ parent: PaginatedReaderWebView) {
            self.parent = parent
        }

        @objc func handleSwipe(_ gesture: UISwipeGestureRecognizer) {
            guard let webView else { return }
            if gesture.direction == .left {
                webView.evaluateJavaScript("window.__folioNextPage && window.__folioNextPage();", completionHandler: nil)
            } else if gesture.direction == .right {
                webView.evaluateJavaScript("window.__folioPrevPage && window.__folioPrevPage();", completionHandler: nil)
            }
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            let initJS = makePaginationInjectionJS(
                bg: parent.theme.cssBackgroundHex,
                fg: parent.theme.cssTextHex,
                fontFamily: parent.fontFamily.rawValue,
                fontSize: Int(parent.fontSize),
                initialRatio: parent.initialScrollRatio,
                startAtLast: parent.startAtLastPage
            )
            webView.evaluateJavaScript(initJS, completionHandler: nil)
        }

        func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            guard let body = message.body as? [String: Any],
                  let event = body["event"] as? String else { return }

            DispatchQueue.main.async {
                switch event {
                case "pageChanged":
                    let page = body["page"] as? Int ?? 0
                    let total = body["total"] as? Int ?? 1
                    let para = body["para"] as? Int ?? 1
                    self.parent.onPageStateChanged(page, total, para)
                case "nextChapter":
                    self.parent.onRequestNextChapter()
                case "prevChapter":
                    self.parent.onRequestPrevChapter()
                case "toggleChrome":
                    self.parent.onToggleChrome()
                default:
                    break
                }
            }
        }

        private func makePaginationInjectionJS(bg: String, fg: String, fontFamily: String, fontSize: Int, initialRatio: Double, startAtLast: Bool) -> String {
            return """
            (function() {
                const oldStyle = document.getElementById('__folio_style');
                if (oldStyle) oldStyle.remove();

                const style = document.createElement('style');
                style.id = '__folio_style';
                style.textContent = `
                    html, body {
                        margin: 0 !important;
                        padding: 0 !important;
                        width: 100vw !important;
                        height: 100vh !important;
                        overflow: hidden !important;
                        background-color: ${bg} !important;
                        color: ${fg} !important;
                        -webkit-text-size-adjust: 100%;
                    }
                    #__folio_viewport {
                        position: fixed;
                        inset: 0;
                        padding: 16px 20px 18px 20px;
                        box-sizing: border-box;
                        overflow: hidden;
                    }
                    #__folio_track {
                        height: 100%;
                        width: 100%;
                        transition: transform 0.25s cubic-bezier(0.2, 0, 0.2, 1);
                        will-change: transform;
                    }
                    #__folio_content {
                        height: 100%;
                        width: 100%;
                        box-sizing: border-box;
                        column-fill: auto;
                        font-family: ${fontFamily} !important;
                        font-size: ${fontSize}px !important;
                        line-height: 1.65 !important;
                        color: ${fg} !important;
                    }
                    #__folio_content p, #__folio_content li, #__folio_content blockquote {
                        color: inherit !important;
                        font-family: inherit !important;
                        orphans: 2;
                        widows: 2;
                    }
                    #__folio_content img, #__folio_content svg, #__folio_content figure {
                        max-width: 100% !important;
                        max-height: calc(100vh - 60px) !important;
                        height: auto !important;
                        object-fit: contain;
                        break-inside: avoid !important;
                        display: block;
                        margin: 0.8em auto;
                    }
                    #__folio_content h1, #__folio_content h2, #__folio_content h3 {
                        break-after: avoid !important;
                        color: inherit !important;
                    }
                    #__folio_content a { color: #C84B31 !important; }
                `;
                document.head.appendChild(style);

                if (!document.getElementById('__folio_viewport')) {
                    const vp = document.createElement('div');
                    vp.id = '__folio_viewport';
                    const track = document.createElement('div');
                    track.id = '__folio_track';
                    const content = document.createElement('div');
                    content.id = '__folio_content';
                    while (document.body.firstChild) {
                        content.appendChild(document.body.firstChild);
                    }
                    track.appendChild(content);
                    vp.appendChild(track);
                    document.body.appendChild(vp);

                    // Tap zones: left 20% prev, right 20% next, center 60% toggle chrome
                    vp.addEventListener('click', (e) => {
                        if (e.target.closest('a')) return;
                        const sel = window.getSelection();
                        if (sel && sel.toString().trim().length > 0) return;
                        const x = e.clientX;
                        const w = window.innerWidth;
                        if (x < w * 0.20) {
                            window.__folioPrevPage();
                        } else if (x > w * 0.80) {
                            window.__folioNextPage();
                        } else {
                            window.webkit.messageHandlers.folioReader.postMessage({ event: 'toggleChrome' });
                        }
                    });
                }

                window.__folioState = { page: 0, total: 1, colWidth: 320, gap: 28 };

                function layout(preserveRatio) {
                    const track = document.getElementById('__folio_track');
                    const content = document.getElementById('__folio_content');
                    const w = track.clientWidth;
                    const gap = 28;
                    content.style.columnWidth = w + 'px';
                    content.style.columnGap = gap + 'px';

                    const sw = content.scrollWidth;
                    let total = 1;
                    if (sw > w + 2) {
                        total = Math.max(1, Math.ceil((sw - w) / (w + gap)) + 1);
                    }
                    window.__folioState.colWidth = w;
                    window.__folioState.gap = gap;
                    window.__folioState.total = total;

                    if (typeof preserveRatio === 'number') {
                        const target = Math.min(total - 1, Math.max(0, Math.round(preserveRatio * (total - 1))));
                        window.__folioGoToPage(target, false);
                    }
                }

                window.__folioGoToPage = function(idx, animate) {
                    const st = window.__folioState;
                    idx = Math.max(0, Math.min(st.total - 1, idx));
                    st.page = idx;
                    const track = document.getElementById('__folio_track');
                    track.style.transition = animate ? 'transform 0.25s cubic-bezier(0.2, 0, 0.2, 1)' : 'none';
                    const stride = st.colWidth + st.gap;
                    track.style.transform = 'translateX(-' + (idx * stride) + 'px)';

                    const paras = Math.max(1, document.querySelectorAll('#__folio_content p, #__folio_content h2, #__folio_content li').length);
                    const ratio = st.total > 1 ? idx / (st.total - 1) : 0;
                    const estPara = Math.max(1, Math.min(paras, Math.round(ratio * (paras - 1)) + 1));
                    window.webkit.messageHandlers.folioReader.postMessage({
                        event: 'pageChanged',
                        page: idx,
                        total: st.total,
                        para: estPara
                    });
                };

                window.__folioNextPage = function() {
                    const st = window.__folioState;
                    if (st.page < st.total - 1) {
                        window.__folioGoToPage(st.page + 1, true);
                    } else {
                        window.webkit.messageHandlers.folioReader.postMessage({ event: 'nextChapter' });
                    }
                };

                window.__folioPrevPage = function() {
                    const st = window.__folioState;
                    if (st.page > 0) {
                        window.__folioGoToPage(st.page - 1, true);
                    } else {
                        window.webkit.messageHandlers.folioReader.postMessage({ event: 'prevChapter' });
                    }
                };

                window.__folioApplyStyle = function(opts) {
                    const prevTotal = window.__folioState.total;
                    const ratio = prevTotal > 1 ? window.__folioState.page / (prevTotal - 1) : 0;
                    document.documentElement.style.backgroundColor = opts.bg;
                    document.body.style.backgroundColor = opts.bg;
                    document.body.style.color = opts.fg;
                    const c = document.getElementById('__folio_content');
                    if (c) {
                        c.style.setProperty('font-family', opts.fontFamily, 'important');
                        c.style.setProperty('font-size', opts.fontSize + 'px', 'important');
                        c.style.setProperty('color', opts.fg, 'important');
                    }
                    setTimeout(() => layout(ratio), 30);
                };

                window.addEventListener('resize', () => {
                    const prevTotal = window.__folioState.total;
                    const ratio = prevTotal > 1 ? window.__folioState.page / (prevTotal - 1) : 0;
                    setTimeout(() => layout(ratio), 50);
                });

                setTimeout(() => {
                    layout(null);
                    if (\(startAtLast ? "true" : "false")) {
                        window.__folioGoToPage(window.__folioState.total - 1, false);
                    } else {
                        const r = \(initialRatio);
                        const p = window.__folioState.total > 1 ? Math.round(r * (window.__folioState.total - 1)) : 0;
                        window.__folioGoToPage(p, false);
                    }
                }, 40);
            })();
            """
        }
    }
}
