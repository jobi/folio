package io.folio.reader.reader

import android.animation.Animator
import android.animation.AnimatorListenerAdapter
import android.animation.ValueAnimator
import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Color
import android.os.Handler
import android.os.Looper
import android.view.MotionEvent
import android.view.VelocityTracker
import android.view.View
import android.view.ViewConfiguration
import android.view.animation.DecelerateInterpolator
import android.webkit.JavascriptInterface
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import io.folio.reader.model.ReaderSettings
import java.io.File
import kotlin.math.abs
import kotlin.math.roundToInt

class PaginatedReaderController {
    internal var hostView: PagingWebViewHost? = null

    fun nextPage() {
        hostView?.stepPage(+1)
    }

    fun prevPage() {
        hostView?.stepPage(-1)
    }

    fun goToRatio(ratio: Double) {
        hostView?.goToRatio(ratio)
    }
}

@Composable
fun PaginatedReaderWebView(
    chapterFile: File,
    initialScrollRatio: Double,
    startAtLastPage: Boolean,
    settings: ReaderSettings,
    controller: PaginatedReaderController,
    onPageStateChanged: (pageIndex: Int, totalPages: Int, estimatedPara: Int, userInitiated: Boolean) -> Unit,
    onRequestNextChapter: () -> Unit,
    onRequestPrevChapter: () -> Unit,
    onToggleChrome: () -> Unit,
    modifier: Modifier = Modifier
) {
    val callbacks = remember { CallbackHolder() }
    callbacks.onPageStateChanged = onPageStateChanged
    callbacks.onRequestNextChapter = onRequestNextChapter
    callbacks.onRequestPrevChapter = onRequestPrevChapter
    callbacks.onToggleChrome = onToggleChrome

    DisposableEffect(controller) {
        onDispose {
            controller.hostView = null
        }
    }

    AndroidView(
        modifier = modifier,
        factory = { context ->
            PagingWebViewHost(context, callbacks).also { host ->
                controller.hostView = host
                host.updateState(
                    chapterFile = chapterFile,
                    initialRatio = initialScrollRatio,
                    startAtLast = startAtLastPage,
                    settings = settings
                )
            }
        },
        update = { host ->
            controller.hostView = host
            host.updateState(
                chapterFile = chapterFile,
                initialRatio = initialScrollRatio,
                startAtLast = startAtLastPage,
                settings = settings
            )
        }
    )
}

internal class CallbackHolder {
    var onPageStateChanged: (Int, Int, Int, Boolean) -> Unit = { _, _, _, _ -> }
    var onRequestNextChapter: () -> Unit = {}
    var onRequestPrevChapter: () -> Unit = {}
    var onToggleChrome: () -> Unit = {}
}

