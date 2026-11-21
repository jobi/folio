import Foundation
import SwiftUI

// MARK: - Server Discovery & Identity

struct DiscoverResponse: Codable, Sendable {
    let service: String
    let appName: String?
    let version: String?
    let name: String?
    let baseURL: String?
    let authenticated: Bool
    let authMode: String
    let authSource: String
    let user: String?
    let displayName: String?
    let syncUsername: String?
    let tailnet: String?
    let nodeName: String?
    let mdnsEnabled: Bool?
    let bookCount: Int?
    let capabilities: [String]?
    let serverTime: Int64?

    enum CodingKeys: String, CodingKey {
        case service
        case appName = "app_name"
        case version
        case name
        case baseURL = "base_url"
        case authenticated
        case authMode = "auth_mode"
        case authSource = "auth_source"
        case user
        case displayName = "display_name"
        case syncUsername = "sync_username"
        case tailnet
        case nodeName = "node_name"
        case mdnsEnabled = "mdns_enabled"
        case bookCount = "book_count"
        case capabilities
        case serverTime = "server_time"
    }
}

// MARK: - Library Models

struct BooksResponse: Codable, Sendable {
    let books: [Book]
    let total: Int
    let serverTime: Int64?

    enum CodingKeys: String, CodingKey {
        case books
        case total
        case serverTime = "server_time"
    }
}

struct BookDetailResponse: Codable, Sendable {
    let book: Book
    let bookmarks: [BookmarkItem]?
}

struct Book: Codable, Identifiable, Hashable, Sendable {
    let id: Int64
    let uuid: String
    let title: String
    let subtitle: String?
    let authors: String
    let authorList: [String]?
    let series: String?
    let seriesIndex: Double?
    let tags: String?
    let tagList: [String]?
    let description: String?
    let publisher: String?
    let publishedDate: String?
    let language: String?
    let isbn: String?
    let fileName: String
    let fileSize: Int64
    let hasCover: Bool
    let coverURL: String?
    let koMD5Partial: String
    let koMD5Alt: String?
    let koMD5Filename: String?
    let chapterCount: Int
    let pageCount: Int
    let readingStatus: String
    let addedAt: Int64
    let updatedAt: Int64
    var latestProgress: ReadingProgress?

    enum CodingKeys: String, CodingKey {
        case id, uuid, title, subtitle, authors, series, tags, description, publisher, language, isbn
        case authorList = "author_list"
        case seriesIndex = "series_index"
        case tagList = "tag_list"
        case publishedDate = "published_date"
        case fileName = "file_name"
        case fileSize = "file_size"
        case hasCover = "has_cover"
        case coverURL = "cover_url"
        case koMD5Partial = "ko_md5_partial"
        case koMD5Alt = "ko_md5_alt"
        case koMD5Filename = "ko_md5_filename"
        case chapterCount = "chapter_count"
        case pageCount = "page_count"
        case readingStatus = "reading_status"
        case addedAt = "added_at"
        case updatedAt = "updated_at"
        case latestProgress = "latest_progress"
    }

    var displayAuthors: String {
        authors.isEmpty ? "Unknown Author" : authors
    }

    var formattedFileSize: String {
        ByteCountFormatter.string(fromByteCount: fileSize, countStyle: .file)
    }

    var progressPercentage: Double {
        latestProgress?.percentage ?? 0
    }
}

// MARK: - Progress & Sync Models

struct CompactPosition: Codable, Hashable, Sendable {
    let pctQ: UInt32
    let spine: UInt16
    let page: UInt16
    let pages: UInt16
    let para: UInt16?
    let li: UInt16?
    let anchor: String?
    let xpath: String?
}

struct SyncMetadata: Codable, Hashable, Sendable {
    let filename: String?
    let title: String?
    let authors: String?
    let language: String?
}

struct ReadingProgress: Codable, Hashable, Sendable {
    let id: Int64?
    let document: String
    let bookID: Int64?
    let bookTitle: String?
    let device: String
    let deviceID: String
    let progress: String
    let percentage: Double
    let position: CompactPosition?
    let metadata: SyncMetadata?
    let timestamp: Int64

