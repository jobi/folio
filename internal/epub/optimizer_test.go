package epub

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func createTestPNG(w, h int, withMargin bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if withMargin && (x < w/4 || x > w*3/4 || y < h/4 || y > h*3/4) {
				img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: 40, G: 100, B: 180, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestOptimizeEPUBFullPipeline(t *testing.T) {
	tmpDir := t.TempDir()
	inPath := filepath.Join(tmpDir, "source.epub")
	outPath := filepath.Join(tmpDir, "optimized.epub")

	// Build a synthetic EPUB containing:
	// - Large PNG cover wrapped in <svg><image .../></svg>
	// - Oversized content PNG with uniform white margins + <img> with hardcoded width/height
	// - Inline base64 data:image/png URI
	// - Embedded .ttf font + @font-face CSS rule
	// - Page-list nav
	// - Oversized paragraph (> 2000 bytes) and oversized chapter (> 11000 bytes) with #anchor2 in the second half
	coverPNG := createTestPNG(1200, 1800, false)
	figurePNG := createTestPNG(1000, 1400, true)
	tinyPNG := createTestPNG(64, 64, false)
	tinyB64 := base64.StdEncoding.EncodeToString(tinyPNG)

	bigSentence := "The quick brown fox jumps over the lazy dog near the quiet river bank while the autumn leaves fall gently. "
	var bigPara strings.Builder
	for bigPara.Len() < 2400 {
		bigPara.WriteString(bigSentence)
	}

	var ch1Body strings.Builder
	ch1Body.WriteString(`<h1 id="top">Chapter One</h1>`)
	ch1Body.WriteString(`<p id="p1">` + bigPara.String() + `</p>`)
	ch1Body.WriteString(`<p><img src="images/figure.png" width="1000" height="1400" style="width:1000px; height:1400px; border:1px solid black"/></p>`)
	ch1Body.WriteString(`<p><img src="data:image/png;base64,` + tinyB64 + `"/></p>`)
	for i := 0; i < 4; i++ {
		ch1Body.WriteString(fmt.Sprintf(`<p>Section %d: %s</p>`, i, bigPara.String()))
	}
	ch1Body.WriteString(`<h2 id="anchor-late">Late Subheading</h2><p>Final paragraph after anchor. See <a href="#anchor-late">local link</a>.</p>`)
	ch1Body.WriteString(`<nav epub:type="page-list"><ol><li><a href="ch1.xhtml#top">1</a></li></ol></nav>`)

	var epubBuf bytes.Buffer
	zw := zip.NewWriter(&epubBuf)

	writeFile := func(name, content string, raw []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if raw != nil {
			_, _ = w.Write(raw)
		} else {
			_, _ = w.Write([]byte(content))
		}
	}

	writeFile("mimetype", "application/epub+zip", nil)
	writeFile("META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`, nil)

	writeFile("OEBPS/content.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="BookId">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="BookId">urn:uuid:12345678-1234-1234-1234-123456789abc</dc:identifier>
    <dc:title>CrossPoint Optimizer Test</dc:title>
    <dc:creator>Test Author</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
    <item id="font1" href="fonts/custom.ttf" media-type="font/ttf"/>
    <item id="cover-img" href="images/cover.png" media-type="image/png" properties="cover-image"/>
    <item id="fig-img" href="images/figure.png" media-type="image/png"/>
    <item id="cover-page" href="cover.xhtml" media-type="application/xhtml+xml" properties="svg"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="cover-page"/>
    <itemref idref="ch1"/>
  </spine>
</package>`, nil)

	writeFile("OEBPS/toc.ncx", `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="mismatch-old-id"/>
  </head>
  <docTitle><text>CrossPoint Optimizer Test</text></docTitle>
  <navMap>
    <navPoint id="nav1" playOrder="1">
      <navLabel><text>Late Subheading</text></navLabel>
      <content src="ch1.xhtml#anchor-late"/>
    </navPoint>
  </navMap>
</ncx>`, nil)

	writeFile("OEBPS/style.css", `@font-face { font-family: "Custom"; src: url("fonts/custom.ttf"); }
body { background: url("images/figure.png"); }`, nil)

	writeFile("OEBPS/fonts/custom.ttf", "dummy-font-bytes", nil)
	writeFile("OEBPS/images/cover.png", "", coverPNG)
	writeFile("OEBPS/images/figure.png", "", figurePNG)

	writeFile("OEBPS/cover.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Cover</title></head>
<body>
  <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 1200 1800">
    <image width="1200" height="1800" xlink:href="images/cover.png"/>
  </svg>
</body>
</html>`, nil)

	writeFile("OEBPS/ch1.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 1</title><link rel="stylesheet" href="style.css"/></head>
<body>`+ch1Body.String()+`</body>
</html>`, nil)

	_ = zw.Close()
	if err := writeOrderedZip(inPath, map[string][]byte{}, map[string]uint16{}, nil); err != nil {
		t.Fatalf("init test: %v", err)
	}
	// Write our test zip bytes to inPath
	zrTest, err := zip.NewReader(bytes.NewReader(epubBuf.Bytes()), int64(epubBuf.Len()))
	if err != nil {
		t.Fatalf("read test zip: %v", err)
	}
	testEntries := map[string][]byte{}
	testMethods := map[string]uint16{}
	var testOrder []string
	for _, f := range zrTest.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		testEntries[f.Name] = b
		testMethods[f.Name] = zip.Deflate
		testOrder = append(testOrder, f.Name)
	}
	if err := writeOrderedZip(inPath, testEntries, testMethods, testOrder); err != nil {
		t.Fatalf("write source epub: %v", err)
	}

	opts := DefaultOptimizeOptions()
	opts.DeviceTarget = "X3"
	opts.AutoCrop = true
	opts.SplitText = true

	summary, err := OptimizeEPUB(inPath, outPath, opts)
	if err != nil {
		t.Fatalf("OptimizeEPUB failed: %v", err)
	}

	if summary.Profile != "X3" {
		t.Fatalf("expected profile X3, got %s", summary.Profile)
	}
	if summary.Images != 2 {
		t.Fatalf("expected 2 raster images processed, got %d", summary.Images)
	}
	if summary.Cropped != 1 {
		t.Fatalf("expected 1 non-cover image auto-cropped, got %d", summary.Cropped)
	}
	if summary.Fonts != 1 {
		t.Fatalf("expected 1 embedded font removed, got %d", summary.Fonts)
	}
	if summary.DataImgs != 1 {
		t.Fatalf("expected 1 base64 data-URI image extracted, got %d", summary.DataImgs)
	}
	if summary.ParaSplits == 0 || summary.FileSplits == 0 {
		t.Fatalf("expected paragraph and file splits, got paras=%d files=%d", summary.ParaSplits, summary.FileSplits)
	}

	// Inspect optimized archive
	zrOut, err := zip.OpenReader(outPath)
	if err != nil {
		t.Fatalf("open optimized epub: %v", err)
	}
	defer zrOut.Close()

	if len(zrOut.File) == 0 || zrOut.File[0].Name != "mimetype" || zrOut.File[0].Method != zip.Store {
		t.Fatalf("expected mimetype first with zip.Store")
	}

	outFiles := map[string]*zip.File{}
	outContent := map[string][]byte{}
	for _, f := range zrOut.File {
		outFiles[f.Name] = f
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		outContent[f.Name] = b
	}

	// 1. Verify images were renamed to .jpg, downscaled to fit X3 (528x792), and stored with zip.Store
	coverFile, ok := outFiles["OEBPS/images/cover.jpg"]
	if !ok {
		t.Fatalf("expected OEBPS/images/cover.jpg in optimized archive")
	}
	if coverFile.Method != zip.Store {
		t.Fatalf("expected cover.jpg to be stored uncompressed (zip.Store)")
	}
	coverImg, _, err := image.Decode(bytes.NewReader(outContent["OEBPS/images/cover.jpg"]))
	if err != nil {
		t.Fatalf("decode optimized cover.jpg: %v", err)
	}
	if coverImg.Bounds().Dx() > 528 || coverImg.Bounds().Dy() > 792 {
		t.Fatalf("expected cover dimensions <= 528x792, got %dx%d", coverImg.Bounds().Dx(), coverImg.Bounds().Dy())
	}

	// 2. Verify SVG cover wrapper was unwrapped and defensive style injected
	coverXHTML := string(outContent["OEBPS/cover.xhtml"])
	if strings.Contains(coverXHTML, "<svg") || !strings.Contains(coverXHTML, `src="images/cover.jpg"`) {
		t.Fatalf("expected SVG cover to be unwrapped to <img src=\"images/cover.jpg\">, got:\n%s", coverXHTML)
	}
	if !strings.Contains(coverXHTML, defensiveStyle) {
		t.Fatalf("expected defensive style in cover.xhtml")
	}

	// 3. Verify embedded font removed & @font-face stripped from CSS
	if _, exists := outFiles["OEBPS/fonts/custom.ttf"]; exists {
		t.Fatalf("expected custom.ttf font to be removed")
	}
	cssText := string(outContent["OEBPS/style.css"])
	if strings.Contains(cssText, "@font-face") || !strings.Contains(cssText, "images/figure.jpg") {
		t.Fatalf("expected @font-face stripped and .png ref renamed in style.css, got:\n%s", cssText)
	}

	// 4. Verify NCX identifier synced and fragment anchor remapped to split chunk
	ncxText := string(outContent["OEBPS/toc.ncx"])
	if !strings.Contains(ncxText, `content="urn:uuid:12345678-1234-1234-1234-123456789abc"`) {
		t.Fatalf("expected NCX dtb:uid to be synced to OPF identifier, got:\n%s", ncxText)
	}
	if !strings.Contains(ncxText, "_ek") || !strings.Contains(ncxText, "#anchor-late") {
		t.Fatalf("expected NCX #anchor-late link to be remapped to split chunk _ek*, got:\n%s", ncxText)
	}

	// 5. Verify position map was generated and status is "mapped"
	posMapBytes, ok := outContent[positionMapPath]
	if !ok {
		t.Fatalf("expected %s in optimized archive", positionMapPath)
	}
	var posMap map[string]any
	if err := json.Unmarshal(posMapBytes, &posMap); err != nil {
		t.Fatalf("decode position map: %v", err)
	}
	if posMap["status"] != "mapped" {
		t.Fatalf("expected position map status 'mapped', got %v (%s)", posMap["status"], string(posMapBytes))
	}
}
