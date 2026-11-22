package io.folio.reader.model

import android.content.Context
import android.os.Build
import androidx.compose.ui.graphics.Color
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.io.File
import java.util.UUID

// MARK: - Server Discovery & Identity

@Serializable
data class DiscoverResponse(
    val service: String = "",
    @SerialName("app_name") val appName: String? = null,
    val version: String? = null,
    val name: String? = null,
    @SerialName("base_url") val baseURL: String? = null,
    val authenticated: Boolean = false,
    @SerialName("auth_mode") val authMode: String = "",
    @SerialName("auth_source") val authSource: String = "",
    val user: String? = null,
    @SerialName("display_name") val displayName: String? = null,
    @SerialName("sync_username") val syncUsername: String? = null,
    val tailnet: String? = null,
    @SerialName("node_name") val nodeName: String? = null,
    @SerialName("mdns_enabled") val mdnsEnabled: Boolean? = null,
    @SerialName("book_count") val bookCount: Int? = null,
    val capabilities: List<String>? = null,
    @SerialName("server_time") val serverTime: Long? = null
)

// MARK: - Library Models

@Serializable
data class BooksResponse(
    val books: List<Book> = emptyList(),
    val total: Int = 0,
    @SerialName("server_time") val serverTime: Long? = null
)

@Serializable
data class Book(
    val id: Long,
    val uuid: String = "",
    val title: String,
    val subtitle: String? = null,
    val authors: String = "",
    @SerialName("author_list") val authorList: List<String>? = null,
    val series: String? = null,
    @SerialName("series_index") val seriesIndex: Double? = null,
    val tags: String? = null,
    @SerialName("tag_list") val tagList: List<String>? = null,
    val description: String? = null,
    val publisher: String? = null,
    @SerialName("published_date") val publishedDate: String? = null,
    val language: String? = null,
    val isbn: String? = null,
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("has_cover") val hasCover: Boolean = false,
    @SerialName("cover_url") val coverURL: String? = null,
    @SerialName("ko_md5_partial") val koMD5Partial: String = "",
    @SerialName("ko_md5_alt") val koMD5Alt: String? = null,
    @SerialName("ko_md5_filename") val koMD5Filename: String? = null,
    @SerialName("chapter_count") val chapterCount: Int = 0,
    @SerialName("page_count") val pageCount: Int = 0,
    @SerialName("reading_status") val readingStatus: String = "",
    @SerialName("added_at") val addedAt: Long = 0,
    @SerialName("updated_at") val updatedAt: Long = 0,
    @SerialName("latest_progress") val latestProgress: ReadingProgress? = null
) {
    val displayAuthors: String
        get() = authors.ifBlank { "Unknown Author" }

    val progressPercentage: Double
        get() = latestProgress?.percentage ?: 0.0

    val formattedFileSize: String
        get() {
            if (fileSize <= 0) return ""
            val kb = fileSize / 1024.0
            val mb = kb / 1024.0
            return if (mb >= 1.0) String.format("%.1f MB", mb) else String.format("%.0f KB", kb)
        }
}

// MARK: - Progress & Sync Models

@Serializable
data class CompactPosition(
    val pctQ: Long = 0,
    val spine: Int = 0,
    val page: Int = 1,
    val pages: Int = 1,
    val para: Int? = null,
    val li: Int? = null,
    val anchor: String? = null,
    val xpath: String? = null
)

@Serializable
data class SyncMetadata(
    val filename: String? = null,
    val title: String? = null,
    val authors: String? = null,
    val language: String? = null
)

@Serializable
data class ReadingProgress(
    val id: Long? = null,
    val document: String = "",
    @SerialName("book_id") val bookID: Long? = null,
    @SerialName("book_title") val bookTitle: String? = null,
    val device: String = "",
    @SerialName("device_id") val deviceID: String = "",
    val progress: String = "",
    val percentage: Double = 0.0,
    val position: CompactPosition? = null,
    val metadata: SyncMetadata? = null,
    val timestamp: Long = 0
)

@Serializable
data class PutProgressRequest(
    val document: String,
    @SerialName("book_id") val bookID: Long? = null,
    val progress: String,
    val percentage: Double,
    val device: String,
    @SerialName("device_id") val deviceID: String,
    val position: CompactPosition? = null,
    val metadata: SyncMetadata? = null
)

@Serializable
data class SyncOutboxItem(
    val id: String = UUID.randomUUID().toString(),
    val payload: PutProgressRequest,
    val createdAtMs: Long = System.currentTimeMillis()
)

// MARK: - Local EPUB Models

data class LocalEPUBBook(
    val bookID: Long,
    val extractedRootDir: File,
    val opfBaseDir: File,
    val chapters: List<LocalEPUBChapter>,
    val tocItems: List<LocalTOCItem>
)

