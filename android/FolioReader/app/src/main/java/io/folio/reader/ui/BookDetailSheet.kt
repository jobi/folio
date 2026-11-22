package io.folio.reader.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.MenuBook
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import io.folio.reader.model.Book
import io.folio.reader.model.FolioAccentColor
import io.folio.reader.service.CoverImageCache
import io.folio.reader.service.FolioAPIClient
import io.folio.reader.service.OfflineLibraryStore
import kotlinx.coroutines.launch
import kotlin.math.roundToInt

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BookDetailSheet(
    book: Book,
    baseURL: String?,
    api: FolioAPIClient,
    coverCache: CoverImageCache,
    store: OfflineLibraryStore,
    onDismiss: () -> Unit,
    onOpenReader: (Book) -> Unit
) {
    val downloadedIDs by store.downloadedBookIDs.collectAsState()
    val isDownloaded = downloadedIDs.contains(book.id)
    var isDownloading by remember { mutableStateOf(false) }
    var errorMessage by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 8.dp)
        ) {
            Row(modifier = Modifier.fillMaxWidth()) {
                CachedCoverImage(
                    book = book,
                    coverUrl = baseURL?.let { api.coverURL(it, book) },
                    coverCache = coverCache,
                    modifier = Modifier
                        .width(105.dp)
                        .height(156.dp)
                        .clip(RoundedCornerShape(8.dp))
                ) {
                    CoverPlaceholder(book)
                }

                Spacer(Modifier.width(16.dp))

                Column(modifier = Modifier.weight(1f)) {
                    if (!book.series.isNullOrBlank()) {
                        Text(
                            book.series.uppercase(),
                            style = MaterialTheme.typography.labelSmall,
                            color = FolioAccentColor,
                            fontWeight = FontWeight.Bold
                        )
                    }
                    Text(
                        book.title,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        book.displayAuthors,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Spacer(Modifier.height(10.dp))

                    val meta = buildList {
                        if (book.formattedFileSize.isNotEmpty()) add(book.formattedFileSize)
                        if (book.chapterCount > 0) add("${book.chapterCount} ch")
                    }.joinToString(" • ")
                    if (meta.isNotEmpty()) {
                        Text(
                            meta,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }

                    if (book.progressPercentage > 0.0) {
                        Spacer(Modifier.height(10.dp))
                        LinearProgressIndicator(
                            progress = { book.progressPercentage.toFloat().coerceIn(0f, 1f) },
                            color = FolioAccentColor,
                            modifier = Modifier.fillMaxWidth()
                        )
                        Spacer(Modifier.height(4.dp))
                        Text(
                            "${(book.progressPercentage * 100).roundToInt()}% completed",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                }
            }

            Spacer(Modifier.height(18.dp))

            Button(
                onClick = { onOpenReader(book) },
                colors = ButtonDefaults.buttonColors(containerColor = FolioAccentColor),
                modifier = Modifier.fillMaxWidth()
            ) {
                Icon(Icons.Default.MenuBook, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text(if (book.progressPercentage > 0.0) "Continue Reading" else "Read Now")
            }

            Spacer(Modifier.height(8.dp))

            if (isDownloaded) {
                OutlinedButton(
                    onClick = { store.removeDownload(book.id) },
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Icon(Icons.Default.Delete, contentDescription = null, tint = MaterialTheme.colorScheme.error)
                    Spacer(Modifier.width(8.dp))
                    Text("Remove Offline Copy", color = MaterialTheme.colorScheme.error)
                }
            } else {
                OutlinedButton(
                    onClick = {
                        scope.launch {
                            isDownloading = true
                            errorMessage = null
                            try {
                                store.downloadForOffline(book, baseURL)
                            } catch (e: Exception) {
                                errorMessage = e.message ?: "Download failed"
                            }
                            isDownloading = false
                        }
                    },
                    enabled = !isDownloading && !baseURL.isNullOrBlank(),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    if (isDownloading) {
                        CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                    } else {
                        Icon(Icons.Default.Download, contentDescription = null)
                        Spacer(Modifier.width(8.dp))
                        Text("Download for Offline Reading")
                    }
                }
            }

            if (errorMessage != null) {
                Spacer(Modifier.height(8.dp))
                Text(errorMessage!!, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }

            if (!book.description.isNullOrBlank()) {
                Spacer(Modifier.height(16.dp))
                Text("Synopsis", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                Spacer(Modifier.height(6.dp))
                Text(
                    book.description.replace(Regex("<[^>]+>"), "").trim(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }

            Spacer(Modifier.height(28.dp))
        }
    }
}
