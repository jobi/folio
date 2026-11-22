package io.folio.reader.reader

import android.util.Xml
import io.folio.reader.model.LocalEPUBBook
import io.folio.reader.model.LocalEPUBChapter
import io.folio.reader.model.LocalTOCItem
import org.xmlpull.v1.XmlPullParser
import java.io.BufferedInputStream
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.net.URLDecoder
import java.util.zip.ZipInputStream

object EPUBParser {

    fun prepareExtractedEPUB(bookID: Long, epubFile: File, destinationDir: File): LocalEPUBBook {
        val marker = File(destinationDir, ".folio_extracted")
        if (!marker.exists()) {
            if (destinationDir.exists()) {
                destinationDir.deleteRecursively()
            }
            destinationDir.mkdirs()
            unzipSafe(epubFile, destinationDir)
            marker.writeText("ok")
        }

        // 1. Parse META-INF/container.xml
        val containerFile = File(destinationDir, "META-INF/container.xml")
        require(containerFile.exists()) { "Invalid EPUB: missing META-INF/container.xml" }
        val opfRelPath = parseContainerRootfile(containerFile)
            ?: error("Invalid EPUB: could not locate OPF package document")

        val opfFile = File(destinationDir, opfRelPath)
        require(opfFile.exists()) { "Invalid EPUB: OPF file missing ($opfRelPath)" }
        val opfBaseDir = opfFile.parentFile ?: destinationDir

        // 2. Parse OPF manifest & spine
        val opf = parseOPF(opfFile)
        require(opf.spineIdRefs.isNotEmpty()) { "This EPUB has no readable chapters in its spine" }

        data class RawChapter(val href: String, val file: File, val size: Long)
        val rawChapters = mutableListOf<RawChapter>()
        val hrefToSpine = mutableMapOf<String, Int>()

        for (idref in opf.spineIdRefs) {
            val item = opf.manifest[idref] ?: continue
            val decodedHref = decodeHref(item.href)
            val chapterFile = File(opfBaseDir, decodedHref).canonicalFile
            if (!chapterFile.exists()) continue
            val idx = rawChapters.size
            rawChapters.add(RawChapter(item.href, chapterFile, maxOf(256L, chapterFile.length())))
            hrefToSpine[item.href] = idx
            hrefToSpine[decodedHref] = idx
            hrefToSpine[chapterFile.relativeTo(opfBaseDir.canonicalFile).path] = idx
        }
        require(rawChapters.isNotEmpty()) { "This EPUB has no readable chapters in its spine" }

        // 3. Parse TOC (EPUB3 nav or EPUB2 NCX)
        var tocItems = emptyList<LocalTOCItem>()
        if (opf.navHref != null) {
            val navFile = File(opfBaseDir, decodeHref(opf.navHref))
            if (navFile.exists()) {
                tocItems = parseNavTOC(navFile, opfBaseDir.canonicalFile, hrefToSpine)
            }
        }
        if (tocItems.isEmpty() && opf.ncxHref != null) {
            val ncxFile = File(opfBaseDir, decodeHref(opf.ncxHref))
            if (ncxFile.exists()) {
                tocItems = parseNcxTOC(ncxFile, opfBaseDir.canonicalFile, hrefToSpine)
            }
        }

        // 4. Compute cumulative percentage ranges per chapter
        val totalBytes = rawChapters.sumOf { it.size }.toDouble().coerceAtLeast(1.0)
        var running = 0.0
        val titleBySpine = tocItems.associate { it.spineIndex to it.title }
        val chapters = rawChapters.mapIndexed { idx, raw ->
            val start = running / totalBytes
            running += raw.size
            val end = running / totalBytes
            LocalEPUBChapter(
                id = idx,
                href = raw.href,
                file = raw.file,
                title = titleBySpine[idx] ?: "Chapter ${idx + 1}",
                startPct = start,
                endPct = end
            )
        }

        val finalToc = tocItems.ifEmpty {
            chapters.map { LocalTOCItem(title = it.title, spineIndex = it.id, href = it.href) }
        }

        return LocalEPUBBook(
            bookID = bookID,
            extractedRootDir = destinationDir,
            opfBaseDir = opfBaseDir,
            chapters = chapters,
            tocItems = finalToc
        )
    }

