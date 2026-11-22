import Foundation
import ZIPFoundation

enum EPUBParserError: LocalizedError {
    case missingContainer
    case missingOPF
    case emptySpine

    var errorDescription: String? {
        switch self {
        case .missingContainer: return "Invalid EPUB: missing META-INF/container.xml"
        case .missingOPF: return "Invalid EPUB: could not locate OPF package document"
        case .emptySpine: return "This EPUB has no readable chapters in its spine"
        }
    }
}

enum EPUBParser {
    static func prepareExtractedEPUB(epubURL: URL, destinationDir: URL) throws -> LocalEPUBBook {
        let fm = FileManager.default
        let markerURL = destinationDir.appendingPathComponent(".folio_extracted")
        if !fm.fileExists(atPath: markerURL.path) {
            if fm.fileExists(atPath: destinationDir.path) {
                try? fm.removeItem(at: destinationDir)
            }
            try fm.createDirectory(at: destinationDir, withIntermediateDirectories: true)
            try fm.unzipItem(at: epubURL, to: destinationDir)
            try Data("ok".utf8).write(to: markerURL)
        }

        // 1. Parse META-INF/container.xml to find the OPF path
        let containerURL = destinationDir.appendingPathComponent("META-INF/container.xml")
        guard let containerData = try? Data(contentsOf: containerURL),
              let opfRelPath = ContainerXMLParser.parseRootfilePath(data: containerData) else {
            throw EPUBParserError.missingContainer
        }

        let opfURL = destinationDir.appendingPathComponent(opfRelPath)
        guard let opfData = try? Data(contentsOf: opfURL) else {
            throw EPUBParserError.missingOPF
        }
        let opfBaseURL = opfURL.deletingLastPathComponent()

        // 2. Parse OPF manifest + spine
        let opfResult = OPFParser.parse(data: opfData)
        guard !opfResult.spineIDRefs.isEmpty else {
            throw EPUBParserError.emptySpine
        }

        var rawChapters: [(href: String, fileURL: URL, size: Int64)] = []
        var hrefToSpine: [String: Int] = [:]

        for idref in opfResult.spineIDRefs {
            guard let item = opfResult.manifest[idref] else { continue }
            let cleanHref = item.href.removingPercentEncoding ?? item.href
            let fileURL = opfBaseURL.appendingPathComponent(cleanHref)
            guard fm.fileExists(atPath: fileURL.path) else { continue }
            let attrs = try? fm.attributesOfItem(atPath: fileURL.path)
            let size = (attrs?[.size] as? NSNumber)?.int64Value ?? 1024
            let idx = rawChapters.count
            rawChapters.append((href: item.href, fileURL: fileURL, size: max(256, size)))
            hrefToSpine[item.href] = idx
            hrefToSpine[cleanHref] = idx
        }

        guard !rawChapters.isEmpty else {
            throw EPUBParserError.emptySpine
        }

        // 3. Parse TOC (EPUB3 nav or NCX)
        var tocItems: [LocalTOCItem] = []
        if let navItem = opfResult.navItem {
            let navURL = opfBaseURL.appendingPathComponent(navItem.href.removingPercentEncoding ?? navItem.href)
            if let navData = try? Data(contentsOf: navURL) {
                tocItems = NavTOCParser.parse(data: navData, navBaseURL: navURL.deletingLastPathComponent(), opfBaseURL: opfBaseURL, hrefToSpine: hrefToSpine)
            }
        }
        if tocItems.isEmpty, let ncxID = opfResult.ncxID, let ncxItem = opfResult.manifest[ncxID] {
            let ncxURL = opfBaseURL.appendingPathComponent(ncxItem.href.removingPercentEncoding ?? ncxItem.href)
            if let ncxData = try? Data(contentsOf: ncxURL) {
                tocItems = NCXParser.parse(data: ncxData, ncxBaseURL: ncxURL.deletingLastPathComponent(), opfBaseURL: opfBaseURL, hrefToSpine: hrefToSpine)
            }
        }

        // Map chapter titles from TOC when available
        var spineTitles: [Int: String] = [:]
        for item in tocItems where spineTitles[item.spineIndex] == nil {
            spineTitles[item.spineIndex] = item.title
        }

        let totalBytes = Double(rawChapters.reduce(0) { $0 + $1.size })
        var running: Double = 0
        let chapters: [LocalEPUBChapter] = rawChapters.enumerated().map { idx, ch in
            let startPct = running / max(1, totalBytes)
            running += Double(ch.size)
            let endPct = running / max(1, totalBytes)
            let title = spineTitles[idx] ?? "Section \(idx + 1)"
            return LocalEPUBChapter(
                id: idx,
                href: ch.href,
                fileURL: ch.fileURL,
                title: title,
                startPct: startPct,
                endPct: endPct
            )
        }

        if tocItems.isEmpty {
            tocItems = chapters.map { LocalTOCItem(title: $0.title, spineIndex: $0.id, href: $0.href) }
        }

        return LocalEPUBBook(
            bookID: 0,
            extractedRootURL: destinationDir,
            opfBaseURL: opfBaseURL,
            chapters: chapters,
            tocItems: tocItems
        )
    }
}