    enum CodingKeys: String, CodingKey {
        case id, document, device, progress, percentage, position, metadata, timestamp
        case bookID = "book_id"
        case bookTitle = "book_title"
        case deviceID = "device_id"
    }
}

struct PutProgressRequest: Codable, Sendable {
    let document: String
    let bookID: Int64?
    let progress: String
    let percentage: Double
    let device: String
    let deviceID: String
    let position: CompactPosition?
    let metadata: SyncMetadata?

    enum CodingKeys: String, CodingKey {
        case document, progress, percentage, device, position, metadata
        case bookID = "book_id"
        case deviceID = "device_id"
    }
}

struct BookmarkItem: Codable, Identifiable, Hashable, Sendable {
    var id: String { "\(si)-\(xpath)-\(updatedAt ?? 0)" }
    let xpath: String
    let percentage: Double
    let summary: String
    let si: Int
    let pp: Int
    let pc: Int
    let deleted: Int?
    let updatedAt: Int64?

    enum CodingKeys: String, CodingKey {
        case xpath, percentage, summary, si, pp, pc, deleted
        case updatedAt = "updated_at"
    }
}

struct SyncOutboxItem: Codable, Identifiable, Sendable {
    let id: UUID
    let payload: PutProgressRequest
    let createdAt: Date
}

// MARK: - Local EPUB & Reader Preferences

struct LocalEPUBBook: Sendable {
    let bookID: Int64
    let extractedRootURL: URL
    let opfBaseURL: URL
    let chapters: [LocalEPUBChapter]
    let tocItems: [LocalTOCItem]
}

struct LocalEPUBChapter: Identifiable, Hashable, Sendable {
    let id: Int
    let href: String
    let fileURL: URL
    let title: String
    let startPct: Double
    let endPct: Double
}

struct LocalTOCItem: Identifiable, Hashable, Sendable {
    let id: UUID = UUID()
    let title: String
    let spineIndex: Int
    let href: String
}

enum ReaderTheme: String, CaseIterable, Identifiable, Codable {
    case paper, sepia, eink, night

    var id: String { rawValue }

    var label: String {
        switch self {
        case .paper: return "Paper"
        case .sepia: return "Sepia"
        case .eink: return "E-Ink"
        case .night: return "Night"
        }
    }

    var backgroundColor: Color {
        switch self {
        case .paper: return Color(red: 0.98, green: 0.97, blue: 0.95)
        case .sepia: return Color(red: 0.95, green: 0.92, blue: 0.85)
        case .eink: return .white
        case .night: return Color(red: 0.08, green: 0.08, blue: 0.075)
        }
    }

    var foregroundColor: Color {
        switch self {
        case .paper: return Color(red: 0.11, green: 0.10, blue: 0.09)
        case .sepia: return Color(red: 0.17, green: 0.14, blue: 0.09)
        case .eink: return .black
        case .night: return Color(red: 0.89, green: 0.88, blue: 0.85)
        }
    }

    var cssBackgroundHex: String {
        switch self {
        case .paper: return "#FAF8F3"
        case .sepia: return "#F3EAD8"
        case .eink: return "#FFFFFF"
        case .night: return "#141413"
        }
    }

    var cssTextHex: String {
        switch self {
        case .paper: return "#1B1A18"
        case .sepia: return "#2B2318"
        case .eink: return "#000000"
        case .night: return "#E4E0D8"
        }
    }
}

enum ReaderFontFamily: String, CaseIterable, Identifiable, Codable {
    case serif = "Georgia, 'Iowan Old Style', serif"
    case palatino = "'Palatino', 'Book Antiqua', serif"
    case sans = "-apple-system, 'SF Pro Text', sans-serif"
    case mono = "'SF Mono', Menlo, monospace"

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .serif: return "Georgia"
        case .palatino: return "Palatino"
        case .sans: return "System Sans"
        case .mono: return "SF Mono"
        }
    }
}
