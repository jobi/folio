package io.folio.reader.service

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import io.folio.reader.model.Book
import io.folio.reader.model.CompactPosition
import io.folio.reader.model.LocalEPUBBook
import io.folio.reader.model.PutProgressRequest
import io.folio.reader.model.ReadingProgress
import io.folio.reader.model.SyncMetadata
import io.folio.reader.model.SyncOutboxItem
import io.folio.reader.reader.EPUBParser
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import java.io.File
import kotlin.math.roundToInt
import kotlin.math.roundToLong

class OfflineLibraryStore(
    private val context: Context,
    private val api: FolioAPIClient,
    private val discovery: ServerDiscoveryService
) {
    private val rootDir = File(context.filesDir, "FolioOffline").apply { mkdirs() }
    private val booksDir = File(rootDir, "Books").apply { mkdirs() }
    private val catalogCacheFile = File(rootDir, "catalog.json")
    private val outboxCacheFile = File(rootDir, "sync_outbox.json")

    private val _books = MutableStateFlow<List<Book>>(emptyList())
    val books: StateFlow<List<Book>> = _books.asStateFlow()

    private val _downloadedBookIDs = MutableStateFlow<Set<Long>>(emptySet())
    val downloadedBookIDs: StateFlow<Set<Long>> = _downloadedBookIDs.asStateFlow()

    private val _activeDownloadIDs = MutableStateFlow<Set<Long>>(emptySet())
    val activeDownloadIDs: StateFlow<Set<Long>> = _activeDownloadIDs.asStateFlow()

    private val _pendingSyncOutbox = MutableStateFlow<List<SyncOutboxItem>>(emptyList())
    val pendingSyncOutbox: StateFlow<List<SyncOutboxItem>> = _pendingSyncOutbox.asStateFlow()

    private val _isRefreshing = MutableStateFlow(false)
    val isRefreshing: StateFlow<Boolean> = _isRefreshing.asStateFlow()

    private val _lastErrorMessage = MutableStateFlow<String?>(null)
    val lastErrorMessage: StateFlow<String?> = _lastErrorMessage.asStateFlow()

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    init {
        loadCachedState()
        scanDownloadedBooks()
        registerNetworkMonitor()
    }

    fun localEPUBFile(bookID: Long): File = File(booksDir, "$bookID.epub")
    fun extractedDir(bookID: Long): File = File(booksDir, "${bookID}_extracted")
    fun isDownloaded(bookID: Long): Boolean = _downloadedBookIDs.value.contains(bookID)

    suspend fun refreshLibrary(baseURL: String?) {
        if (baseURL.isNullOrBlank()) return
        _isRefreshing.value = true
        try {
            flushSyncOutbox(baseURL)
            val fetched = api.fetchBooks(baseURL)
            _books.value = fetched
            _lastErrorMessage.value = null
            persistCatalogCache()
        } catch (e: Exception) {
            _lastErrorMessage.value = e.message ?: "Failed to refresh library"
        } finally {
            _isRefreshing.value = false
        }
    }

    suspend fun downloadForOffline(book: Book, baseURL: String?) {
        if (baseURL.isNullOrBlank()) error("No Folio server connected.")
        _activeDownloadIDs.value = _activeDownloadIDs.value + book.id
        try {
            val dest = localEPUBFile(book.id)
            api.downloadEPUB(baseURL, book.id, dest)
            withContext(Dispatchers.IO) {
                EPUBParser.prepareExtractedEPUB(book.id, dest, extractedDir(book.id))
            }
            _downloadedBookIDs.value = _downloadedBookIDs.value + book.id
        } finally {
            _activeDownloadIDs.value = _activeDownloadIDs.value - book.id
        }
    }

    fun removeDownload(bookID: Long) {
        localEPUBFile(bookID).delete()
        extractedDir(bookID).deleteRecursively()
        _downloadedBookIDs.value = _downloadedBookIDs.value - bookID
    }

    suspend fun openLocalEPUB(book: Book, baseURL: String?): LocalEPUBBook {
        if (!isDownloaded(book.id)) {
            downloadForOffline(book, baseURL)
        }
        return withContext(Dispatchers.IO) {
            EPUBParser.prepareExtractedEPUB(book.id, localEPUBFile(book.id), extractedDir(book.id))
        }
    }

    /**
     * Records progress locally and pushes to the server.
     * By default (force = false), protects against overwriting a further-along synced position
     * from another device (CrossPoint / KOReader / iOS). Pass force = true to explicitly override.
     */
    suspend fun recordProgress(
        book: Book,
        percentage: Double,
        spineIndex: Int,
        pageIndex: Int,
        totalPages: Int,
        paraIndex: Int,
        baseURL: String?,
        force: Boolean = false
    ): Boolean {
        val clamped = percentage.coerceIn(0.001, 1.0)

        val existing = _books.value.firstOrNull { it.id == book.id }?.latestProgress ?: book.latestProgress
        if (!force && existing != null && existing.percentage > clamped + 0.002) {
            return false
        }

        val docFrag = spineIndex + 1
        val xpath = "/body/DocFragment[$docFrag]/body/section[1]/p[${maxOf(1, paraIndex)}]/text().0"
        val compact = CompactPosition(
            pctQ = (clamped * 1_000_000.0).roundToLong(),
            spine = maxOf(0, spineIndex),
            page = maxOf(1, pageIndex + 1),
            pages = maxOf(1, totalPages),
            para = maxOf(1, paraIndex),
            xpath = xpath
        )

        val nowSec = System.currentTimeMillis() / 1000L
        val localProgress = ReadingProgress(
            document = book.koMD5Partial,
            bookID = book.id,
            bookTitle = book.title,
            device = api.deviceName,
            deviceID = api.deviceID,
            progress = xpath,
            percentage = clamped,
            position = compact,
            timestamp = nowSec
        )

        updateBookLatestProgress(book.id, localProgress)

        val payload = PutProgressRequest(
            document = book.koMD5Partial,
            bookID = book.id,
            progress = xpath,
            percentage = (clamped * 10000.0).roundToInt() / 10000.0,
            device = api.deviceName,
            deviceID = api.deviceID,
            position = compact,
            metadata = SyncMetadata(
                filename = book.fileName,
                title = book.title,
                authors = book.authors,
                language = book.language
            )
        )

        if (!baseURL.isNullOrBlank()) {
            try {
                api.pushProgress(baseURL, payload)
                _pendingSyncOutbox.value = _pendingSyncOutbox.value.filterNot { it.payload.document == payload.document }
                persistOutbox()
                return true
            } catch (_: Exception) {
                // Queue offline
            }
        }

        enqueueSyncOutbox(payload)
        return true
    }

    /**
     * Pulls remote progress first so we never clobber a further-along remote position
     * with a stale offline outbox item. Returns the furthest-along progress.
     */
    suspend fun syncLatestProgress(book: Book, baseURL: String?): ReadingProgress? {
        val local = _books.value.firstOrNull { it.id == book.id }?.latestProgress ?: book.latestProgress
        if (baseURL.isNullOrBlank()) return local

        val remote = api.pullProgress(baseURL, book.koMD5Partial)
        if (remote != null) {
            val filtered = _pendingSyncOutbox.value.filterNot { item ->
                item.payload.document == book.koMD5Partial && remote.percentage >= item.payload.percentage - 0.002
            }
            if (filtered.size != _pendingSyncOutbox.value.size) {
                _pendingSyncOutbox.value = filtered
                persistOutbox()
            }

            flushSyncOutbox(baseURL)

            val best = if (local != null && local.percentage > remote.percentage + 0.002) local else remote
            updateBookLatestProgress(book.id, best)
            return best
        }

        flushSyncOutbox(baseURL)
        return local
    }

    suspend fun flushSyncOutbox(baseURL: String) {
        val queue = _pendingSyncOutbox.value
        if (queue.isEmpty()) return
        val remaining = mutableListOf<SyncOutboxItem>()
        for (item in queue) {
            // Check remote before pushing queued offline progress so we don't regress another device
            val remote = api.pullProgress(baseURL, item.payload.document)
            if (remote != null && remote.percentage > item.payload.percentage + 0.002) {
                _books.value.firstOrNull { it.koMD5Partial == item.payload.document }?.let { b ->
                    updateBookLatestProgress(b.id, remote)
                }
                continue
            }
            try {
                api.pushProgress(baseURL, item.payload)
            } catch (_: Exception) {
                remaining.add(item)
            }
        }
        _pendingSyncOutbox.value = remaining
        persistOutbox()
    }

    private fun updateBookLatestProgress(bookID: Long, progress: ReadingProgress) {
        val updated = _books.value.map { if (it.id == bookID) it.copy(latestProgress = progress) else it }
        _books.value = updated
        persistCatalogCache()
    }

    private fun enqueueSyncOutbox(payload: PutProgressRequest) {
        val next = _pendingSyncOutbox.value.filterNot { it.payload.document == payload.document } +
            SyncOutboxItem(payload = payload)
        _pendingSyncOutbox.value = next
        persistOutbox()
    }

    private fun scanDownloadedBooks() {
        val ids = booksDir.listFiles()
            ?.filter { it.extension.equals("epub", ignoreCase = true) }
            ?.mapNotNull { it.nameWithoutExtension.toLongOrNull() }
            ?.toSet()
            ?: emptySet()
        _downloadedBookIDs.value = ids
    }

    private fun loadCachedState() {
        try {
            if (catalogCacheFile.exists()) {
                _books.value = api.json.decodeFromString(catalogCacheFile.readText())
            }
        } catch (_: Exception) {}
        try {
            if (outboxCacheFile.exists()) {
                _pendingSyncOutbox.value = api.json.decodeFromString(outboxCacheFile.readText())
            }
        } catch (_: Exception) {}
    }

    private fun persistCatalogCache() {
        try {
            catalogCacheFile.writeText(api.json.encodeToString(_books.value))
        } catch (_: Exception) {}
    }

    private fun persistOutbox() {
        try {
            outboxCacheFile.writeText(api.json.encodeToString(_pendingSyncOutbox.value))
        } catch (_: Exception) {}
    }

    private fun registerNetworkMonitor() {
        try {
            val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return
            val req = NetworkRequest.Builder()
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .build()
            cm.registerNetworkCallback(req, object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) {
                    val base = discovery.activeBaseURL.value ?: return
                    scope.launch { flushSyncOutbox(base) }
                }
            })
        } catch (_: Exception) {}
    }
}