// MARK: - XML Helpers

private final class ContainerXMLParser: NSObject, XMLParserDelegate {
    private var rootPath: String?

    static func parseRootfilePath(data: Data) -> String? {
        let delegate = ContainerXMLParser()
        let parser = XMLParser(data: data)
        parser.delegate = delegate
        parser.parse()
        return delegate.rootPath
    }

    func parser(_ parser: XMLParser, didStartElement elementName: String, namespaceURI: String?, qualifiedName qName: String?, attributes attributeDict: [String: String] = [:]) {
        if elementName.hasSuffix("rootfile"), rootPath == nil {
            rootPath = attributeDict["full-path"]
        }
    }
}

private struct OPFManifestItem {
    let id: String
    let href: String
    let mediaType: String
    let properties: String
}

private final class OPFParser: NSObject, XMLParserDelegate {
    var manifest: [String: OPFManifestItem] = [:]
    var spineIDRefs: [String] = []
    var ncxID: String?
    var navItem: OPFManifestItem?

    static func parse(data: Data) -> OPFParser {
        let delegate = OPFParser()
        let parser = XMLParser(data: data)
        parser.delegate = delegate
        parser.parse()
        return delegate
    }

    func parser(_ parser: XMLParser, didStartElement elementName: String, namespaceURI: String?, qualifiedName qName: String?, attributes attributeDict: [String: String] = [:]) {
        let local = elementName.components(separatedBy: ":").last ?? elementName
        switch local {
        case "item":
            if let id = attributeDict["id"], let href = attributeDict["href"] {
                let props = attributeDict["properties"] ?? ""
                let item = OPFManifestItem(
                    id: id,
                    href: href,
                    mediaType: attributeDict["media-type"] ?? "",
                    properties: props
                )
                manifest[id] = item
                if props.split(separator: " ").contains("nav") {
                    navItem = item
                }
            }
        case "spine":
            ncxID = attributeDict["toc"]
        case "itemref":
            if let idref = attributeDict["idref"] {
                spineIDRefs.append(idref)
            }
        default:
            break
        }
    }
}

private final class NCXParser: NSObject, XMLParserDelegate {
    private var items: [LocalTOCItem] = []
    private var inNavLabel = false
    private var inText = false
    private var currentTitle = ""
    private var ncxBaseURL: URL!
    private var opfBaseURL: URL!
    private var hrefToSpine: [String: Int] = [:]

    static func parse(data: Data, ncxBaseURL: URL, opfBaseURL: URL, hrefToSpine: [String: Int]) -> [LocalTOCItem] {
        let delegate = NCXParser()
        delegate.ncxBaseURL = ncxBaseURL
        delegate.opfBaseURL = opfBaseURL
        delegate.hrefToSpine = hrefToSpine
        let parser = XMLParser(data: data)
        parser.delegate = delegate
        parser.parse()
        return delegate.items
    }

