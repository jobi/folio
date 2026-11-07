package epub

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ParsedEPUB holds extracted metadata, cover image bytes, and structure metrics from an EPUB file.
type ParsedEPUB struct {
	UUID          string
	Title         string
	Subtitle      string
	Authors       []string
	Series        string
	SeriesIndex   float64
	Tags          []string
	Description   string
	Publisher     string
	PublishedDate string
	Language      string
	ISBN          string
	CoverBytes    []byte
	CoverExt      string
	ChapterCount  int
	PageCount     int
	KOMD5Partial  string
	KOMD5Alt      string
	KOMD5Filename string
}

// SpineChapter describes one linear reading chapter in the EPUB spine.
type SpineChapter struct {
	Index      int    `json:"index"`
	ID         string `json:"id"`
	Href       string `json:"href"`
	Title      string `json:"title"`
	CharCount  int    `json:"char_count"`
	StartPct   float64 `json:"start_pct"`
	EndPct     float64 `json:"end_pct"`
}

// TOCEntry describes a table of contents item.
type TOCEntry struct {
	Title      string `json:"title"`
	Href       string `json:"href"`
	SpineIndex int    `json:"spine_index"`
	Anchor     string `json:"anchor,omitempty"`
}

// BookManifest is returned to the Web Reader to render TOC and navigate chapters.
type BookManifest struct {
	BookID       int64          `json:"book_id"`
	Title        string         `json:"title"`
	Authors      string         `json:"authors"`
	TotalChars   int            `json:"total_chars"`
	PageCount    int            `json:"page_count"`
	Chapters     []SpineChapter `json:"chapters"`
	TOC          []TOCEntry     `json:"toc"`
	KOMD5Partial string         `json:"ko_md5_partial"`
}