data class LocalEPUBChapter(
    val id: Int,
    val href: String,
    val file: File,
    val title: String,
    val startPct: Double,
    val endPct: Double
)

data class LocalTOCItem(
    val id: String = UUID.randomUUID().toString(),
    val title: String,
    val spineIndex: Int,
    val href: String
)

// MARK: - Reader Preferences & Themes

enum class ReaderTheme(
    val id: String,
    val label: String,
    val backgroundColor: Color,
    val foregroundColor: Color,
    val cssBackgroundHex: String,
    val cssTextHex: String
) {
    PAPER("paper", "Paper", Color(0xFFFAF8F3), Color(0xFF1B1A18), "#FAF8F3", "#1B1A18"),
    SEPIA("sepia", "Sepia", Color(0xFFF3EAD8), Color(0xFF2B2318), "#F3EAD8", "#2B2318"),
    EINK("eink", "E-Ink", Color(0xFFFFFFFF), Color(0xFF000000), "#FFFFFF", "#000000"),
    NIGHT("night", "Night", Color(0xFF141413), Color(0xFFE4E0D8), "#141413", "#E4E0D8");

    companion object {
        fun fromId(id: String?, defaultTheme: ReaderTheme): ReaderTheme =
            entries.firstOrNull { it.id == id } ?: defaultTheme
    }
}

enum class ReaderFontFamily(
    val id: String,
    val displayName: String,
    val cssValue: String
) {
    BOOK_SERIF("serif", "Book Serif", "Georgia, 'Noto Serif', 'Droid Serif', serif"),
    PALATINO("palatino", "Palatino", "'Palatino Linotype', 'Book Antiqua', 'Noto Serif', serif"),
    SANS("sans", "System Sans", "Roboto, 'Noto Sans', sans-serif"),
    MONO("mono", "Monospace", "'Roboto Mono', monospace");

    companion object {
        fun fromId(id: String?): ReaderFontFamily =
            entries.firstOrNull { it.id == id } ?: BOOK_SERIF
    }
}

enum class ReaderMargin(
    val id: String,
    val label: String,
    val horizontalDp: Int
) {
    COMPACT("compact", "Compact", 18),
    STANDARD("standard", "Standard", 28),
    WIDE("wide", "Wide", 40);

    companion object {
        fun fromId(id: String?): ReaderMargin =
            entries.firstOrNull { it.id == id } ?: STANDARD
    }
}

data class ReaderSettings(
    val theme: ReaderTheme,
    val fontFamily: ReaderFontFamily,
    val fontSize: Int,
    val margin: ReaderMargin,
    val volumeButtonsTurnPages: Boolean,
    val swapVolumeButtons: Boolean,
    val animatePageTurns: Boolean
) {
    companion object {
        private const val PREFS = "folio_reader_prefs"

        val isEInkDevice: Boolean
            get() {
                val m = Build.MANUFACTURER.orEmpty().lowercase()
                val b = Build.BRAND.orEmpty().lowercase()
                val model = Build.MODEL.orEmpty().lowercase()
                return m.contains("onyx") || b.contains("onyx") || b.contains("boox") ||
                    model.contains("palma") || model.contains("boox") ||
                    m.contains("boyue") || m.contains("bigme") || m.contains("hisense")
            }

        fun load(context: Context): ReaderSettings {
            val sp = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            val defaultTheme = if (isEInkDevice) ReaderTheme.EINK else ReaderTheme.PAPER
            return ReaderSettings(
                theme = ReaderTheme.fromId(sp.getString("theme", null), defaultTheme),
                fontFamily = ReaderFontFamily.fromId(sp.getString("fontFamily", null)),
                fontSize = sp.getInt("fontSize", 21),
                margin = ReaderMargin.fromId(sp.getString("margin", null)),
                // Volume Up / Down page turning enabled by default (ideal for Boox Palma)
                volumeButtonsTurnPages = sp.getBoolean("volumeButtonsTurnPages", true),
                swapVolumeButtons = sp.getBoolean("swapVolumeButtons", false),
                // Disable slide animations by default on E-Ink displays to eliminate ghosting
                animatePageTurns = sp.getBoolean("animatePageTurns", !isEInkDevice)
            )
        }

        fun save(context: Context, settings: ReaderSettings) {
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
                .putString("theme", settings.theme.id)
                .putString("fontFamily", settings.fontFamily.id)
                .putInt("fontSize", settings.fontSize)
                .putString("margin", settings.margin.id)
                .putBoolean("volumeButtonsTurnPages", settings.volumeButtonsTurnPages)
                .putBoolean("swapVolumeButtons", settings.swapVolumeButtons)
                .putBoolean("animatePageTurns", settings.animatePageTurns)
                .apply()
        }
    }
}

val FolioAccentColor = Color(0xFFC84B31)
