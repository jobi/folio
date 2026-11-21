import Foundation
import UIKit

enum FolioAPIError: LocalizedError {
    case noServerConfigured
    case invalidResponse(Int, String)

    var errorDescription: String? {
        switch self {
        case .noServerConfigured:
            return "No Folio server connected."
        case .invalidResponse(let code, let message):
            return "Server error (\(code)): \(message)"
        }
    }
}

final class FolioAPIClient: Sendable {
    private let session: URLSession

    init(session: URLSession = .shared) {
        self.session = session
    }

    private func makeRequest(baseURL: URL, path: String, method: String = "GET", body: Data? = nil) -> URLRequest {
        let cleanPath = path.hasPrefix("/") ? String(path.dropFirst()) : path
        let url = baseURL.appendingPathComponent(cleanPath)
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.httpBody = body
        req.timeoutInterval = 15
        req.setValue("ios", forHTTPHeaderField: "X-Folio-Client")
        if body != nil {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return req
    }

    func fetchBooks(baseURL: URL, query: String = "", status: String = "all") async throws -> [Book] {
        var comps = URLComponents(url: baseURL.appendingPathComponent("api/books"), resolvingAgainstBaseURL: false)!
        var items: [URLQueryItem] = [URLQueryItem(name: "sort", value: "updated")]
        if !query.isEmpty { items.append(URLQueryItem(name: "q", value: query)) }
        if status != "all" { items.append(URLQueryItem(name: "status", value: status)) }
        comps.queryItems = items

        var req = URLRequest(url: comps.url!)
        req.setValue("ios", forHTTPHeaderField: "X-Folio-Client")
        let (data, resp) = try await session.data(for: req)
        try validate(resp, data: data)
        return try JSONDecoder().decode(BooksResponse.self, from: data).books
    }

    func fetchBookDetail(baseURL: URL, bookID: Int64) async throws -> BookDetailResponse {
        let req = makeRequest(baseURL: baseURL, path: "/api/books/\(bookID)")
        let (data, resp) = try await session.data(for: req)
        try validate(resp, data: data)
        return try JSONDecoder().decode(BookDetailResponse.self, from: data)
    }

    func downloadEPUB(baseURL: URL, bookID: Int64) async throws -> URL {
        let url = baseURL.appendingPathComponent("api/books/\(bookID)/download")
        var req = URLRequest(url: url)
        req.timeoutInterval = 120
        req.setValue("ios", forHTTPHeaderField: "X-Folio-Client")
        let (tempURL, resp) = try await session.download(for: req)
        try validate(resp, data: Data())
        return tempURL
    }

    func coverURL(baseURL: URL, book: Book) -> URL? {
        if let raw = book.coverURL, !raw.isEmpty {
            if raw.hasPrefix("http") { return URL(string: raw) }
            let clean = raw.hasPrefix("/") ? String(raw.dropFirst()) : raw
            return URL(string: clean, relativeTo: baseURL)?.absoluteURL
        }
        return baseURL.appendingPathComponent("api/books/\(book.id)/cover")
    }

    func pullProgress(baseURL: URL, documentHash: String) async throws -> ReadingProgress? {
        let req = makeRequest(baseURL: baseURL, path: "/syncs/progress/\(documentHash)")
        let (data, resp) = try await session.data(for: req)
        try validate(resp, data: data)
        // Stock KOReader returns `{}` when no progress exists yet
        if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any], obj.isEmpty {
            return nil
        }
        return try JSONDecoder().decode(ReadingProgress.self, from: data)
    }

    func pushProgress(baseURL: URL, payload: PutProgressRequest) async throws {
        let encoded = try JSONEncoder().encode(payload)
        let req = makeRequest(baseURL: baseURL, path: "/syncs/progress", method: "PUT", body: encoded)
        let (data, resp) = try await session.data(for: req)
        try validate(resp, data: data)
    }

    func saveBookmark(baseURL: URL, documentHash: String, bookmark: BookmarkItem) async throws {
        let wrapper = ["items": [bookmark]]
        let encoded = try JSONEncoder().encode(wrapper)
        let req = makeRequest(baseURL: baseURL, path: "/api/v1/bookmarks/\(documentHash)", method: "PUT", body: encoded)
        let (data, resp) = try await session.data(for: req)
        try validate(resp, data: data)
    }

    private func validate(_ response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else {
            throw FolioAPIError.invalidResponse(0, "Invalid HTTP response")
        }
        guard (200...299).contains(http.statusCode) else {
            let bodyText = String(data: data, encoding: .utf8) ?? "Unknown error"
            throw FolioAPIError.invalidResponse(http.statusCode, bodyText)
        }
    }

    @MainActor
    static var currentDeviceName: String {
        "Folio iOS (\(UIDevice.current.name))"
    }

    @MainActor
    static var currentDeviceID: String {
        UIDevice.current.identifierForVendor?.uuidString ?? "folio-ios-device"
    }
}