// ComputeKOReaderPartialMD5 computes the exact KOReader partial_md5_checksum.
// In KOReader's LuaJIT implementation (util.partialMD5):
// step = 1024, size = 1024; for i = -1, 10 do seek(lshift(step, 2*i)) read(size)
// In 32-bit LuaJIT bit.lshift(1024, -2) shifts by (-2 & 31) = 30 -> 1024 << 30 = 0!
// So the 12 offsets are: 0, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824.
func ComputeKOReaderPartialMD5(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	buf := make([]byte, 1024)
	for i := -1; i <= 10; i++ {
		var offset int64
		if i == -1 {
			offset = 0
		} else {
			offset = int64(1024) << uint(2*i)
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			break
		}
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ComputeAltPartialMD5 computes the alternative partial MD5 where i=-1 uses offset 512 (used by some 3rd-party Python kosync clients).
func ComputeAltPartialMD5(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	buf := make([]byte, 1024)
	for i := -1; i <= 10; i++ {
		var offset int64
		if i == -1 {
			offset = 512
		} else {
			offset = int64(1024) << uint(2*i)
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			break
		}
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ComputeFilenameMD5 computes MD5(basename(filename)) for KOReader/CrossPoint filename-mode sync.
func ComputeFilenameMD5(fileName string) string {
	base := filepath.Base(fileName)
	sum := md5.Sum([]byte(base))
	return hex.EncodeToString(sum[:])
}

// XML structs for container.xml and OPF package
type xmlContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type xmlOPFPackage struct {
	Metadata struct {
		Titles       []string `xml:"title"`
		Creators     []string `xml:"creator"`
		Subjects     []string `xml:"subject"`
		Description  string   `xml:"description"`
		Publisher    string   `xml:"publisher"`
		Dates        []string `xml:"date"`
		Languages    []string `xml:"language"`
		Identifiers  []struct {
			ID     string `xml:"id,attr"`
			Scheme string `xml:"scheme,attr"`
			Value  string `xml:",chardata"`
		} `xml:"identifier"`
		Metas []struct {
			Name     string `xml:"name,attr"`
			Content  string `xml:"content,attr"`
			Property string `xml:"property,attr"`
			Refines  string `xml:"refines,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []xmlManifestItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Toc      string `xml:"toc,attr"`
		Itemrefs []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

type xmlManifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type xmlNCX struct {
	NavMap struct {
		NavPoints []xmlNavPoint `xml:"navPoint"`
	} `xml:"navMap"`
}

type xmlNavPoint struct {
	NavLabel struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []xmlNavPoint `xml:"navPoint"`
}

// ParseEPUB extracts metadata, cover art, chapter counts, and KOReader hashes from an EPUB file.
func ParseEPUB(epubPath, originalFileName string) (*ParsedEPUB, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, fmt.Errorf("open epub zip: %w", err)
	}
	defer zr.Close()

	opfPath, opfPkg, err := readOPF(&zr.Reader)
	if err != nil {
		return nil, err
	}
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	res := &ParsedEPUB{
		Language: "en",
	}

	if len(opfPkg.Metadata.Titles) > 0 {
		res.Title = strings.TrimSpace(opfPkg.Metadata.Titles[0])
		if len(opfPkg.Metadata.Titles) > 1 {
			res.Subtitle = strings.TrimSpace(opfPkg.Metadata.Titles[1])
		}
	}
	if res.Title == "" {
		base := filepath.Base(originalFileName)
		res.Title = strings.TrimSuffix(base, filepath.Ext(base))
	}

	for _, c := range opfPkg.Metadata.Creators {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			res.Authors = append(res.Authors, trimmed)
		}
	}
	if len(res.Authors) == 0 {
		res.Authors = []string{"Unknown Author"}
	}

	for _, s := range opfPkg.Metadata.Subjects {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			res.Tags = append(res.Tags, trimmed)
		}
	}

	res.Description = cleanHTMLDescription(opfPkg.Metadata.Description)
	res.Publisher = strings.TrimSpace(opfPkg.Metadata.Publisher)
	if len(opfPkg.Metadata.Dates) > 0 {
		res.PublishedDate = strings.TrimSpace(opfPkg.Metadata.Dates[0])
	}
	if len(opfPkg.Metadata.Languages) > 0 && strings.TrimSpace(opfPkg.Metadata.Languages[0]) != "" {
		res.Language = strings.TrimSpace(opfPkg.Metadata.Languages[0])
	}

	for _, id := range opfPkg.Metadata.Identifiers {
		val := strings.TrimSpace(id.Value)
		if val == "" {
			continue
		}
		lowerVal := strings.ToLower(val)
		lowerScheme := strings.ToLower(id.Scheme)
		if strings.Contains(lowerScheme, "isbn") || strings.HasPrefix(lowerVal, "urn:isbn:") || isLikelyISBN(val) {
			res.ISBN = strings.TrimPrefix(val, "urn:isbn:")
			res.ISBN = strings.TrimPrefix(res.ISBN, "URN:ISBN:")
		}
		if res.UUID == "" {
			res.UUID = strings.TrimPrefix(val, "urn:uuid:")
		}
	}

	var coverMetaID string
	for _, m := range opfPkg.Metadata.Metas {
		name := strings.ToLower(strings.TrimSpace(m.Name))
		prop := strings.ToLower(strings.TrimSpace(m.Property))
		switch {
		case name == "cover" && m.Content != "":
			coverMetaID = strings.TrimSpace(m.Content)
		case name == "calibre:series" && m.Content != "":
			res.Series = strings.TrimSpace(m.Content)
		case name == "calibre:series_index" && m.Content != "":
			if f, err := strconv.ParseFloat(strings.TrimSpace(m.Content), 64); err == nil {
				res.SeriesIndex = f
			}
		case prop == "belongs-to-collection" && strings.TrimSpace(m.Value) != "":
			res.Series = strings.TrimSpace(m.Value)
		case prop == "group-position" && strings.TrimSpace(m.Value) != "":
			if f, err := strconv.ParseFloat(strings.TrimSpace(m.Value), 64); err == nil {
				res.SeriesIndex = f
			}
		}
	}

	// Extract cover image from manifest
	coverBytes, coverExt := extractCoverFromOPF(&zr.Reader, opfDir, opfPkg, coverMetaID)
	if len(coverBytes) == 0 {
		// Generate a rich bespoke typographic cover so every book has a cover image
		coverBytes = GenerateTypographicCoverPNG(res.Title, strings.Join(res.Authors, ", "), res.Series)
		coverExt = ".png"
	}
	res.CoverBytes = coverBytes
	res.CoverExt = coverExt

	// Count chapters and approximate pages (1500 chars per standard page)
	manifestByID := make(map[string]xmlManifestItem, len(opfPkg.Manifest.Items))
	for _, it := range opfPkg.Manifest.Items {
		manifestByID[it.ID] = it
	}

	totalChars := 0
	chapterCount := 0
	for _, ref := range opfPkg.Spine.Itemrefs {
		if strings.EqualFold(ref.Linear, "no") {
			continue
		}
		item, ok := manifestByID[ref.IDRef]
		if !ok {
			continue
		}
		chapterCount++
		entryPath := resolveZipPath(opfDir, item.Href)
		if data, err := readZipEntry(&zr.Reader, entryPath); err == nil {
			plain := stripHTMLTags(string(data))
			totalChars += len(plain)
		}
	}
	if chapterCount == 0 {
		chapterCount = 1
	}
	res.ChapterCount = chapterCount
	pages := int(math.Max(1, math.Round(float64(totalChars)/1500.0)))
	res.PageCount = pages

	res.KOMD5Partial, _ = ComputeKOReaderPartialMD5(epubPath)
	res.KOMD5Alt, _ = ComputeAltPartialMD5(epubPath)
	if originalFileName == "" {
		originalFileName = filepath.Base(epubPath)
	}
	res.KOMD5Filename = ComputeFilenameMD5(originalFileName)

	return res, nil
}

func readOPF(zr *zip.Reader) (string, *xmlOPFPackage, error) {
	containerBytes, err := readZipEntry(zr, "META-INF/container.xml")
	var opfPath string
	if err == nil {
		var c xmlContainer
		if xml.Unmarshal(containerBytes, &c) == nil && len(c.Rootfiles) > 0 {
			opfPath = c.Rootfiles[0].FullPath
		}
	}
	if opfPath == "" {
		// Fallback: find any .opf file in the zip
		for _, f := range zr.File {
			if strings.HasSuffix(strings.ToLower(f.Name), ".opf") {
				opfPath = f.Name
				break
			}
		}
	}
	if opfPath == "" {
		return "", nil, fmt.Errorf("no OPF package document found in EPUB")
	}

	opfBytes, err := readZipEntry(zr, opfPath)
	if err != nil {
		return "", nil, fmt.Errorf("read OPF %s: %w", opfPath, err)
	}

	var pkg xmlOPFPackage
	if err := xml.Unmarshal(opfBytes, &pkg); err != nil {
		return "", nil, fmt.Errorf("parse OPF XML: %w", err)
	}
	return opfPath, &pkg, nil
}

func extractCoverFromOPF(zr *zip.Reader, opfDir string, pkg *xmlOPFPackage, coverMetaID string) ([]byte, string) {
	var candidate *xmlManifestItem

	// Strategy 1: EPUB 3 properties="cover-image"
	for i := range pkg.Manifest.Items {
		it := &pkg.Manifest.Items[i]
		if strings.Contains(strings.ToLower(it.Properties), "cover-image") && strings.HasPrefix(strings.ToLower(it.MediaType), "image/") {
			candidate = it
			break
		}
	}

	// Strategy 2: EPUB 2 <meta name="cover" content="item-id"/>
	if candidate == nil && coverMetaID != "" {
		for i := range pkg.Manifest.Items {
			it := &pkg.Manifest.Items[i]
			if (it.ID == coverMetaID || it.Href == coverMetaID) && strings.HasPrefix(strings.ToLower(it.MediaType), "image/") {
				candidate = it
				break
			}
		}
	}

	// Strategy 3: Manifest item with ID or Href containing "cover" and media-type image/*
	if candidate == nil {
		for i := range pkg.Manifest.Items {
			it := &pkg.Manifest.Items[i]
			if !strings.HasPrefix(strings.ToLower(it.MediaType), "image/") {
				continue
			}
			if strings.Contains(strings.ToLower(it.ID), "cover") || strings.Contains(strings.ToLower(it.Href), "cover") {
				candidate = it
				break
			}
		}
	}

	// Strategy 4: First image in manifest
	if candidate == nil {
		for i := range pkg.Manifest.Items {
			it := &pkg.Manifest.Items[i]
			if strings.HasPrefix(strings.ToLower(it.MediaType), "image/") {
				candidate = it
				break
			}
		}
	}

	if candidate == nil {
		return nil, ""
	}

	zipPath := resolveZipPath(opfDir, candidate.Href)
	data, err := readZipEntry(zr, zipPath)
	if err != nil || len(data) == 0 {
		return nil, ""
	}
	ext := strings.ToLower(filepath.Ext(candidate.Href))
	if ext == "" {
		switch strings.ToLower(candidate.MediaType) {
		case "image/png":
			ext = ".png"
		case "image/svg+xml":
			ext = ".svg"
		case "image/webp":
			ext = ".webp"
		default:
			ext = ".jpg"
		}
	}
	return data, ext
}

// GetBookManifest parses the spine and table of contents for the built-in Web Reader.
func GetBookManifest(epubPath string, bookID int64, bookTitle, bookAuthors, koMD5 string) (*BookManifest, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	opfPath, pkg, err := readOPF(&zr.Reader)
	if err != nil {
		return nil, err
	}
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	manifestByID := make(map[string]xmlManifestItem, len(pkg.Manifest.Items))
	for _, it := range pkg.Manifest.Items {
		manifestByID[it.ID] = it
	}

	// Parse NCX or Nav TOC first so we can map href -> title
	tocEntries := parseTOC(&zr.Reader, opfDir, pkg)
	hrefTitles := make(map[string]string)
	for _, t := range tocEntries {
		cleanHref := stripFragment(t.Href)
		if _, exists := hrefTitles[cleanHref]; !exists && t.Title != "" {
			hrefTitles[cleanHref] = t.Title
		}
	}

	var chapters []SpineChapter
	hrefToSpine := make(map[string]int)
	totalChars := 0

	for _, ref := range pkg.Spine.Itemrefs {
		if strings.EqualFold(ref.Linear, "no") {
			continue
		}
		it, ok := manifestByID[ref.IDRef]
		if !ok {
			continue
		}
		cleanHref := stripFragment(it.Href)
		zipEntry := resolveZipPath(opfDir, cleanHref)
		rawBytes, _ := readZipEntry(&zr.Reader, zipEntry)
		rawStr := string(rawBytes)
		plain := stripHTMLTags(rawStr)
		charLen := len(plain)
		if charLen < 50 {
			charLen = 50
		}

		title := hrefTitles[cleanHref]
		if title == "" {
			title = extractHeadingFromHTML(rawStr)
		}
		if title == "" {
			title = fmt.Sprintf("Section %d", len(chapters)+1)
		}

		idx := len(chapters)
		hrefToSpine[cleanHref] = idx
		hrefToSpine[zipEntry] = idx

		chapters = append(chapters, SpineChapter{
			Index:     idx,
			ID:        it.ID,
			Href:      cleanHref,
			Title:     title,
			CharCount: charLen,
		})
		totalChars += charLen
	}

	if totalChars == 0 {
		totalChars = 1
	}

	running := 0
	for i := range chapters {
		chapters[i].StartPct = float64(running) / float64(totalChars)
		running += chapters[i].CharCount
		chapters[i].EndPct = float64(running) / float64(totalChars)
	}

	// Link TOC entries to spine indices
	var resolvedTOC []TOCEntry
	for _, t := range tocEntries {
		baseHref, frag := splitFragment(t.Href)
		spineIdx, ok := hrefToSpine[baseHref]
		if !ok {
			spineIdx, ok = hrefToSpine[resolveZipPath(opfDir, baseHref)]
		}
		if !ok {
			// Match by basename
			for k, v := range hrefToSpine {
				if path.Base(k) == path.Base(baseHref) {
					spineIdx = v
					ok = true
					break
				}
			}
		}
		if ok {
			resolvedTOC = append(resolvedTOC, TOCEntry{
				Title:      t.Title,
				Href:       baseHref,
				SpineIndex: spineIdx,
				Anchor:     frag,
			})
		}
	}

	if len(resolvedTOC) == 0 {
		for _, ch := range chapters {
			resolvedTOC = append(resolvedTOC, TOCEntry{
				Title:      ch.Title,
				Href:       ch.Href,
				SpineIndex: ch.Index,
			})
		}
	}

	pages := int(math.Max(1, math.Round(float64(totalChars)/1500.0)))
	return &BookManifest{
		BookID:       bookID,
		Title:        bookTitle,
		Authors:      bookAuthors,
		TotalChars:   totalChars,
		PageCount:    pages,
		Chapters:     chapters,
		TOC:          resolvedTOC,
		KOMD5Partial: koMD5,
	}, nil
}

// ChapterResponse holds sanitized chapter HTML and resolved styles for the Web Reader.
type ChapterResponse struct {
	BookID       int64   `json:"book_id"`
	Index        int     `json:"index"`
	Total        int     `json:"total"`
	Title        string  `json:"title"`
	HTML         string  `json:"html"`
	Styles       string  `json:"styles"`
	StartPct     float64 `json:"start_pct"`
	EndPct       float64 `json:"end_pct"`
	DocFragment  int     `json:"doc_fragment"` // 1-based KOReader DocFragment index
}

// GetChapterContent returns the rendered HTML and embedded CSS for a specific spine chapter index.
func GetChapterContent(epubPath string, bookID int64, spineIndex int) (*ChapterResponse, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	opfPath, pkg, err := readOPF(&zr.Reader)
	if err != nil {
		return nil, err
	}
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	manifest, err := GetBookManifest(epubPath, bookID, "", "", "")
	if err != nil {
		return nil, err
	}
	if len(manifest.Chapters) == 0 {
		return nil, fmt.Errorf("book has no readable chapters")
	}
	if spineIndex < 0 {
		spineIndex = 0
	}
	if spineIndex >= len(manifest.Chapters) {
		spineIndex = len(manifest.Chapters) - 1
	}

	ch := manifest.Chapters[spineIndex]
	chapterZipPath := resolveZipPath(opfDir, ch.Href)
	chapterDir := path.Dir(chapterZipPath)
	if chapterDir == "." {
		chapterDir = ""
	}

	rawBytes, err := readZipEntry(&zr.Reader, chapterZipPath)
	if err != nil {
		return nil, fmt.Errorf("read chapter %s: %w", chapterZipPath, err)
	}
	rawHTML := string(rawBytes)

	// Collect linked CSS stylesheets from this chapter
	var stylesBuilder strings.Builder
	cssLinkRe := regexp.MustCompile(`(?i)<link[^>]+href=["']([^"']+\.css)["'][^>]*>`)
	for _, match := range cssLinkRe.FindAllStringSubmatch(rawHTML, -1) {
		cssPath := resolveZipPath(chapterDir, match[1])
		if cssBytes, err := readZipEntry(&zr.Reader, cssPath); err == nil {
			stylesBuilder.WriteString(scopeChapterCSS(string(cssBytes), bookID, path.Dir(cssPath)))
			stylesBuilder.WriteString("\n")
		}
	}

	// Also extract inline <style> blocks
	styleBlockRe := regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	for _, match := range styleBlockRe.FindAllStringSubmatch(rawHTML, -1) {
		stylesBuilder.WriteString(scopeChapterCSS(match[1], bookID, chapterDir))
		stylesBuilder.WriteString("\n")
	}

	bodyHTML := extractBodyHTML(rawHTML)
	bodyHTML = rewriteChapterAssetURLs(bodyHTML, bookID, chapterDir, manifest.Chapters, opfDir)

	_ = pkg
	return &ChapterResponse{
		BookID:      bookID,
		Index:       spineIndex,
		Total:       len(manifest.Chapters),
		Title:       ch.Title,
		HTML:        bodyHTML,
		Styles:      stylesBuilder.String(),
		StartPct:    ch.StartPct,
		EndPct:      ch.EndPct,
		DocFragment: spineIndex + 1,
	}, nil
}

// ReadAsset reads an internal EPUB resource (image, font, stylesheet) by zip path.
func ReadAsset(epubPath, assetPath string) ([]byte, string, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, "", err
	}
	defer zr.Close()

	cleanPath := strings.TrimPrefix(path.Clean(assetPath), "/")
	data, err := readZipEntry(&zr.Reader, cleanPath)
	if err != nil {
		return nil, "", err
	}

	ext := strings.ToLower(filepath.Ext(cleanPath))
	mimeType := "application/octet-stream"
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".svg":
		mimeType = "image/svg+xml"
	case ".webp":
		mimeType = "image/webp"
	case ".css":
		mimeType = "text/css; charset=utf-8"
	case ".xhtml", ".html", ".htm":
		mimeType = "text/html; charset=utf-8"
	case ".ttf":
		mimeType = "font/ttf"
	case ".otf":
		mimeType = "font/otf"
	case ".woff":
		mimeType = "font/woff"
	case ".woff2":
		mimeType = "font/woff2"
	}
	return data, mimeType, nil
}

// RepackEPUBMetadata updates the OPF metadata (and optionally embeds a new cover image) inside the EPUB file.
// This ensures that downloading via OPDS or pushing to an Xteink X3 includes the edited metadata and cover.
func RepackEPUBMetadata(epubPath string, meta *ParsedEPUB, newCoverBytes []byte, newCoverExt string) error {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return err
	}

	opfPath, _, err := readOPF(&zr.Reader)
	if err != nil {
		zr.Close()
		return err
	}
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	rawOPFBytes, err := readZipEntry(&zr.Reader, opfPath)
	if err != nil {
		zr.Close()
		return err
	}

	var coverZipPath string
	var coverHref string
	var coverMime string
	if len(newCoverBytes) > 0 {
		if newCoverExt == "" {
			newCoverExt = ".png"
		}
		coverHref = "folio-cover" + newCoverExt
		coverZipPath = resolveZipPath(opfDir, coverHref)
		switch strings.ToLower(newCoverExt) {
		case ".jpg", ".jpeg":
			coverMime = "image/jpeg"
		case ".webp":
			coverMime = "image/webp"
		case ".svg":
			coverMime = "image/svg+xml"
		default:
			coverMime = "image/png"
		}
	}

	updatedOPF := updateOPFXML(string(rawOPFBytes), meta, coverHref, coverMime)

	tmpPath := epubPath + ".repack.tmp"
	outFile, err := os.Create(tmpPath)
	if err != nil {
		zr.Close()
		return err
	}
	zw := zip.NewWriter(outFile)

	// 1. Write mimetype first uncompressed if present
	for _, f := range zr.File {
		if f.Name == "mimetype" {
			rc, err := f.Open()
			if err == nil {
				w, err := zw.CreateHeader(&zip.FileHeader{
					Name:   "mimetype",
					Method: zip.Store,
				})
				if err == nil {
					_, _ = io.Copy(w, rc)
				}
				rc.Close()
			}
			break
		}
	}

	// 2. Copy all other entries, replacing OPF and optional cover
	for _, f := range zr.File {
		if f.Name == "mimetype" {
			continue
		}
		if coverZipPath != "" && f.Name == coverZipPath {
			continue
		}
		if f.Name == opfPath {
			w, err := zw.Create(opfPath)
			if err != nil {
				zw.Close()
				outFile.Close()
				zr.Close()
				return err
			}
			if _, err := w.Write([]byte(updatedOPF)); err != nil {
				zw.Close()
				outFile.Close()
				zr.Close()
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		w, err := zw.Create(f.Name)
		if err == nil {
			_, _ = io.Copy(w, rc)
		}
		rc.Close()
	}

	if coverZipPath != "" && len(newCoverBytes) > 0 {
		if w, err := zw.Create(coverZipPath); err == nil {
			_, _ = w.Write(newCoverBytes)
		}
	}

	if err := zw.Close(); err != nil {
		outFile.Close()
		zr.Close()
		return err
	}
	if err := outFile.Close(); err != nil {
		zr.Close()
		return err
	}
	zr.Close()

	return os.Rename(tmpPath, epubPath)
}

func updateOPFXML(rawOPF string, meta *ParsedEPUB, newCoverHref, newCoverMime string) string {
	// Replace <metadata ...> ... </metadata> with clean Dublin Core + Calibre series metadata while preserving existing unknown metas
	metaBlockRe := regexp.MustCompile(`(?is)<metadata[^>]*>.*?</metadata>`)
	var b strings.Builder
	b.WriteString(`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">` + "\n")
	b.WriteString(fmt.Sprintf("    <dc:title>%s</dc:title>\n", html.EscapeString(meta.Title)))
	if meta.Subtitle != "" {
		b.WriteString(fmt.Sprintf("    <dc:title>%s</dc:title>\n", html.EscapeString(meta.Subtitle)))
	}
	for _, a := range meta.Authors {
		if strings.TrimSpace(a) != "" {
			b.WriteString(fmt.Sprintf("    <dc:creator>%s</dc:creator>\n", html.EscapeString(strings.TrimSpace(a))))
		}
	}
	for _, t := range meta.Tags {
		if strings.TrimSpace(t) != "" {
			b.WriteString(fmt.Sprintf("    <dc:subject>%s</dc:subject>\n", html.EscapeString(strings.TrimSpace(t))))
		}
	}
	if meta.Description != "" {
		b.WriteString(fmt.Sprintf("    <dc:description>%s</dc:description>\n", html.EscapeString(meta.Description)))
	}
	if meta.Publisher != "" {
		b.WriteString(fmt.Sprintf("    <dc:publisher>%s</dc:publisher>\n", html.EscapeString(meta.Publisher)))
	}
	if meta.PublishedDate != "" {
		b.WriteString(fmt.Sprintf("    <dc:date>%s</dc:date>\n", html.EscapeString(meta.PublishedDate)))
	}
	lang := meta.Language
	if lang == "" {
		lang = "en"
	}
	b.WriteString(fmt.Sprintf("    <dc:language>%s</dc:language>\n", html.EscapeString(lang)))
	if meta.UUID != "" {
		b.WriteString(fmt.Sprintf("    <dc:identifier id=\"BookId\">urn:uuid:%s</dc:identifier>\n", html.EscapeString(meta.UUID)))
	}
	if meta.ISBN != "" {
		b.WriteString(fmt.Sprintf("    <dc:identifier opf:scheme=\"ISBN\">%s</dc:identifier>\n", html.EscapeString(meta.ISBN)))
	}
	if meta.Series != "" {
		b.WriteString(fmt.Sprintf("    <meta name=\"calibre:series\" content=\"%s\"/>\n", html.EscapeString(meta.Series)))
		if meta.SeriesIndex > 0 {
			b.WriteString(fmt.Sprintf("    <meta name=\"calibre:series_index\" content=\"%g\"/>\n", meta.SeriesIndex))
		}
	}
	if newCoverHref != "" {
		b.WriteString("    <meta name=\"cover\" content=\"folio-cover-img\"/>\n")
	}
	b.WriteString("  </metadata>")

	updated := metaBlockRe.ReplaceAllString(rawOPF, b.String())
	if newCoverHref != "" && !strings.Contains(updated, `id="folio-cover-img"`) {
		manifestItem := fmt.Sprintf("    <item id=\"folio-cover-img\" href=\"%s\" media-type=\"%s\" properties=\"cover-image\"/>\n  </manifest>",
			html.EscapeString(newCoverHref), html.EscapeString(newCoverMime))
		updated = strings.Replace(updated, "</manifest>", manifestItem, 1)
	}
	return updated
}

// GenerateTypographicCoverPNG generates a clean, literary cover PNG (480x720) with geometric bookplate motifs.
func GenerateTypographicCoverPNG(title, author, series string) []byte {
	const width, height = 480, 720
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Select a warm editorial palette from hash of title
	palettes := []struct {
		bg     color.RGBA
		frame  color.RGBA
		accent color.RGBA
	}{
		{color.RGBA{34, 48, 42, 255}, color.RGBA{222, 212, 193, 255}, color.RGBA{196, 98, 67, 255}},   // Forest & Terracotta
		{color.RGBA{46, 35, 32, 255}, color.RGBA{230, 218, 200, 255}, color.RGBA{204, 138, 69, 255}},  // Espresso & Ochre
		{color.RGBA{31, 41, 55, 255}, color.RGBA{225, 220, 209, 255}, color.RGBA{189, 83, 62, 255}},   // Slate Navy & Vermilion
		{color.RGBA{112, 46, 36, 255}, color.RGBA{242, 234, 219, 255}, color.RGBA{214, 172, 96, 255}},  // Carmine & Gold
	}
	h := 0
	for i := 0; i < len(title); i++ {
		h = (h*31 + int(title[i])) & 0x7fffffff
	}
	pal := palettes[h%len(palettes)]

	// Fill background
	draw.Draw(img, img.Bounds(), &image.Uniform{pal.bg}, image.Point{}, draw.Src)

	// Draw book spine shadow on left edge
	for x := 0; x < 18; x++ {
		alpha := uint8(60 - x*3)
		for y := 0; y < height; y++ {
			c := img.RGBAAt(x, y)
			c.R = uint8(max(0, int(c.R)-int(alpha/2)))
			c.G = uint8(max(0, int(c.G)-int(alpha/2)))
			c.B = uint8(max(0, int(c.B)-int(alpha/2)))
			img.SetRGBA(x, y, c)
		}
	}

	// Draw double editorial border frame
	drawRectBorder(img, 36, 36, width-72, height-72, 2, pal.frame)
	drawRectBorder(img, 44, 44, width-88, height-88, 1, pal.accent)

	// Draw central geometric emblem (Penguin/Criterion inspired geometric plate)
	cx, cy := width/2, height/2-20
	for r := 70; r <= 73; r++ {
		drawCircle(img, cx, cy, r, pal.accent)
	}
	drawCircleFilled(img, cx, cy, 42, pal.accent)
	drawCircleFilled(img, cx, cy, 18, pal.bg)

	// Draw horizontal rules above and below emblem
	draw.Draw(img, image.Rect(84, 175, width-84, 177), &image.Uniform{pal.frame}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(84, height-165, width-84, height-163), &image.Uniform{pal.frame}, image.Point{}, draw.Src)

	// Render crisp bitmap typography for Title & Author inside the cover plate
	drawBitmapTextCentered(img, strings.ToUpper(truncateStr(title, 24)), width/2, 95, 3, pal.frame)
	if len(title) > 24 {
		drawBitmapTextCentered(img, strings.ToUpper(truncateStr(title[24:], 24)), width/2, 130, 2, pal.frame)
	}
	if series != "" {
		drawBitmapTextCentered(img, strings.ToUpper(truncateStr(series, 28)), width/2, height-205, 2, pal.accent)
	}
	drawBitmapTextCentered(img, strings.ToUpper(truncateStr(author, 28)), width/2, height-120, 2, pal.frame)

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// Helper functions for EPUB parsing and HTML rewriting

func readZipEntry(zr *zip.Reader, target string) ([]byte, error) {
	target = strings.TrimPrefix(path.Clean(target), "/")
	if u, err := url.PathUnescape(target); err == nil {
		target = u
	}
	for _, f := range zr.File {
		name := strings.TrimPrefix(path.Clean(f.Name), "/")
		if name == target || strings.EqualFold(name, target) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, os.ErrNotExist
}

func resolveZipPath(baseDir, relHref string) string {
	relHref = stripFragment(relHref)
	if u, err := url.PathUnescape(relHref); err == nil {
		relHref = u
	}
	if strings.HasPrefix(relHref, "/") {
		return strings.TrimPrefix(path.Clean(relHref), "/")
	}
	if baseDir == "" || baseDir == "." {
		return strings.TrimPrefix(path.Clean(relHref), "/")
	}
	return strings.TrimPrefix(path.Clean(path.Join(baseDir, relHref)), "/")
}

func stripFragment(href string) string {
	if idx := strings.IndexByte(href, '#'); idx >= 0 {
		return href[:idx]
	}
	return href
}

func splitFragment(href string) (string, string) {
	if idx := strings.IndexByte(href, '#'); idx >= 0 {
		return href[:idx], href[idx+1:]
	}
	return href, ""
}

func parseTOC(zr *zip.Reader, opfDir string, pkg *xmlOPFPackage) []TOCEntry {
	// 1. Check EPUB 3 Nav document (properties="nav")
	for _, it := range pkg.Manifest.Items {
		if strings.Contains(strings.ToLower(it.Properties), "nav") {
			navPath := resolveZipPath(opfDir, it.Href)
			navDir := path.Dir(navPath)
			if navDir == "." {
				navDir = ""
			}
			if data, err := readZipEntry(zr, navPath); err == nil {
				entries := parseNavHTML(string(data), navDir, opfDir)
				if len(entries) > 0 {
					return entries
				}
			}
		}
	}

	// 2. Check EPUB 2 NCX document
	for _, it := range pkg.Manifest.Items {
		if it.ID == pkg.Spine.Toc || strings.Contains(strings.ToLower(it.MediaType), "dtbncx") || strings.HasSuffix(strings.ToLower(it.Href), ".ncx") {
			ncxPath := resolveZipPath(opfDir, it.Href)
			ncxDir := path.Dir(ncxPath)
			if ncxDir == "." {
				ncxDir = ""
			}
			if data, err := readZipEntry(zr, ncxPath); err == nil {
				var ncx xmlNCX
				if xml.Unmarshal(data, &ncx) == nil {
					var out []TOCEntry
					var walk func(pts []xmlNavPoint)
					walk = func(pts []xmlNavPoint) {
						for _, p := range pts {
							lbl := strings.TrimSpace(p.NavLabel.Text)
							src := strings.TrimSpace(p.Content.Src)
							if lbl != "" && src != "" {
								rel := resolveRelativeToOPF(ncxDir, opfDir, src)
								out = append(out, TOCEntry{Title: lbl, Href: rel})
							}
							walk(p.Children)
						}
					}
					walk(ncx.NavMap.NavPoints)
					if len(out) > 0 {
						return out
					}
				}
			}
		}
	}
	return nil
}

func parseNavHTML(rawNav, navDir, opfDir string) []TOCEntry {
	navRe := regexp.MustCompile(`(?is)<nav[^>]*toc[^>]*>(.*?)</nav>`)
	section := rawNav
	if m := navRe.FindStringSubmatch(rawNav); len(m) > 1 {
		section = m[1]
	}
	linkRe := regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	var out []TOCEntry
	for _, m := range linkRe.FindAllStringSubmatch(section, -1) {
		href := strings.TrimSpace(m[1])
		title := strings.TrimSpace(stripHTMLTags(m[2]))
		if href != "" && title != "" {
			rel := resolveRelativeToOPF(navDir, opfDir, href)
			out = append(out, TOCEntry{Title: title, Href: rel})
		}
	}
	return out
}

func resolveRelativeToOPF(fromDir, opfDir, href string) string {
	base, frag := splitFragment(href)
	zipFull := resolveZipPath(fromDir, base)
	if opfDir != "" && strings.HasPrefix(zipFull, opfDir+"/") {
		zipFull = strings.TrimPrefix(zipFull, opfDir+"/")
	}
	if frag != "" {
		return zipFull + "#" + frag
	}
	return zipFull
}

var tagStripRe = regexp.MustCompile(`<[^>]*>`)
var multiSpaceRe = regexp.MustCompile(`\s+`)

func stripHTMLTags(s string) string {
	noTags := tagStripRe.ReplaceAllString(s, " ")
	unesc := html.UnescapeString(noTags)
	return strings.TrimSpace(multiSpaceRe.ReplaceAllString(unesc, " "))
}

func cleanHTMLDescription(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return stripHTMLTags(s)
}

func extractHeadingFromHTML(raw string) string {
	re := regexp.MustCompile(`(?is)<(h1|h2|h3|title)[^>]*>(.*?)</(?:h1|h2|h3|title)>`)
	for _, m := range re.FindAllStringSubmatch(raw, 4) {
		txt := stripHTMLTags(m[2])
		if txt != "" && !strings.EqualFold(txt, "unknown") {
			return truncateStr(txt, 80)
		}
	}
	return ""
}

func extractBodyHTML(raw string) string {
	re := regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)
	if m := re.FindStringSubmatch(raw); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(raw)
}

func rewriteChapterAssetURLs(body string, bookID int64, chapterDir string, chapters []SpineChapter, opfDir string) string {
	// Rewrite src="..." and xlink:href="..." for images/media
	srcRe := regexp.MustCompile(`(?i)(\b(?:src|xlink:href)=["'])([^"']+)(["'])`)
	body = srcRe.ReplaceAllStringFunc(body, func(full string) string {
		m := srcRe.FindStringSubmatch(full)
		if len(m) < 4 {
			return full
		}
		target := strings.TrimSpace(m[2])
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "data:") {
			return full
		}
		resolved := resolveZipPath(chapterDir, target)
		return fmt.Sprintf(`%s/api/books/%d/asset/%s%s`, m[1], bookID, resolved, m[3])
	})

	// Rewrite internal chapter links <a href="..."> to data-folio-spine attributes so the Web Reader navigates smoothly
	hrefRe := regexp.MustCompile(`(?i)(<a\b[^>]*?\bhref=["'])([^"']+)(["'])`)
	body = hrefRe.ReplaceAllStringFunc(body, func(full string) string {
		m := hrefRe.FindStringSubmatch(full)
		if len(m) < 4 {
			return full
		}
		target := strings.TrimSpace(m[2])
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
			return full
		}
		if strings.HasPrefix(target, "#") {
			return full
		}
		base, frag := splitFragment(target)
		resolvedZip := resolveZipPath(chapterDir, base)
		for _, ch := range chapters {
			chZip := resolveZipPath(opfDir, ch.Href)
			if chZip == resolvedZip || path.Base(chZip) == path.Base(resolvedZip) {
				return fmt.Sprintf(`%s#%s" data-spine-index="%d" data-anchor="%s`, m[1], frag, ch.Index, frag)
			}
		}
		return full
	})

	return body
}

func scopeChapterCSS(css string, bookID int64, cssDir string) string {
	urlRe := regexp.MustCompile(`(?i)url\(['"]?([^'")]+)['"]?\)`)
	css = urlRe.ReplaceAllStringFunc(css, func(full string) string {
		m := urlRe.FindStringSubmatch(full)
		if len(m) < 2 {
			return full
		}
		t := strings.TrimSpace(m[1])
		if strings.HasPrefix(t, "data:") || strings.HasPrefix(t, "http") {
			return full
		}
		resolved := resolveZipPath(cssDir, t)
		return fmt.Sprintf(`url("/api/books/%d/asset/%s")`, bookID, resolved)
	})
	return css
}

func isLikelyISBN(s string) bool {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "-", ""), " ", "")
	if len(cleaned) != 10 && len(cleaned) != 13 {
		return false
	}
	for i, ch := range cleaned {
		if i == 9 && (ch == 'X' || ch == 'x') {
			continue
		}
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

// Drawing helpers for cover art generation
func drawRectBorder(img *image.RGBA, x, y, w, h, thickness int, c color.RGBA) {
	u := &image.Uniform{c}
	draw.Draw(img, image.Rect(x, y, x+w, y+thickness), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(x, y+h-thickness, x+w, y+h), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(x, y, x+thickness, y+h), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(x+w-thickness, y, x+w, y+h), u, image.Point{}, draw.Src)
}

func drawCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for angle := 0.0; angle < 2*math.Pi; angle += 0.005 {
		px := cx + int(math.Round(float64(r)*math.Cos(angle)))
		py := cy + int(math.Round(float64(r)*math.Sin(angle)))
		img.SetRGBA(px, py, c)
	}
}

func drawCircleFilled(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(cx+dx, cy+dy, c)
			}
		}
	}
}

var glyphs5x7 = map[rune][7]uint8{
	'A': {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'B': {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E},
	'C': {0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E},
	'D': {0x1E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1E},
	'E': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F},
	'F': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10},
	'G': {0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0F},
	'H': {0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'I': {0x0E, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'J': {0x07, 0x02, 0x02, 0x02, 0x12, 0x12, 0x0C},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F},
	'M': {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O': {0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'P': {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
	'Q': {0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D},
	'R': {0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11},
	'S': {0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E},
	'T': {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x1B, 0x11},
	'X': {0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0A, 0x04, 0x04, 0x04, 0x04},
	'Z': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F},
	'0': {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	'1': {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'2': {0x0E, 0x11, 0x01, 0x06, 0x08, 0x10, 0x1F},
	'3': {0x1F, 0x02, 0x04, 0x06, 0x01, 0x11, 0x0E},
	'4': {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	'5': {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	'6': {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	'7': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	'9': {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
	':': {0x00, 0x04, 0x04, 0x00, 0x04, 0x04, 0x00},
	'-': {0x00, 0x00, 0x00, 0x1F, 0x00, 0x00, 0x00},
	'.': {0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x06},
	'\'': {0x04, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00},
}

func drawBitmapTextCentered(img *image.RGBA, text string, centerX, topY, scale int, col color.RGBA) {
	runes := []rune(text)
	charWidth := 6 * scale
	totalW := len(runes) * charWidth
	startX := centerX - totalW/2

	for idx, ch := range runes {
		glyph, ok := glyphs5x7[ch]
		if !ok {
			continue
		}
		gx := startX + idx*charWidth
		for row := 0; row < 7; row++ {
			bits := glyph[row]
			for colIdx := 0; colIdx < 5; colIdx++ {
				if (bits & (1 << (4 - colIdx))) != 0 {
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							img.SetRGBA(gx+colIdx*scale+dx, topY+row*scale+dy, col)
						}
					}
				}
			}
		}
	}
}