@SuppressLint("SetJavaScriptEnabled", "ClickableViewAccessibility")
internal class PagingWebViewHost(
    context: Context,
    private val callbacks: CallbackHolder
) : FrameLayout(context) {

    private val mainHandler = Handler(Looper.getMainLooper())
    private val webView: WebView
    private val touchOverlay: View
    private val touchSlop = ViewConfiguration.get(context).scaledTouchSlop

    private var loadedChapterPath: String? = null
    private var currentSettings: ReaderSettings? = null
    private var initialRatio: Double = 0.0
    private var startAtLast: Boolean = false

    private var currentPage: Int = 0
    private var totalPages: Int = 1
    private var totalParas: Int = 1
    private var isChapterReady: Boolean = false
    private var hasFinishedLoad: Boolean = false
    private var activeAnimator: ValueAnimator? = null

    private var lastWidthPx: Int = 0
    private var lastHeightPx: Int = 0

    init {
        setBackgroundColor(Color.TRANSPARENT)

        webView = WebView(context).apply {
            setBackgroundColor(Color.TRANSPARENT)
            isHorizontalScrollBarEnabled = false
            isVerticalScrollBarEnabled = false
            overScrollMode = View.OVER_SCROLL_NEVER
            settings.apply {
                javaScriptEnabled = true
                allowFileAccess = true
                @Suppress("DEPRECATION")
                allowFileAccessFromFileURLs = true
                @Suppress("DEPRECATION")
                allowUniversalAccessFromFileURLs = true
                useWideViewPort = false
                loadWithOverviewMode = false
                setSupportZoom(false)
                builtInZoomControls = false
                displayZoomControls = false
                textZoom = 100
            }
            addJavascriptInterface(JSBridge(), "FolioBridge")
            webViewClient = object : WebViewClient() {
                override fun onPageFinished(view: WebView?, url: String?) {
                    hasFinishedLoad = true
                    injectPagination()
                }
            }
        }

        touchOverlay = View(context).apply {
            setBackgroundColor(Color.TRANSPARENT)
            setOnTouchListener(PagingTouchListener())
        }

        addView(webView, LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.MATCH_PARENT))
        addView(touchOverlay, LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.MATCH_PARENT))
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        if (w > 0 && h > 0 && (w != lastWidthPx || h != lastHeightPx)) {
            lastWidthPx = w
            lastHeightPx = h
            if (hasFinishedLoad) {
                if (isChapterReady) {
                    applyStyleLive()
                } else {
                    injectPagination()
                }
            }
        }
    }

    fun updateState(
        chapterFile: File,
        initialRatio: Double,
        startAtLast: Boolean,
        settings: ReaderSettings
    ) {
        val prevSettings = currentSettings
        this.currentSettings = settings
        this.initialRatio = initialRatio
        this.startAtLast = startAtLast

        val path = chapterFile.absolutePath
        if (loadedChapterPath != path) {
            loadedChapterPath = path
            currentPage = 0
            totalPages = 1
            isChapterReady = false
            hasFinishedLoad = false
            activeAnimator?.cancel()
            webView.scrollTo(0, 0)
            webView.loadUrl("file://$path")
        } else if (prevSettings != null && (
                prevSettings.theme != settings.theme ||
                prevSettings.fontFamily != settings.fontFamily ||
                prevSettings.fontSize != settings.fontSize ||
                prevSettings.margin != settings.margin
            )
        ) {
            applyStyleLive()
        }
    }

    fun stepPage(delta: Int) {
        if (!isChapterReady) return
        val target = currentPage + delta
        when {
            target < 0 -> callbacks.onRequestPrevChapter()
            target >= totalPages -> callbacks.onRequestNextChapter()
            else -> scrollToPage(
                target,
                animated = currentSettings?.animatePageTurns ?: false,
                userInitiated = true
            )
        }
    }

    fun goToRatio(ratio: Double) {
        if (!isChapterReady) return
        val clamped = ratio.coerceIn(0.0, 1.0)
        val target = if (totalPages > 1) (clamped * (totalPages - 1)).roundToInt() else 0
        scrollToPage(
            target,
            animated = currentSettings?.animatePageTurns ?: false,
            userInitiated = false
        )
    }

    private fun scrollToPage(idx: Int, animated: Boolean, userInitiated: Boolean) {
        val pageWidth = width.coerceAtLeast(1)
        val clamped = idx.coerceIn(0, (totalPages - 1).coerceAtLeast(0))
        val targetX = clamped * pageWidth
        val changed = clamped != currentPage
        currentPage = clamped

        activeAnimator?.cancel()
        if (animated && webView.scrollX != targetX) {
            val startX = webView.scrollX
            activeAnimator = ValueAnimator.ofInt(startX, targetX).apply {
                duration = 220L
                interpolator = DecelerateInterpolator(1.6f)
                addUpdateListener { anim ->
                    webView.scrollTo(anim.animatedValue as Int, 0)
                }
                addListener(object : AnimatorListenerAdapter() {
                    override fun onAnimationEnd(animation: Animator) {
                        webView.scrollTo(targetX, 0)
                        if (changed || !userInitiated) {
                            reportPageState(userInitiated)
                        }
                    }
                })
                start()
            }
        } else {
            webView.scrollTo(targetX, 0)
            if (changed || !userInitiated) {
                reportPageState(userInitiated)
            }
        }
    }

    private fun reportPageState(userInitiated: Boolean) {
        val ratio = if (totalPages > 1) currentPage.toDouble() / (totalPages - 1).toDouble() else 0.0
        val estPara = (ratio * (totalParas - 1).coerceAtLeast(0)).roundToInt().coerceIn(0, totalParas - 1) + 1
        callbacks.onPageStateChanged(currentPage, totalPages, estPara, userInitiated)
    }

    private fun viewDimensionsDp(): Pair<Int, Int> {
        val density = resources.displayMetrics.density.coerceAtLeast(1f)
        val wDp = (width / density).roundToInt().coerceAtLeast(1)
        val hDp = (height / density).roundToInt().coerceAtLeast(1)
        return wDp to hDp
    }

    private fun applyStyleLive() {
        val s = currentSettings ?: return
        if (width <= 0 || height <= 0) return
        val (wDp, hDp) = viewDimensionsDp()
        val ratio = if (totalPages > 1) currentPage.toDouble() / (totalPages - 1).toDouble() else 0.0
        val js = """
            window.__folioApplyStyle && window.__folioApplyStyle({
                bg: '${s.theme.cssBackgroundHex}',
                fg: '${s.theme.cssTextHex}',
                fontFamily: "${s.fontFamily.cssValue}",
                fontSize: ${s.fontSize},
                marginPx: ${s.margin.horizontalDp},
                viewW: $wDp,
                viewH: $hDp,
                preserveRatio: $ratio
            });
        """.trimIndent()
        webView.evaluateJavascript(js, null)
    }

    private fun injectPagination() {
        val s = currentSettings ?: return
        if (width <= 0 || height <= 0) return
        val (wDp, hDp) = viewDimensionsDp()
        val bg = s.theme.cssBackgroundHex
        val fg = s.theme.cssTextHex
        val fontFamily = s.fontFamily.cssValue
        val fontSize = s.fontSize
        val marginPx = s.margin.horizontalDp

        val js = """
            (function() {
                const head = document.head || document.getElementsByTagName('head')[0] || document.documentElement;
                let meta = document.querySelector('meta[name="viewport"]');
                if (!meta) {
                    meta = document.createElement('meta');
                    meta.setAttribute('name', 'viewport');
                    head.appendChild(meta);
                }
                meta.setAttribute('content', 'width=device-width, initial-scale=1.0, minimum-scale=1.0, maximum-scale=1.0, user-scalable=no');

                const oldStyle = document.getElementById('__folio_style');
                if (oldStyle) oldStyle.remove();

                const style = document.createElement('style');
                style.id = '__folio_style';
                style.textContent = `
                    html, body {
                        margin: 0 !important;
                        padding: 0 !important;
                        height: ${hDp}px !important;
                        overflow: visible !important;
                        background-color: $bg !important;
                        color: $fg !important;
                        -webkit-text-size-adjust: 100% !important;
                    }
                    #__folio_stage {
                        position: relative;
                        height: ${hDp}px;
                        margin: 0;
                        padding: 0;
                        overflow: visible;
                    }
                    #__folio_content {
                        position: absolute;
                        top: 20px;
                        left: ${marginPx}px;
                        box-sizing: border-box;
                        column-fill: auto;
                        -webkit-column-fill: auto;
                        font-family: $fontFamily !important;
                        font-size: ${fontSize}px !important;
                        line-height: 1.58 !important;
                        color: $fg !important;
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
                        max-height: ${(hDp - 80).coerceAtLeast(100)}px !important;
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
                    marginPx: $marginPx,
                    viewW: $wDp,
                    viewH: $hDp,
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
                        if (window.FolioBridge) {
                            window.FolioBridge.onLayoutComplete(total, targetPage, paras);
                        }
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
                    layout($initialRatio, ${if (startAtLast) "true" else "false"});
                }, 40);
            })();
        """.trimIndent()
        webView.evaluateJavascript(js, null)
    }

    private inner class JSBridge {
        @JavascriptInterface
        fun onLayoutComplete(total: Int, targetPage: Int, paras: Int) {
            mainHandler.post {
                totalPages = total.coerceAtLeast(1)
                totalParas = paras.coerceAtLeast(1)
                isChapterReady = true
                scrollToPage(targetPage, animated = false, userInitiated = false)
            }
        }
    }

    private inner class PagingTouchListener : OnTouchListener {
        private var downX = 0f
        private var downY = 0f
        private var isDragging = false
        private var velocityTracker: VelocityTracker? = null

        override fun onTouch(v: View, event: MotionEvent): Boolean {
            if (!isChapterReady) return true
            val pageWidth = width.coerceAtLeast(1)
            val animate = currentSettings?.animatePageTurns ?: false

            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    activeAnimator?.cancel()
                    downX = event.x
                    downY = event.y
                    isDragging = false
                    velocityTracker?.recycle()
                    velocityTracker = VelocityTracker.obtain()?.also { it.addMovement(event) }
                    return true
                }

                MotionEvent.ACTION_MOVE -> {
                    velocityTracker?.addMovement(event)
                    val dx = event.x - downX
                    val dy = event.y - downY
                    if (!isDragging && abs(dx) > touchSlop && abs(dx) > abs(dy)) {
                        isDragging = true
                    }
                    if (isDragging && animate) {
                        val baseOffset = currentPage * pageWidth
                        val maxOffset = (totalPages - 1).coerceAtLeast(0) * pageWidth
                        val rawOffset = (baseOffset - dx).roundToInt()
                        webView.scrollTo(rawOffset.coerceIn(0, maxOffset), 0)
                    }
                    return true
                }

                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    velocityTracker?.addMovement(event)
                    velocityTracker?.computeCurrentVelocity(1000)
                    val vx = velocityTracker?.xVelocity ?: 0f
                    velocityTracker?.recycle()
                    velocityTracker = null

                    val dx = event.x - downX
                    if (isDragging) {
                        isDragging = false
                        val threshold = if (animate) pageWidth * 0.30f else pageWidth * 0.18f
                        val flickThreshold = 600f
                        when {
                            dx < 0 && vx > flickThreshold -> scrollToPage(currentPage, animated = animate, userInitiated = false)
                            dx > 0 && vx < -flickThreshold -> scrollToPage(currentPage, animated = animate, userInitiated = false)
                            dx < -threshold || vx < -flickThreshold -> stepPage(+1)
                            dx > threshold || vx > flickThreshold -> stepPage(-1)
                            else -> scrollToPage(currentPage, animated = animate, userInitiated = false)
                        }
                    } else if (event.actionMasked == MotionEvent.ACTION_UP) {
                        // Tap zones: left 25% prev, right 25% next, center 50% toggle chrome
                        when {
                            event.x < pageWidth * 0.25f -> stepPage(-1)
                            event.x > pageWidth * 0.75f -> stepPage(+1)
                            else -> callbacks.onToggleChrome()
                        }
                    }
                    return true
                }
            }
            return true
        }
    }
}
