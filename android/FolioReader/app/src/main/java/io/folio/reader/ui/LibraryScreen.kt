package io.folio.reader.ui

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MenuBook
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.folio.reader.model.Book
import io.folio.reader.model.FolioAccentColor
import io.folio.reader.service.ConnectionState
import io.folio.reader.service.CoverImageCache
import io.folio.reader.service.FolioAPIClient
import io.folio.reader.service.OfflineLibraryStore
import io.folio.reader.service.ServerDiscoveryService
import kotlinx.coroutines.launch

enum class LibraryFilter(val label: String) {
    ALL("All"),
    DOWNLOADED("Downloaded"),
    READING("Reading"),
    UNREAD("Unread")
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LibraryScreen(
    discovery: ServerDiscoveryService,
    store: OfflineLibraryStore,
    api: FolioAPIClient,
    coverCache: CoverImageCache,
    onOpenBook: (Book) -> Unit,
    onOpenSettings: () -> Unit
) {
    val books by store.books.collectAsState()
    val downloadedIDs by store.downloadedBookIDs.collectAsState()
    val activeDownloadIDs by store.activeDownloadIDs.collectAsState()
    val pendingOutbox by store.pendingSyncOutbox.collectAsState()
    val isRefreshing by store.isRefreshing.collectAsState()
    val connectionState by discovery.connectionState.collectAsState()
    val baseURL by discovery.activeBaseURL.collectAsState()

    var searchText by remember { mutableStateOf("") }
    var selectedFilter by remember { mutableStateOf(LibraryFilter.ALL) }
    var selectedBook by remember { mutableStateOf<Book?>(null) }
    val scope = rememberCoroutineScope()

    val filteredBooks = remember(books, searchText, selectedFilter, downloadedIDs) {
        books.filter { book ->
            val matchesSearch = searchText.isBlank() ||
                book.title.contains(searchText, ignoreCase = true) ||
                book.authors.contains(searchText, ignoreCase = true) ||
                (book.series?.contains(searchText, ignoreCase = true) == true)

            if (!matchesSearch) return@filter false

            when (selectedFilter) {
                LibraryFilter.ALL -> true
                LibraryFilter.DOWNLOADED -> downloadedIDs.contains(book.id)
                LibraryFilter.READING -> book.readingStatus == "reading"
                LibraryFilter.UNREAD -> book.readingStatus == "unread" || book.readingStatus.isBlank()
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Folio", fontWeight = FontWeight.Bold) },
                actions = {
                    IconButton(onClick = {
                        scope.launch {
                            if (discovery.activeBaseURL.value == null) {
                                discovery.discoverAutomatically()
                            }
                            store.refreshLibrary(discovery.activeBaseURL.value)
                        }
                    }) {
                        Icon(Icons.Default.Refresh, contentDescription = "Refresh")
                    }
                    IconButton(onClick = onOpenSettings) {
                        Icon(Icons.Default.Settings, contentDescription = "Settings")
                    }
                }
            )
        }
    ) { innerPadding ->
        PullToRefreshBox(
            isRefreshing = isRefreshing,
            onRefresh = {
                scope.launch {
                    if (discovery.activeBaseURL.value == null) {
                        discovery.discoverAutomatically()
                    }
                    store.refreshLibrary(discovery.activeBaseURL.value)
                }
            },
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
        ) {
            Column(modifier = Modifier.fillMaxSize()) {
                ConnectionBanner(
                    state = connectionState,
                    pendingSyncCount = pendingOutbox.size,
                    onRetry = {
                        scope.launch {
                            discovery.discoverAutomatically()
                            store.refreshLibrary(discovery.activeBaseURL.value)
                        }
                    }
                )

                OutlinedTextField(
                    value = searchText,
                    onValueChange = { searchText = it },
                    placeholder = { Text("Title, author, or series") },
                    leadingIcon = { Icon(Icons.Default.Search, contentDescription = null) },
                    trailingIcon = {
                        if (searchText.isNotEmpty()) {
                            IconButton(onClick = { searchText = "" }) {
                                Icon(Icons.Default.Close, contentDescription = "Clear")
                            }
                        }
                    },
                    singleLine = true,
                    shape = RoundedCornerShape(12.dp),
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 6.dp)
                )

                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 4.dp)
                ) {
                    LibraryFilter.entries.forEach { filter ->
                        FilterChip(
                            selected = selectedFilter == filter,
                            onClick = { selectedFilter = filter },
                            label = { Text(filter.label, maxLines = 1) }
                        )
                    }
                }

                if (filteredBooks.isEmpty()) {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center
                    ) {
                        Column(
                            horizontalAlignment = Alignment.CenterHorizontally,
                            modifier = Modifier.padding(32.dp)
                        ) {
                            Icon(
                                Icons.Default.MenuBook,
                                contentDescription = null,
                                modifier = Modifier.size(48.dp),
                                tint = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                            Spacer(Modifier.height(12.dp))
                            Text(
                                text = if (books.isEmpty()) "No Books Yet" else "No Matching Books",
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.SemiBold
                            )
                            Spacer(Modifier.height(6.dp))
                            Text(
                                text = if (books.isEmpty()) {
                                    "Connect to your Folio server on Tailscale or LAN, or configure a custom URL in Settings."
                                } else {
                                    "Try clearing your search or switching filters."
                                },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                textAlign = TextAlign.Center
                            )
                        }
                    }
                } else {
                    LazyVerticalGrid(
                        columns = GridCells.Adaptive(minSize = 140.dp),
                        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp),
                        horizontalArrangement = Arrangement.spacedBy(14.dp),
                        verticalArrangement = Arrangement.spacedBy(18.dp),
                        modifier = Modifier.fillMaxSize()
                    ) {
                        items(filteredBooks, key = { it.id }) { book ->
                            BookGridCard(
                                book = book,
                                coverUrl = baseURL?.let { api.coverURL(it, book) },
                                coverCache = coverCache,
                                isDownloaded = downloadedIDs.contains(book.id),
                                isDownloading = activeDownloadIDs.contains(book.id),
                                onClick = { selectedBook = book }
                            )
                        }
                    }
                }
            }
        }
    }

    selectedBook?.let { book ->
        val freshBook = books.firstOrNull { it.id == book.id } ?: book
        BookDetailSheet(
            book = freshBook,
            baseURL = baseURL,
            api = api,
            coverCache = coverCache,
            store = store,
            onDismiss = { selectedBook = null },
            onOpenReader = { target ->
                selectedBook = null
                onOpenBook(target)
            }
        )
    }
}

