import Foundation
import ImageIO
import SwiftUI
import UIKit

/// Two-tier (in-memory + disk) downsampled cover image cache with in-flight deduplication.
/// Provides synchronous memory lookup so LazyVGrid cells never flicker or reload when scrolling.
final class CoverImageCache: @unchecked Sendable {
    static let shared = CoverImageCache()

    private let memoryCache: NSCache<NSNumber, UIImage> = {
        let cache = NSCache<NSNumber, UIImage>()
        cache.countLimit = 300
        cache.totalCostLimit = 80 * 1024 * 1024 // 80 MB
        return cache
    }()

    private let diskDirectory: URL
    private let session: URLSession
    private let loader = InFlightLoader()

    private init() {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
        diskDirectory = caches.appendingPathComponent("FolioCovers", isDirectory: true)
        try? FileManager.default.createDirectory(at: diskDirectory, withIntermediateDirectories: true)

        let config = URLSessionConfiguration.default
        config.httpMaximumConnectionsPerHost = 6
        config.requestCachePolicy = .returnCacheDataElseLoad
        config.timeoutIntervalForRequest = 15
        session = URLSession(configuration: config)
    }

    /// Instant synchronous lookup from memory cache (safe to call from SwiftUI body).
    func cachedImage(for bookID: Int64) -> UIImage? {
        memoryCache.object(forKey: NSNumber(value: bookID))
    }

    /// Loads the cover from memory, disk, or network (deduplicated, downsampled, and pre-decoded off main thread).
    func image(for book: Book, url: URL?) async -> UIImage? {
        let key = NSNumber(value: book.id)
        if let hit = memoryCache.object(forKey: key) {
            return hit
        }

        let diskURL = diskFileURL(for: book.id)
        return await loader.load(bookID: book.id) { [weak self] in
            guard let self else { return nil }

            // 1. Check disk cache first (works offline too!)
            if let data = try? Data(contentsOf: diskURL),
               let img = Self.decodeDownsampled(data: data, maxPixelSize: 600) {
                self.storeInMemory(img, for: book.id)
                return img
            }

            // 2. Fetch from server if available
            guard let url else { return nil }
            var req = URLRequest(url: url)
            req.setValue("ios", forHTTPHeaderField: "X-Folio-Client")

            guard let (data, resp) = try? await self.session.data(for: req),
                  let http = resp as? HTTPURLResponse,
                  (200...299).contains(http.statusCode),
                  let img = Self.decodeDownsampled(data: data, maxPixelSize: 600) else {
                return nil
            }

            // Save downsampled JPEG to disk so subsequent launches & offline browsing are instant
            if let jpeg = img.jpegData(compressionQuality: 0.85) {
                try? jpeg.write(to: diskURL, options: .atomic)
            }
            self.storeInMemory(img, for: book.id)
            return img
        }
    }

    private func storeInMemory(_ image: UIImage, for bookID: Int64) {
        let cost = Int(image.size.width * image.size.height * image.scale * image.scale * 4)
        memoryCache.setObject(image, forKey: NSNumber(value: bookID), cost: max(1, cost))
    }

    private func diskFileURL(for bookID: Int64) -> URL {
        diskDirectory.appendingPathComponent("\(bookID).jpg")
    }

    private static func decodeDownsampled(data: Data, maxPixelSize: CGFloat) -> UIImage? {
        let sourceOptions: [CFString: Any] = [
            kCGImageSourceShouldCache: false
        ]
        guard let source = CGImageSourceCreateWithData(data as CFData, sourceOptions as CFDictionary) else {
            return UIImage(data: data)
        }
        let thumbOptions: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixelSize
        ]
        guard let cgImage = CGImageSourceCreateThumbnailAtIndex(source, 0, thumbOptions as CFDictionary) else {
            return UIImage(data: data)
        }
        let image = UIImage(cgImage: cgImage)
        return image.preparingForDisplay() ?? image
    }
}

private actor InFlightLoader {
    private var tasks: [Int64: Task<UIImage?, Never>] = [:]

    func load(bookID: Int64, work: @escaping @Sendable () async -> UIImage?) async -> UIImage? {
        if let existing = tasks[bookID] {
            return await existing.value
        }
        let task = Task(priority: .userInitiated) {
            await work()
        }
        tasks[bookID] = task
        let result = await task.value
        tasks[bookID] = nil
        return result
    }
}

struct CachedCoverImage<Placeholder: View>: View {
    let book: Book
    let url: URL?
    @ViewBuilder let placeholder: () -> Placeholder

    @State private var loadedImage: UIImage?

    var body: some View {
        let displayImage = loadedImage ?? CoverImageCache.shared.cachedImage(for: book.id)
        Group {
            if let displayImage {
                Image(uiImage: displayImage)
                    .resizable()
                    .scaledToFill()
            } else {
                placeholder()
            }
        }
        .task(id: "\(book.id)-\(url?.absoluteString ?? "")") {
            guard book.hasCover else { return }
            if let cached = CoverImageCache.shared.cachedImage(for: book.id) {
                loadedImage = cached
                return
            }
            if let img = await CoverImageCache.shared.image(for: book, url: url) {
                loadedImage = img
            }
        }
    }
}