    private fun unzipSafe(zipFile: File, destDir: File) {
        val destCanonical = destDir.canonicalPath
        ZipInputStream(BufferedInputStream(FileInputStream(zipFile))).use { zis ->
            var entry = zis.nextEntry
            val buffer = ByteArray(8192)
            while (entry != null) {
                val outFile = File(destDir, entry.name)
                val outCanonical = outFile.canonicalPath
                if (!outCanonical.startsWith(destCanonical + File.separator) && outCanonical != destCanonical) {
                    zis.closeEntry()
                    entry = zis.nextEntry
                    continue
                }
                if (entry.isDirectory) {
                    outFile.mkdirs()
                } else {
                    outFile.parentFile?.mkdirs()
                    FileOutputStream(outFile).use { fos ->
                        var count: Int
                        while (zis.read(buffer).also { count = it } != -1) {
                            fos.write(buffer, 0, count)
                        }
                    }
                }
                zis.closeEntry()
                entry = zis.nextEntry
            }
        }
    }

    private fun decodeHref(href: String): String = try {
        URLDecoder.decode(href.substringBefore('#'), "UTF-8")
    } catch (_: Exception) {
        href.substringBefore('#')
    }

    private fun parseContainerRootfile(file: File): String? {
        FileInputStream(file).use { input ->
            val parser = Xml.newPullParser()
            parser.setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES, false)
            parser.setInput(input, null)
            var event = parser.eventType
            while (event != XmlPullParser.END_DOCUMENT) {
                if (event == XmlPullParser.START_TAG && parser.name.endsWith("rootfile", ignoreCase = true)) {
                    val path = parser.getAttributeValue(null, "full-path")
                    if (!path.isNullOrBlank()) return path
                }
                event = parser.next()
            }
        }
        return null
    }

    private data class ManifestItem(val id: String, val href: String, val mediaType: String, val properties: String)
    private data class OPFResult(
        val manifest: Map<String, ManifestItem>,
        val spineIdRefs: List<String>,
        val navHref: String?,
        val ncxHref: String?
    )

    private fun parseOPF(file: File): OPFResult {
        val manifest = mutableMapOf<String, ManifestItem>()
        val spine = mutableListOf<String>()
        var navHref: String? = null
        var ncxId: String? = null
        var ncxHref: String? = null

        FileInputStream(file).use { input ->
            val parser = Xml.newPullParser()
            parser.setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES, false)
            parser.setInput(input, null)
            var event = parser.eventType
            while (event != XmlPullParser.END_DOCUMENT) {
                if (event == XmlPullParser.START_TAG) {
                    val tag = parser.name.substringAfter(':').lowercase()
                    when (tag) {
                        "item" -> {
                            val id = parser.getAttributeValue(null, "id").orEmpty()
                            val href = parser.getAttributeValue(null, "href").orEmpty()
                            val mediaType = parser.getAttributeValue(null, "media-type").orEmpty()
                            val props = parser.getAttributeValue(null, "properties").orEmpty()
                            if (id.isNotEmpty() && href.isNotEmpty()) {
                                manifest[id] = ManifestItem(id, href, mediaType, props)
                                if (props.split(' ').contains("nav")) navHref = href
                                if (mediaType.contains("dtbncx")) ncxHref = href
                            }
                        }
                        "spine" -> {
                            ncxId = parser.getAttributeValue(null, "toc")
                        }
                        "itemref" -> {
                            val idref = parser.getAttributeValue(null, "idref")
                            val linear = parser.getAttributeValue(null, "linear")
                            if (!idref.isNullOrEmpty() && linear != "no") {
                                spine.add(idref)
                            }
                        }
                    }
                }
                event = parser.next()
            }
        }

        if (ncxHref == null && ncxId != null) {
            ncxHref = manifest[ncxId]?.href
        }
        return OPFResult(manifest, spine, navHref, ncxHref)
    }

    private fun parseNavTOC(
        navFile: File,
        opfBaseDir: File,
        hrefToSpine: Map<String, Int>
    ): List<LocalTOCItem> {
        val items = mutableListOf<LocalTOCItem>()
        val navBaseDir = navFile.parentFile ?: opfBaseDir
        try {
            FileInputStream(navFile).use { input ->
                val parser = Xml.newPullParser()
                parser.setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES, false)
                parser.setInput(input, null)
                var inTocNav = false
                var inAnchor = false
                var currentHref = ""
                val textBuf = StringBuilder()

                var event = parser.eventType
                while (event != XmlPullParser.END_DOCUMENT) {
                    when (event) {
                        XmlPullParser.START_TAG -> {
                            val tag = parser.name.substringAfter(':').lowercase()
                            if (tag == "nav") {
                                val epubType = parser.getAttributeValue(null, "epub:type")
                                    ?: parser.getAttributeValue(null, "type")
                                    ?: ""
                                if (epubType.contains("toc", ignoreCase = true)) {
                                    inTocNav = true
                                }
                            } else if (inTocNav && tag == "a") {
                                currentHref = parser.getAttributeValue(null, "href").orEmpty()
                                textBuf.clear()
                                inAnchor = true
                            }
                        }
                        XmlPullParser.TEXT -> {
                            if (inAnchor) textBuf.append(parser.text)
                        }
                        XmlPullParser.END_TAG -> {
                            val tag = parser.name.substringAfter(':').lowercase()
                            if (tag == "nav" && inTocNav) {
                                inTocNav = false
                            } else if (tag == "a" && inAnchor) {
                                inAnchor = false
                                val title = textBuf.toString().trim().replace(Regex("\\s+"), " ")
                                val spineIdx = resolveSpineIndex(currentHref, navBaseDir, opfBaseDir, hrefToSpine)
                                if (title.isNotEmpty() && spineIdx != null) {
                                    items.add(LocalTOCItem(title = title, spineIndex = spineIdx, href = currentHref))
                                }
                            }
                        }
                    }
                    event = parser.next()
                }
            }
        } catch (_: Exception) {
            // Fall back to NCX or chapter numbers
        }
        return items
    }

    private fun parseNcxTOC(
        ncxFile: File,
        opfBaseDir: File,
        hrefToSpine: Map<String, Int>
    ): List<LocalTOCItem> {
        val items = mutableListOf<LocalTOCItem>()
        val ncxBaseDir = ncxFile.parentFile ?: opfBaseDir
        try {
            FileInputStream(ncxFile).use { input ->
                val parser = Xml.newPullParser()
                parser.setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES, false)
                parser.setInput(input, null)
                var inNavLabel = false
                var inText = false
                var currentTitle = ""
                val textBuf = StringBuilder()

                var event = parser.eventType
                while (event != XmlPullParser.END_DOCUMENT) {
                    when (event) {
                        XmlPullParser.START_TAG -> {
                            val tag = parser.name.substringAfter(':').lowercase()
                            when (tag) {
                                "navlabel" -> inNavLabel = true
                                "text" -> if (inNavLabel) {
                                    textBuf.clear()
                                    inText = true
                                }
                                "content" -> {
                                    val src = parser.getAttributeValue(null, "src").orEmpty()
                                    val spineIdx = resolveSpineIndex(src, ncxBaseDir, opfBaseDir, hrefToSpine)
                                    if (currentTitle.isNotEmpty() && spineIdx != null) {
                                        items.add(LocalTOCItem(title = currentTitle, spineIndex = spineIdx, href = src))
                                        currentTitle = ""
                                    }
                                }
                            }
                        }
                        XmlPullParser.TEXT -> if (inText) textBuf.append(parser.text)
                        XmlPullParser.END_TAG -> {
                            val tag = parser.name.substringAfter(':').lowercase()
                            when (tag) {
                                "text" -> if (inText) {
                                    inText = false
                                    currentTitle = textBuf.toString().trim().replace(Regex("\\s+"), " ")
                                }
                                "navlabel" -> inNavLabel = false
                            }
                        }
                    }
                    event = parser.next()
                }
            }
        } catch (_: Exception) {
            // Fall back to chapter numbers
        }
        return items
    }

    private fun resolveSpineIndex(
        rawHref: String,
        baseDir: File,
        opfBaseDir: File,
        hrefToSpine: Map<String, Int>
    ): Int? {
        val clean = decodeHref(rawHref)
        if (clean.isEmpty()) return null
        hrefToSpine[clean]?.let { return it }
        val resolved = File(baseDir, clean).canonicalFile
        val rel = try {
            resolved.relativeTo(opfBaseDir).path
        } catch (_: Exception) {
            clean
        }
        return hrefToSpine[rel] ?: hrefToSpine.entries.firstOrNull { (k, _) ->
            k.endsWith("/$clean") || clean.endsWith("/$k") || k == clean
        }?.value
    }
}
