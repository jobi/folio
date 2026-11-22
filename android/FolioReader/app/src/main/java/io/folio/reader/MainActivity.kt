package io.folio.reader

import android.content.Intent
import android.os.Bundle
import android.view.KeyEvent
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.lifecycle.lifecycleScope
import io.folio.reader.model.Book
import io.folio.reader.model.FolioAccentColor
import io.folio.reader.model.ReaderSettings
import io.folio.reader.reader.PaginatedReaderController
import io.folio.reader.service.CoverImageCache
import io.folio.reader.service.FolioAPIClient
import io.folio.reader.service.OfflineLibraryStore
import io.folio.reader.service.ServerDiscoveryService
import io.folio.reader.ui.LibraryScreen
import io.folio.reader.ui.ReaderScreen
import io.folio.reader.ui.SettingsSheet
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {

    private lateinit var api: FolioAPIClient
    private lateinit var discovery: ServerDiscoveryService
    private lateinit var store: OfflineLibraryStore
    private lateinit var coverCache: CoverImageCache

    private val readerController = PaginatedReaderController()
    private var activeBookState = mutableStateOf<Book?>(null)
    private var readerSettingsState = mutableStateOf<ReaderSettings?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        api = FolioAPIClient(applicationContext)
        discovery = ServerDiscoveryService(applicationContext, api)
        store = OfflineLibraryStore(applicationContext, api, discovery)
        coverCache = CoverImageCache(applicationContext)
        readerSettingsState.value = ReaderSettings.load(applicationContext)

        handleIncomingIntent(intent)

        setContent {
            val settings = readerSettingsState.value ?: ReaderSettings.load(applicationContext)
            val activeBook = activeBookState.value
            var showSettings by remember { mutableStateOf(false) }

            LaunchedEffect(Unit) {
                discovery.discoverAutomatically()
                store.refreshLibrary(discovery.activeBaseURL.value)
            }

            val colorScheme = lightColorScheme(
                primary = FolioAccentColor,
                secondary = FolioAccentColor,
                background = if (ReaderSettings.isEInkDevice) Color.White else Color(0xFFFAF8F3),
                surface = if (ReaderSettings.isEInkDevice) Color.White else Color(0xFFFAF8F3)
            )

            MaterialTheme(colorScheme = colorScheme) {
                if (activeBook != null) {
                    ReaderScreen(
                        book = activeBook,
                        discovery = discovery,
                        store = store,
                        settings = settings,
                        onUpdateSettings = { updated ->
                            readerSettingsState.value = updated
                            ReaderSettings.save(applicationContext, updated)
                        },
                        controller = readerController,
                        onClose = { activeBookState.value = null }
                    )
                } else {
                    LibraryScreen(
                        discovery = discovery,
                        store = store,
                        api = api,
                        coverCache = coverCache,
                        onOpenBook = { book -> activeBookState.value = book },
                        onOpenSettings = { showSettings = true }
                    )

                    if (showSettings) {
                        SettingsSheet(
                            discovery = discovery,
                            store = store,
                            readerSettings = settings,
                            onUpdateReaderSettings = { updated ->
                                readerSettingsState.value = updated
                                ReaderSettings.save(applicationContext, updated)
                            },
                            onDismiss = { showSettings = false }
                        )
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIncomingIntent(intent)
    }

    override fun onDestroy() {
        super.onDestroy()
        discovery.stopNsdBrowsing()
    }

    /**
     * Intercepts hardware Volume Up / Down (and Page Up / Down / D-pad) keys when the reader is open.
     * Enabled by default for Boox Palma ergonomic page turning; consumes both DOWN and UP events
     * so Android's system volume slider never appears.
     */
    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (activeBookState.value != null) {
            val settings = readerSettingsState.value
            val volumeEnabled = settings?.volumeButtonsTurnPages ?: true
            val swap = settings?.swapVolumeButtons ?: false

            when (event.keyCode) {
                KeyEvent.KEYCODE_VOLUME_DOWN -> if (volumeEnabled) {
                    if (event.action == KeyEvent.ACTION_DOWN) {
                        if (swap) readerController.prevPage() else readerController.nextPage()
                    }
                    return true
                }
                KeyEvent.KEYCODE_VOLUME_UP -> if (volumeEnabled) {
                    if (event.action == KeyEvent.ACTION_DOWN) {
                        if (swap) readerController.nextPage() else readerController.prevPage()
                    }
                    return true
                }
                KeyEvent.KEYCODE_PAGE_DOWN, KeyEvent.KEYCODE_DPAD_RIGHT -> {
                    if (event.action == KeyEvent.ACTION_DOWN) readerController.nextPage()
                    return true
                }
                KeyEvent.KEYCODE_PAGE_UP, KeyEvent.KEYCODE_DPAD_LEFT -> {
                    if (event.action == KeyEvent.ACTION_DOWN) readerController.prevPage()
                    return true
                }
            }
        }
        return super.dispatchKeyEvent(event)
    }

    private fun handleIncomingIntent(intent: Intent?) {
        val uri = intent?.data ?: return
        if (uri.scheme.equals("folio", ignoreCase = true)) {
            lifecycleScope.launch {
                if (discovery.handleDeepLink(uri)) {
                    store.refreshLibrary(discovery.activeBaseURL.value)
                }
            }
        }
    }
}
