package io.folio.reader.ui

import android.app.Activity
import androidx.activity.compose.BackHandler
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.FormatListBulleted
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Slider
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableDoubleStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import io.folio.reader.model.Book
import io.folio.reader.model.FolioAccentColor
import io.folio.reader.model.LocalEPUBBook
import io.folio.reader.model.ReaderFontFamily
import io.folio.reader.model.ReaderMargin
import io.folio.reader.model.ReaderSettings
import io.folio.reader.model.ReaderTheme
import io.folio.reader.model.ReadingProgress
import io.folio.reader.reader.PaginatedReaderController
import io.folio.reader.reader.PaginatedReaderWebView
import io.folio.reader.service.OfflineLibraryStore
import io.folio.reader.service.ServerDiscoveryService
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.roundToInt

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReaderScreen(
    book: Book,
    discovery: ServerDiscoveryService,
    store: OfflineLibraryStore,
    settings: ReaderSettings,
    onUpdateSettings: (ReaderSettings) -> Unit,
    controller: PaginatedReaderController,
    onClose: () -> Unit
) {
    val baseURL by discovery.activeBaseURL.collectAsState()
    val scope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current

    var localBook by remember { mutableStateOf<LocalEPUBBook?>(null) }
    var loadError by remember { mutableStateOf<String?>(null) }
    var chapterIndex by remember { mutableIntStateOf(0) }
    var pageIndex by remember { mutableIntStateOf(0) }
    var totalPages by remember { mutableIntStateOf(1) }
    var lastParaIndex by remember { mutableIntStateOf(1) }
    var initialChapterRatio by remember { mutableDoubleStateOf(0.0) }
    var startAtLastPage by remember { mutableStateOf(false) }

    var showChrome by remember { mutableStateOf(false) }
    var showTOC by remember { mutableStateOf(false) }
    var showTypography by remember { mutableStateOf(false) }
    var isSyncing by remember { mutableStateOf(false) }
    var toastMessage by remember { mutableStateOf<String?>(null) }
    var remoteConflict by remember { mutableStateOf<ReadingProgress?>(null) }

    fun computeOverallPercentage(
        opened: LocalEPUBBook? = localBook,
        chIdx: Int = chapterIndex,
        pIdx: Int = pageIndex,
        tot: Int = totalPages
    ): Double {
        if (opened == null || opened.chapters.isEmpty()) return 0.0
        val ch = opened.chapters[chIdx.coerceIn(0, opened.chapters.lastIndex)]
        val ratio = if (tot > 1) pIdx.toDouble() / (tot - 1).toDouble() else 0.0
        return ch.startPct + ratio * (ch.endPct - ch.startPct)
    }

    fun showToast(msg: String) {
        toastMessage = msg
        scope.launch {
            delay(2200)
            if (toastMessage == msg) toastMessage = null
        }
    }

    fun resolvePosition(progress: ReadingProgress, opened: LocalEPUBBook): Pair<Int, Double> {
        if (opened.chapters.isEmpty()) return 0 to 0.0
        val pct = progress.percentage.coerceIn(0.0, 1.0)
        var targetSpine: Int? = null
        val pos = progress.position
        if (pos != null && pos.spine in opened.chapters.indices) {
            val ch = opened.chapters[pos.spine]
            if (pct >= ch.startPct - 0.02 && pct <= ch.endPct + 0.02) {
                targetSpine = pos.spine
            }
        }
        if (targetSpine == null) {
            targetSpine = opened.chapters.indexOfFirst { pct >= it.startPct && pct <= it.endPct }
                .takeIf { it >= 0 }
                ?: if (pct >= 0.99) opened.chapters.lastIndex else 0
        }
        val ch = opened.chapters[targetSpine]
        val ratio = if (ch.endPct > ch.startPct) {
            ((pct - ch.startPct) / (ch.endPct - ch.startPct)).coerceIn(0.0, 1.0)
        } else 0.0
        return targetSpine to ratio
    }

    fun jumpTo(progress: ReadingProgress) {
        val opened = localBook ?: return
        val (targetSpine, targetRatio) = resolvePosition(progress, opened)
        startAtLastPage = false
        if (targetSpine == chapterIndex) {
            controller.goToRatio(targetRatio)
        } else {
            initialChapterRatio = targetRatio
            chapterIndex = targetSpine
        }
        showToast("Jumped to ${(progress.percentage * 100).roundToInt()}%")
    }

    suspend fun pushCurrentPosition(force: Boolean) {
        val pct = computeOverallPercentage()
        store.recordProgress(
            book = book,
            percentage = pct,
            spineIndex = chapterIndex,
            pageIndex = pageIndex,
            totalPages = totalPages,
            paraIndex = lastParaIndex,
            baseURL = baseURL,
            force = force
        )
        showToast("Synced ${(pct * 100).roundToInt()}%")
    }

    suspend fun syncProgress(userTriggered: Boolean) {
        if (baseURL.isNullOrBlank()) {
            if (userTriggered) showToast("Offline — will sync when connected")
            return
        }
        isSyncing = true
        try {
            val latest = store.syncLatestProgress(book, baseURL)
            val currentPct = computeOverallPercentage()
            if (latest != null && latest.percentage > currentPct + 0.005) {
                remoteConflict = latest
            } else if (userTriggered) {
                pushCurrentPosition(force = false)
            }
        } finally {
            isSyncing = false
        }
    }

    BackHandler(onBack = onClose)

    // Initial book open + furthest-along progress pull
    LaunchedEffect(book.id) {
        try {
            val opened = store.openLocalEPUB(book, baseURL)
            val remote = store.syncLatestProgress(book, baseURL)
            val target = remote ?: book.latestProgress
            if (target != null) {
                val (spine, ratio) = resolvePosition(target, opened)
                chapterIndex = spine
                initialChapterRatio = ratio
            } else {
                chapterIndex = 0
                initialChapterRatio = 0.0
            }
            localBook = opened
        } catch (e: Exception) {
            loadError = e.message ?: "Could not open EPUB"
        }
    }

    // Auto-pull progress when returning to the app
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME && localBook != null) {
                scope.launch { syncProgress(userTriggered = false) }
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    val view = LocalView.current

    // Immersive fullscreen: hide status & navigation bars while reading, reveal when chrome is shown
    DisposableEffect(showChrome) {
        val window = (view.context as? Activity)?.window
        val insetsController = window?.let { WindowCompat.getInsetsController(it, view) }
        insetsController?.systemBarsBehavior =
            WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        if (showChrome) {
            insetsController?.show(WindowInsetsCompat.Type.systemBars())
        } else {
            insetsController?.hide(WindowInsetsCompat.Type.systemBars())
        }
        onDispose { }
    }

    // Restore system bars when exiting the reader back to the library
    DisposableEffect(Unit) {
        onDispose {
            val window = (view.context as? Activity)?.window
            val insetsController = window?.let { WindowCompat.getInsetsController(it, view) }
            insetsController?.show(WindowInsetsCompat.Type.systemBars())
        }
    }

    val overallPct = computeOverallPercentage()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(settings.theme.backgroundColor)
    ) {
        val opened = localBook
        when {
            opened != null -> {
                val chapter = opened.chapters[chapterIndex.coerceIn(0, opened.chapters.lastIndex)]
                PaginatedReaderWebView(
                    chapterFile = chapter.file,
                    initialScrollRatio = initialChapterRatio,
                    startAtLastPage = startAtLastPage,
                    settings = settings,
                    controller = controller,
                    onPageStateChanged = { page, total, para, userInitiated ->
                        pageIndex = page
                        totalPages = total
                        lastParaIndex = para
                        startAtLastPage = false
                        initialChapterRatio = 0.0
                        if (userInitiated) {
                            val pct = computeOverallPercentage(opened, chapterIndex, page, total)
                            scope.launch {
                                store.recordProgress(
                                    book = book,
                                    percentage = pct,
                                    spineIndex = chapterIndex,
                                    pageIndex = page,
                                    totalPages = total,
                                    paraIndex = para,
                                    baseURL = baseURL,
                                    force = false
                                )
                            }
                        }
                    },
                    onRequestNextChapter = {
                        if (chapterIndex < opened.chapters.lastIndex) {
                            startAtLastPage = false
                            initialChapterRatio = 0.0
                            chapterIndex += 1
                        }
                    },
                    onRequestPrevChapter = {
                        if (chapterIndex > 0) {
                            startAtLastPage = true
                            initialChapterRatio = 1.0
                            chapterIndex -= 1
                        }
                    },
                    onToggleChrome = { showChrome = !showChrome },
                    modifier = Modifier.fillMaxSize()
                )

                if (showChrome) {
                    // Top bar
                    Surface(
                        tonalElevation = 3.dp,
                        modifier = Modifier
                            .align(Alignment.TopCenter)
                            .fillMaxWidth()
                            .statusBarsPadding()
                    ) {
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(horizontal = 4.dp, vertical = 4.dp)
                        ) {
                            IconButton(onClick = onClose) {
                                Icon(Icons.Default.Close, contentDescription = "Close")
                            }
                            Column(modifier = Modifier.weight(1f)) {
                                Text(
                                    book.title,
                                    style = MaterialTheme.typography.titleSmall,
                                    fontWeight = FontWeight.SemiBold,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis
                                )
                                Text(
                                    chapter.title,
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis
                                )
                            }
                            IconButton(
                                onClick = { scope.launch { syncProgress(userTriggered = true) } },
                                enabled = !isSyncing
                            ) {
                                if (isSyncing) {
                                    CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                                } else {
                                    Icon(Icons.Default.Sync, contentDescription = "Sync Progress")
                                }
                            }
                            IconButton(onClick = { showTOC = true }) {
                                Icon(Icons.AutoMirrored.Filled.FormatListBulleted, contentDescription = "Contents")
                            }
                            TextButton(onClick = { showTypography = true }) {
                                Text("Aa", fontWeight = FontWeight.Bold)
                            }
                        }
                    }

                    // Bottom progress bar
                    Surface(
                        tonalElevation = 3.dp,
                        modifier = Modifier
                            .align(Alignment.BottomCenter)
                            .fillMaxWidth()
                            .navigationBarsPadding()
                    ) {
                        Column(modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp)) {
                            LinearProgressIndicator(
                                progress = { overallPct.toFloat().coerceIn(0f, 1f) },
                                color = FolioAccentColor,
                                modifier = Modifier.fillMaxWidth()
                            )
                            Spacer(Modifier.height(6.dp))
                            Row(
                                horizontalArrangement = Arrangement.SpaceBetween,
                                modifier = Modifier.fillMaxWidth()
                            ) {
                                Text(
                                    "Page ${pageIndex + 1} of $totalPages",
                                    style = MaterialTheme.typography.labelSmall,
                                    fontWeight = FontWeight.Medium
                                )
                                Text(
                                    "Ch. ${chapterIndex + 1}/${opened.chapters.size} • ${(overallPct * 100).roundToInt()}%",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                            }
                        }
                    }
                }

                toastMessage?.let { msg ->
                    Surface(
                        shape = RoundedCornerShape(20.dp),
                        tonalElevation = 4.dp,
                        modifier = Modifier
                            .align(Alignment.BottomCenter)
                            .padding(bottom = if (showChrome) 76.dp else 24.dp)
                    ) {
                        Text(
                            msg,
                            style = MaterialTheme.typography.labelMedium,
                            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)
                        )
                    }
                }
            }

            loadError != null -> {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp)
                ) {
                    Text(
                        loadError!!,
                        color = settings.theme.foregroundColor,
                        textAlign = TextAlign.Center
                    )
                    Spacer(Modifier.height(14.dp))
                    Button(onClick = onClose) { Text("Close") }
                }
            }

            else -> {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                    modifier = Modifier.fillMaxSize()
                ) {
                    CircularProgressIndicator(color = FolioAccentColor)
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "Preparing offline EPUB...",
                        color = settings.theme.foregroundColor.copy(alpha = 0.7f),
                        style = MaterialTheme.typography.bodySmall
                    )
                }
            }
        }
    }

    // Furthest-along conflict dialog
    remoteConflict?.let { remote ->
        val remotePct = (remote.percentage * 100).roundToInt()
        val currentPct = (overallPct * 100).roundToInt()
        val device = remote.device.ifBlank { "another device" }
        AlertDialog(
            onDismissRequest = { remoteConflict = null },
            title = { Text("Furthest Progress: $remotePct%") },
            text = { Text("Synced position on $device is further along than your current page ($currentPct%).") },
            confirmButton = {
                TextButton(onClick = {
                    jumpTo(remote)
                    remoteConflict = null
                }) {
                    Text("Jump to $remotePct%", color = FolioAccentColor, fontWeight = FontWeight.SemiBold)
                }
            },
            dismissButton = {
                Row {
                    TextButton(onClick = {
                        remoteConflict = null
                        scope.launch { pushCurrentPosition(force = true) }
                    }) {
                        Text("Override ($currentPct%)", color = MaterialTheme.colorScheme.error)
                    }
                    TextButton(onClick = { remoteConflict = null }) {
                        Text("Stay")
                    }
                }
            }
        )
    }

    // Table of Contents sheet
    if (showTOC && localBook != null) {
        ModalBottomSheet(onDismissRequest = { showTOC = false }) {
            Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
                Text("Contents", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                Spacer(Modifier.height(8.dp))
                LazyColumn(modifier = Modifier.fillMaxWidth().height(420.dp)) {
                    items(localBook!!.tocItems, key = { it.id }) { item ->
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable {
                                    startAtLastPage = false
                                    initialChapterRatio = 0.0
                                    chapterIndex = item.spineIndex
                                    showTOC = false
                                }
                                .padding(vertical = 12.dp, horizontal = 4.dp)
                        ) {
                            Text(
                                item.title,
                                modifier = Modifier.weight(1f),
                                style = MaterialTheme.typography.bodyMedium
                            )
                            if (item.spineIndex == chapterIndex) {
                                Icon(Icons.Default.Check, contentDescription = null, tint = FolioAccentColor)
                            }
                        }
                        HorizontalDivider()
                    }
                }
                Spacer(Modifier.height(20.dp))
            }
        }
    }

    // Typography & Hardware Controls sheet
    if (showTypography) {
        ModalBottomSheet(onDismissRequest = { showTypography = false }) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = 20.dp, vertical = 8.dp)
            ) {
                Text("Typography & Controls", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                Spacer(Modifier.height(14.dp))

                Text("Surface Theme", style = MaterialTheme.typography.labelLarge, color = FolioAccentColor)
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp)
                ) {
                    ReaderTheme.entries.forEach { t ->
                        FilterChip(
                            selected = settings.theme == t,
                            onClick = { onUpdateSettings(settings.copy(theme = t)) },
                            label = { Text(t.label) }
                        )
                    }
                }

                Spacer(Modifier.height(8.dp))
                Text("Typeface", style = MaterialTheme.typography.labelLarge, color = FolioAccentColor)
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp)
                ) {
                    ReaderFontFamily.entries.forEach { f ->
                        FilterChip(
                            selected = settings.fontFamily == f,
                            onClick = { onUpdateSettings(settings.copy(fontFamily = f)) },
                            label = { Text(f.displayName, maxLines = 1) }
                        )
                    }
                }

                Spacer(Modifier.height(8.dp))
                Text("Font Size (${settings.fontSize} pt)", style = MaterialTheme.typography.labelLarge, color = FolioAccentColor)
                Slider(
                    value = settings.fontSize.toFloat(),
                    onValueChange = { onUpdateSettings(settings.copy(fontSize = it.roundToInt())) },
                    valueRange = 14f..32f,
                    steps = 17
                )

                Spacer(Modifier.height(8.dp))
                Text("Margins", style = MaterialTheme.typography.labelLarge, color = FolioAccentColor)
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp)
                ) {
                    ReaderMargin.entries.forEach { m ->
                        FilterChip(
                            selected = settings.margin == m,
                            onClick = { onUpdateSettings(settings.copy(margin = m)) },
                            label = { Text(m.label) }
                        )
                    }
                }

                Spacer(Modifier.height(10.dp))
                HorizontalDivider()
                Spacer(Modifier.height(10.dp))

                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Column(modifier = Modifier.weight(1f)) {
                        Text("Volume Buttons Turn Pages", style = MaterialTheme.typography.bodyMedium)
                        Text(
                            "Down = Next, Up = Previous",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    Switch(
                        checked = settings.volumeButtonsTurnPages,
                        onCheckedChange = { onUpdateSettings(settings.copy(volumeButtonsTurnPages = it)) }
                    )
                }

                if (settings.volumeButtonsTurnPages) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween,
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Text("Swap Volume Up / Down", style = MaterialTheme.typography.bodyMedium)
                        Switch(
                            checked = settings.swapVolumeButtons,
                            onCheckedChange = { onUpdateSettings(settings.copy(swapVolumeButtons = it)) }
                        )
                    }
                }

                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("Animate Page Turns", style = MaterialTheme.typography.bodyMedium)
                    Switch(
                        checked = settings.animatePageTurns,
                        onCheckedChange = { onUpdateSettings(settings.copy(animatePageTurns = it)) }
                    )
                }

                Spacer(Modifier.height(24.dp))
            }
        }
    }
}