@Composable
private fun ConnectionBanner(
    state: ConnectionState,
    pendingSyncCount: Int,
    onRetry: () -> Unit
) {
    Surface(
        shape = RoundedCornerShape(12.dp),
        tonalElevation = 2.dp,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 4.dp)
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp)
        ) {
            when (state) {
                is ConnectionState.Idle, is ConnectionState.Discovering -> {
                    CircularProgressIndicator(modifier = Modifier.size(14.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(10.dp))
                    Text(
                        "Searching Tailnet (folio) & local Wi-Fi...",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                is ConnectionState.Connected -> {
                    Box(
                        modifier = Modifier
                            .size(8.dp)
                            .clip(CircleShape)
                            .background(Color(0xFF2E7D32))
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(
                        "Connected via ${state.via}",
                        style = MaterialTheme.typography.bodySmall,
                        fontWeight = FontWeight.Medium,
                        modifier = Modifier.weight(1f),
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                    if (pendingSyncCount > 0) {
                        Text(
                            "$pendingSyncCount syncing",
                            style = MaterialTheme.typography.labelSmall,
                            color = FolioAccentColor
                        )
                    }
                }
                is ConnectionState.Unavailable -> {
                    Box(
                        modifier = Modifier
                            .size(8.dp)
                            .clip(CircleShape)
                            .background(Color(0xFFEF6C00))
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(
                        state.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis
                    )
                    TextButton(onClick = onRetry) {
                        Text("Retry", color = FolioAccentColor)
                    }
                }
            }
        }
    }
}

@Composable
fun CachedCoverImage(
    book: Book,
    coverUrl: String?,
    coverCache: CoverImageCache,
    modifier: Modifier = Modifier,
    placeholder: @Composable () -> Unit
) {
    var bitmap by remember(book.id) { mutableStateOf<Bitmap?>(coverCache.cachedBitmap(book.id)) }
    val displayBitmap = bitmap ?: coverCache.cachedBitmap(book.id)

    LaunchedEffect(book.id, coverUrl) {
        if (!book.hasCover) return@LaunchedEffect
        val cached = coverCache.cachedBitmap(book.id)
        if (cached != null) {
            bitmap = cached
            return@LaunchedEffect
        }
        val loaded = coverCache.loadCover(book.id, coverUrl)
        if (loaded != null) {
            bitmap = loaded
        }
    }

    if (displayBitmap != null) {
        Image(
            bitmap = displayBitmap.asImageBitmap(),
            contentDescription = book.title,
            contentScale = ContentScale.Crop,
            modifier = modifier
        )
    } else {
        Box(modifier = modifier) {
            placeholder()
        }
    }
}

@Composable
private fun BookGridCard(
    book: Book,
    coverUrl: String?,
    coverCache: CoverImageCache,
    isDownloaded: Boolean,
    isDownloading: Boolean,
    onClick: () -> Unit
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(0.67f)
                .clip(RoundedCornerShape(8.dp))
        ) {
            CachedCoverImage(
                book = book,
                coverUrl = coverUrl,
                coverCache = coverCache,
                modifier = Modifier.fillMaxSize()
            ) {
                CoverPlaceholder(book)
            }

            if (isDownloading) {
                Box(
                    modifier = Modifier
                        .align(Alignment.TopEnd)
                        .padding(6.dp)
                        .clip(CircleShape)
                        .background(Color.White.copy(alpha = 0.85f))
                        .padding(4.dp)
                ) {
                    CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
                }
            } else if (isDownloaded) {
                Icon(
                    Icons.Default.CheckCircle,
                    contentDescription = "Downloaded",
                    tint = FolioAccentColor,
                    modifier = Modifier
                        .align(Alignment.TopEnd)
                        .padding(6.dp)
                        .size(20.dp)
                        .clip(CircleShape)
                        .background(Color.White)
                )
            }

            if (book.progressPercentage > 0.0) {
                Box(
                    modifier = Modifier
                        .align(Alignment.BottomStart)
                        .fillMaxWidth()
                        .height(4.dp)
                        .background(Color.Black.copy(alpha = 0.35f))
                ) {
                    Box(
                        modifier = Modifier
                            .fillMaxWidth(book.progressPercentage.toFloat().coerceIn(0f, 1f))
                            .height(4.dp)
                            .background(FolioAccentColor)
                    )
                }
            }
        }

        Spacer(Modifier.height(6.dp))

        if (!book.series.isNullOrBlank()) {
            Text(
                book.series.uppercase(),
                style = MaterialTheme.typography.labelSmall,
                color = FolioAccentColor,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }

        Text(
            book.title,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis
        )

        Text(
            book.displayAuthors,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
    }
}

@Composable
fun CoverPlaceholder(book: Book) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(
                Brush.linearGradient(
                    listOf(Color(0xFF2E2926), Color(0xFF473D35))
                )
            )
            .padding(12.dp),
        contentAlignment = Alignment.Center
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                book.title,
                color = Color.White,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                textAlign = TextAlign.Center,
                maxLines = 4,
                overflow = TextOverflow.Ellipsis
            )
            Spacer(Modifier.height(6.dp))
            Text(
                book.displayAuthors,
                color = Color.White.copy(alpha = 0.78f),
                style = MaterialTheme.typography.bodySmall,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
}