    func parser(_ parser: XMLParser, didStartElement elementName: String, namespaceURI: String?, qualifiedName qName: String?, attributes attributeDict: [String: String] = [:]) {
        let local = elementName.components(separatedBy: ":").last ?? elementName
        if local == "navLabel" {
            inNavLabel = true
            currentTitle = ""
        } else if local == "text" && inNavLabel {
            inText = true
        } else if local == "content", let src = attributeDict["src"] {
            let fileOnly = src.components(separatedBy: "#").first ?? src
            if let idx = resolveSpine(src: fileOnly, relativeTo: ncxBaseURL, opfBase: opfBaseURL, map: hrefToSpine) {
                let title = currentTitle.trimmingCharacters(in: .whitespacesAndNewlines)
                items.append(LocalTOCItem(title: title.isEmpty ? "Chapter \(idx + 1)" : title, spineIndex: idx, href: fileOnly))
            }
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) {
        if inText { currentTitle += string }
    }

    func parser(_ parser: XMLParser, didEndElement elementName: String, namespaceURI: String?, qualifiedName qName: String?) {
        let local = elementName.components(separatedBy: ":").last ?? elementName
        if local == "text" { inText = false }
        if local == "navLabel" { inNavLabel = false }
    }
}

private final class NavTOCParser: NSObject, XMLParserDelegate {
    private var items: [LocalTOCItem] = []
    private var inTocNav = false
    private var inLink = false
    private var currentHref = ""
    private var currentTitle = ""
    private var navBaseURL: URL!
    private var opfBaseURL: URL!
    private var hrefToSpine: [String: Int] = [:]

    static func parse(data: Data, navBaseURL: URL, opfBaseURL: URL, hrefToSpine: [String: Int]) -> [LocalTOCItem] {
        let delegate = NavTOCParser()
        delegate.navBaseURL = navBaseURL
        delegate.opfBaseURL = opfBaseURL
        delegate.hrefToSpine = hrefToSpine
        let parser = XMLParser(data: data)
        parser.delegate = delegate
        parser.parse()
        return delegate.items
    }

    func parser(_ parser: XMLParser, didStartElement elementName: String, namespaceURI: String?, qualifiedName qName: String?, attributes attributeDict: [String: String] = [:]) {
        let local = elementName.components(separatedBy: ":").last ?? elementName
        if local == "nav" {
            let epubType = attributeDict["epub:type"] ?? attributeDict["type"] ?? ""
            if epubType.isEmpty || epubType.contains("toc") {
                inTocNav = true
            }
        } else if inTocNav && local == "a", let href = attributeDict["href"] {
            inLink = true
            currentHref = href
            currentTitle = ""
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) {
        if inLink { currentTitle += string }
    }

    func parser(_ parser: XMLParser, didEndElement elementName: String, namespaceURI: String?, qualifiedName qName: String?) {
        let local = elementName.components(separatedBy: ":").last ?? elementName
        if local == "a" && inLink {
            inLink = false
            let fileOnly = currentHref.components(separatedBy: "#").first ?? currentHref
            if let idx = resolveSpine(src: fileOnly, relativeTo: navBaseURL, opfBase: opfBaseURL, map: hrefToSpine) {
                let title = currentTitle.trimmingCharacters(in: .whitespacesAndNewlines)
                items.append(LocalTOCItem(title: title.isEmpty ? "Chapter \(idx + 1)" : title, spineIndex: idx, href: fileOnly))
            }
        } else if local == "nav" {
            inTocNav = false
        }
    }
}

private func resolveSpine(src: String, relativeTo base: URL, opfBase: URL, map: [String: Int]) -> Int? {
    let decoded = src.removingPercentEncoding ?? src
    if let direct = map[src] ?? map[decoded] {
        return direct
    }
    let resolved = base.appendingPathComponent(decoded).standardizedFileURL.path
    let opfPath = opfBase.standardizedFileURL.path
    if resolved.hasPrefix(opfPath) {
        var rel = String(resolved.dropFirst(opfPath.count))
        if rel.hasPrefix("/") { rel.removeFirst() }
        return map[rel]
    }
    return nil
}
