package epub

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DeviceProfile defines the screen resolution for a CrossPoint Reader model.
type DeviceProfile struct {
	Label  string `json:"label"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

var DeviceProfiles = map[string]DeviceProfile{
	"X4": {Label: "X4", Width: 480, Height: 800},
	"X3": {Label: "X3", Width: 528, Height: 792},
}

const DefaultDeviceProfile = "X4"
const DefaultJPEGQuality = 85

// Auto-crop tuning constants (mirroring crosspoint_reader/optimizer.py)
const (
	cropWhiteThreshold      = 245
	cropBackgroundTolerance = 28
	cropBackgroundMaxSpread = 24.0
	cropEdgeSampleSize      = 12
	cropPaddingPx           = 8
	minCropSavingsRatio     = 0.08
	minColorCropSavings     = 0.20
	minCropDimension        = 240
)

// Text splitting constants (mirroring crosspoint_reader/textsplit.py)
const (
	splitLimit  = 9500 // split <body> content bigger than this
	chunkTarget = 7000 // aim for files of this many bytes of body content
	paraLimit   = 1600 // split <p> whose inner content exceeds this
	paraTarget  = 1200 // aim for paragraphs of this many bytes
)

const (
	positionMapPath        = "META-INF/crosspoint-position-map.json"
	positionMapUnavailable = `{"version":1,"status":"unavailable"}`
	defensiveStyle         = `<style type="text/css">img,svg{max-width:100%;height:auto}body{overflow-wrap:break-word}table{max-width:100%;table-layout:fixed}pre,code{white-space:pre-wrap;word-wrap:break-word}*{box-sizing:border-box}</style>`
)

var (
	rasterRe    = regexp.MustCompile(`(?i)\.(png|gif|webp|bmp|jpe?g)$`)
	renameRe    = regexp.MustCompile(`(?i)\.(png|gif|webp|bmp|jpeg)$`)
	refRe       = regexp.MustCompile(`(?i)\.(?:png|gif|webp|bmp|jpeg)(?:["'\)\s#?>])`)
	xhtmlRe     = regexp.MustCompile(`(?i)\.(xhtml|html|htm)$`)
	fontRe      = regexp.MustCompile(`(?i)\.(otf|ttf|woff2?)$`)
	coverNameRe = regexp.MustCompile(`(?i)(^|/)(cover|thumbnail|thumb|icon)[^/]*\.(jpe?g|png|gif|webp|bmp)$`)
	svgImgRe    = regexp.MustCompile(`(?is)<(?:svg:)?svg\b[^>]*>[\s\S]*?<(?:svg:)?image\b[^>]*?(?:xlink:href|href)=["']([^"']+)["'][\s\S]*?</(?:svg:)?svg>`)
	imgTagRe    = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	widthAttrRe = regexp.MustCompile(`(?i)\s+(?:width|height)\s*=\s*(?:"[^"]*"|'[^']*')`)
	styleAttrRe = regexp.MustCompile(`(?is)\s+style=(?:"([^"]*)"|'([^']*)')`)
	blockTagRe  = regexp.MustCompile(`(?i)<(?:p|div|table|ul|ol)\b`)
	dataURIRe   = regexp.MustCompile(`(?i)(src|xlink:href)="data:image/(png|jpe?g|gif|webp|bmp);base64,([^"]+)"`)
	pageListRe  = regexp.MustCompile(`(?is)<nav[^>]*epub:type=["']page-list["'].*?</nav>`)
	fontFaceRe  = regexp.MustCompile(`(?is)@font-face\s*\{[^}]*\}\s*`)
)

// OptimizeOptions controls CrossPoint EPUB optimization.
type OptimizeOptions struct {
	DeviceTarget   string `json:"device_target"`   // "auto", "X3", "X4"
	DetectedDevice string `json:"detected_device"` // e.g. "X3" or "X4" from /api/status
	Quality        int    `json:"quality"`         // 1..100 (default 85)
	Grayscale      bool   `json:"grayscale"`       // default true
	AutoCrop       bool   `json:"auto_crop"`       // default false
	SplitText      bool   `json:"split_text"`      // default true
}

// DefaultOptimizeOptions returns the recommended CrossPoint Reader optimization settings.
func DefaultOptimizeOptions() OptimizeOptions {
	return OptimizeOptions{
		DeviceTarget: "auto",
		Quality:      DefaultJPEGQuality,
		Grayscale:    true,
		AutoCrop:     false,
		SplitText:    true,
	}
}

// OptimizeSummary reports the results of optimizing an EPUB for CrossPoint Reader.
type OptimizeSummary struct {
	Name       string   `json:"name"`
	OrigSize   int64    `json:"orig_size"`
	NewSize    int64    `json:"new_size"`
	Images     int      `json:"images"`
	Cropped    int      `json:"cropped"`
	Errors     int      `json:"errors"`
	Fixes      int      `json:"fixes"`
	ParaSplits int      `json:"para_splits"`
	FileSplits int      `json:"file_splits"`
	Fonts      int      `json:"fonts_removed"`
	DataImgs   int      `json:"data_imgs"`
	Profile    string   `json:"profile"`
	Steps      []string `json:"steps"`
	ElapsedMs  int64    `json:"elapsed_ms"`
}

// ResolveProfile resolves "auto" | "X4" | "X3" (+ detected device string) to a concrete DeviceProfile.
func ResolveProfile(deviceTarget, detectedDevice string) DeviceProfile {
	norm := func(s string) string {
		u := strings.ToUpper(strings.TrimSpace(s))
		if strings.Contains(u, "X3") {
			return "X3"
		}
		if strings.Contains(u, "X4") {
			return "X4"
		}
		return u
	}
	if key := norm(deviceTarget); key != "" && key != "AUTO" {
		if p, ok := DeviceProfiles[key]; ok {
			return p
		}
	}
	if key := norm(detectedDevice); key != "" {
		if p, ok := DeviceProfiles[key]; ok {
			return p
		}
	}
	return DeviceProfiles[DefaultDeviceProfile]
}

type imageMeta struct {
	origW, origH     int
	origSize         int
	finalW, finalH   int
	finalSize        int
	cropped          bool
}

// flattenWhiteRGB composites any alpha/transparency onto a solid white background.
func flattenWhiteRGB(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

func estimateCropBackground(rgb *image.RGBA) *[3]float64 {
	b := rgb.Bounds()
	w, h := b.Dx(), b.Dy()
	sample := cropEdgeSampleSize
	if w/8 < sample {
		sample = w / 8
	}
	if h/8 < sample {
		sample = h / 8
	}
	if sample < 2 {
		return nil
	}
	pts := [][2]int{
		{0, 0}, {w - sample, 0}, {0, h - sample}, {w - sample, h - sample},
		{(w - sample) / 2, 0}, {(w - sample) / 2, h - sample},
		{0, (h - sample) / 2}, {w - sample, (h - sample) / 2},
	}
	var means [8][3]float64
	var avg [3]float64
	count := float64(sample * sample)
	for i, pt := range pts {
		var sr, sg, sb float64
		for y := pt[1]; y < pt[1]+sample; y++ {
			for x := pt[0]; x < pt[0]+sample; x++ {
				off := rgb.PixOffset(x, y)
				sr += float64(rgb.Pix[off])
				sg += float64(rgb.Pix[off+1])
				sb += float64(rgb.Pix[off+2])
			}
		}
		means[i] = [3]float64{sr / count, sg / count, sb / count}
		avg[0] += means[i][0]
		avg[1] += means[i][1]
		avg[2] += means[i][2]
	}
	avg[0] /= 8.0
	avg[1] /= 8.0
	avg[2] /= 8.0

	maxSpread := 0.0
	for _, m := range means {
		for c := 0; c < 3; c++ {
			d := math.Abs(m[c] - avg[c])
			if d > maxSpread {
				maxSpread = d
			}
		}
	}
	if maxSpread > cropBackgroundMaxSpread {
		return nil
	}
	return &avg
}

func findCropBox(rgb *image.RGBA) *image.Rectangle {
	b := rgb.Bounds()
	w, h := b.Dx(), b.Dy()
	bg := estimateCropBackground(rgb)

	minX, minY := w, h
	maxX, maxY := -1, -1

	var bgR, bgG, bgB int
	if bg != nil {
		bgR = int(math.Round(bg[0]))
		bgG = int(math.Round(bg[1]))
		bgB = int(math.Round(bg[2]))
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := rgb.PixOffset(x, y)
			r := int(rgb.Pix[off])
			g := int(rgb.Pix[off+1])
			bl := int(rgb.Pix[off+2])

			fg := false
			if bg != nil {
				dr := r - bgR
				if dr < 0 {
					dr = -dr
				}
				dg := g - bgG
				if dg < 0 {
					dg = -dg
				}
				db := bl - bgB
				if db < 0 {
					db = -db
				}
				if dr > cropBackgroundTolerance || dg > cropBackgroundTolerance || db > cropBackgroundTolerance {
					fg = true
				}
			} else {
				if r < cropWhiteThreshold || g < cropWhiteThreshold || bl < cropWhiteThreshold {
					fg = true
				}
			}
			if fg {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	if maxX < minX || maxY < minY {
		return nil
	}

	left := minX - cropPaddingPx
	if left < 0 {
		left = 0
	}
	top := minY - cropPaddingPx
	if top < 0 {
		top = 0
	}
	right := maxX + 1 + cropPaddingPx
	if right > w {
		right = w
	}
	bottom := maxY + 1 + cropPaddingPx
	if bottom > h {
		bottom = h
	}

	cropW, cropH := right-left, bottom-top
	savedRatio := 1.0 - float64(cropW*cropH)/float64(w*h)
	nearWhite := bg != nil && bg[0] >= cropWhiteThreshold && bg[1] >= cropWhiteThreshold && bg[2] >= cropWhiteThreshold
	minRatio := minColorCropSavings
	if bg == nil || nearWhite {
		minRatio = minCropSavingsRatio
	}
	if savedRatio < minRatio {
		return nil
	}
	rect := image.Rect(left, top, right, bottom)
	return &rect
}

func shouldSkipAutoCrop(imagePath string, w, h int) bool {
	if w < minCropDimension || h < minCropDimension {
		return true
	}
	return coverNameRe.MatchString(imagePath)
}

// resizeRGBA scales src to (dstW, dstH) using area-averaging / bilinear interpolation.
func resizeRGBA(src *image.RGBA, dstW, dstH int) *image.RGBA {
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW == dstW && srcH == dstH {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	scaleX := float64(srcW) / float64(dstW)
	scaleY := float64(srcH) / float64(dstH)

	for dy := 0; dy < dstH; dy++ {
		sy0 := float64(dy) * scaleY
		sy1 := float64(dy+1) * scaleY
		iy0 := int(math.Floor(sy0))
		iy1 := int(math.Ceil(sy1))
		if iy0 < 0 {
			iy0 = 0
		}
		if iy1 > srcH {
			iy1 = srcH
		}
		if iy1 <= iy0 {
			iy1 = iy0 + 1
			if iy1 > srcH {
				iy1 = srcH
				iy0 = srcH - 1
			}
		}

		for dx := 0; dx < dstW; dx++ {
			sx0 := float64(dx) * scaleX
			sx1 := float64(dx+1) * scaleX
			ix0 := int(math.Floor(sx0))
			ix1 := int(math.Ceil(sx1))
			if ix0 < 0 {
				ix0 = 0
			}
			if ix1 > srcW {
				ix1 = srcW
			}
			if ix1 <= ix0 {
				ix1 = ix0 + 1
				if ix1 > srcW {
					ix1 = srcW
					ix0 = srcW - 1
				}
			}

			var rSum, gSum, bSum, wSum float64
			for sy := iy0; sy < iy1; sy++ {
				wy := 1.0
				if sy1 > sy0 {
					top := math.Max(sy0, float64(sy))
					bot := math.Min(sy1, float64(sy+1))
					if bot > top {
						wy = bot - top
					}
				}
				for sx := ix0; sx < ix1; sx++ {
					wx := 1.0
					if sx1 > sx0 {
						left := math.Max(sx0, float64(sx))
						right := math.Min(sx1, float64(sx+1))
						if right > left {
							wx = right - left
						}
					}
					weight := wx * wy
					off := src.PixOffset(sb.Min.X+sx, sb.Min.Y+sy)
					rSum += float64(src.Pix[off]) * weight
					gSum += float64(src.Pix[off+1]) * weight
					bSum += float64(src.Pix[off+2]) * weight
					wSum += weight
				}
			}

			dOff := dst.PixOffset(dx, dy)
			if wSum > 0 {
				dst.Pix[dOff] = uint8(math.Min(255, math.Max(0, math.Round(rSum/wSum))))
				dst.Pix[dOff+1] = uint8(math.Min(255, math.Max(0, math.Round(gSum/wSum))))
				dst.Pix[dOff+2] = uint8(math.Min(255, math.Max(0, math.Round(bSum/wSum))))
				dst.Pix[dOff+3] = 255
			}
		}
	}
	return dst
}

// rgbaToGrayITU601 converts an RGBA image to 8-bit grayscale using ITU-R 601 luma weights (0.299R + 0.587G + 0.114B).
func rgbaToGrayITU601(src *image.RGBA) *image.Gray {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	gray := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := src.PixOffset(b.Min.X+x, b.Min.Y+y)
			r := float64(src.Pix[off])
			g := float64(src.Pix[off+1])
			bl := float64(src.Pix[off+2])
			luma := uint8(math.Min(255, math.Max(0, math.Round(0.299*r+0.587*g+0.114*bl))))
			gray.Pix[gray.PixOffset(x, y)] = luma
		}
	}
	return gray
}

func processImage(data []byte, profile DeviceProfile, opts OptimizeOptions, imagePath string) ([]byte, imageMeta, error) {
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, imageMeta{}, err
	}
	rgb := flattenWhiteRGB(im)
	origW, origH := rgb.Bounds().Dx(), rgb.Bounds().Dy()
	origSize := len(data)
	maxW, maxH := profile.Width, profile.Height

	src := rgb
	srcW, srcH := origW, origH
	cropped := false

	if opts.AutoCrop && !shouldSkipAutoCrop(imagePath, origW, origH) {
		if box := findCropBox(rgb); box != nil {
			croppedImg := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
			draw.Draw(croppedImg, croppedImg.Bounds(), rgb, box.Min, draw.Src)
			src = croppedImg
			srcW, srcH = box.Dx(), box.Dy()
			cropped = true
		}
	}

	var scaled *image.RGBA
	finalW, finalH := srcW, srcH
	fits := srcW <= maxW && srcH <= maxH
	if fits && !cropped {
		scaled = src
	} else {
		scale := math.Min(float64(maxW)/float64(srcW), float64(maxH)/float64(srcH))
		finalW = int(math.Max(1, math.Round(float64(srcW)*scale)))
		finalH = int(math.Max(1, math.Round(float64(srcH)*scale)))
		scaled = resizeRGBA(src, finalW, finalH)
	}

	quality := opts.Quality
	if quality <= 0 || quality > 100 {
		quality = DefaultJPEGQuality
	}

	var buf bytes.Buffer
	if opts.Grayscale {
		gray := rgbaToGrayITU601(scaled)
		if err := jpeg.Encode(&buf, gray, &jpeg.Options{Quality: quality}); err != nil {
			return nil, imageMeta{}, err
		}
	} else {
		if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality}); err != nil {
			return nil, imageMeta{}, err
		}
	}

	outBytes := buf.Bytes()
	return outBytes, imageMeta{
		origW:     origW,
		origH:     origH,
		origSize:  origSize,
		finalW:    finalW,
		finalH:    finalH,
		finalSize: len(outBytes),
		cropped:   cropped,
	}, nil
}

func renamedImagePath(p string) string {
	return renameRe.ReplaceAllString(p, ".jpg")
}

func renameBasenameRefs(text string) string {
	return refRe.ReplaceAllStringFunc(text, func(m string) string {
		lastChar := m[len(m)-1:]
		return ".jpg" + lastChar
	})
}

func stripImgDimsAndFixSrc(text string) string {
	return imgTagRe.ReplaceAllStringFunc(text, func(tag string) string {
		tag = widthAttrRe.ReplaceAllString(tag, "")
		tag = styleAttrRe.ReplaceAllStringFunc(tag, func(sm string) string {
			sub := styleAttrRe.FindStringSubmatch(sm)
			if len(sub) < 3 {
				return sm
			}
			quote := `"`
			style := sub[1]
			if style == "" && strings.Contains(sm, "'") {
				quote = `'`
				style = sub[2]
			}
			parts := strings.Split(style, ";")
			var kept []string
			modified := false
			for _, part := range parts {
				kv := strings.SplitN(part, ":", 2)
				if len(kv) == 2 {
					prop := strings.ToLower(strings.TrimSpace(kv[0]))
					if prop == "width" || prop == "height" {
						modified = true
						continue
					}
				}
				if strings.TrimSpace(part) != "" {
					kept = append(kept, strings.TrimSpace(part))
				}
			}
			if !modified {
				return sm
			}
			if len(kept) == 0 {
				return ""
			}
			return fmt.Sprintf(" style=%s%s%s", quote, strings.Join(kept, ";"), quote)
		})
		return tag
	})
}

func unwrapSVGImages(content string) (string, int) {
	if !strings.Contains(strings.ToLower(content), "<svg") || !strings.Contains(strings.ToLower(content), "href") {
		return content, 0
	}
	count := 0
	out := svgImgRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := svgImgRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		count++
		src := renamedImagePath(sub[1])
		return fmt.Sprintf(`<img style="max-width:100%%;height:auto" src="%s" alt="" />`, src)
	})
	return out, count
}

func fixXHTML(text string, logFn func(tag, msg string)) (string, int) {
	fixes := 0
	newText, n := unwrapSVGImages(text)
	if n > 0 {
		text = newText
		fixes += n
		logFn("FIX", fmt.Sprintf("SVG images (%d)", n))
	}

	text = renameBasenameRefs(text)
	text = stripImgDimsAndFixSrc(text)
	text = pageListRe.ReplaceAllString(text, "")

	if strings.Contains(text, "</head>") && !strings.Contains(text, defensiveStyle) {
		text = strings.Replace(text, "</head>", defensiveStyle+"</head>", 1)
	}
	return text, fixes
}

var (
	opfItemTagRe       = regexp.MustCompile(`(?is)<(?:\w+:)?item\b[^>]*>`)
	opfMediaTypeRe     = regexp.MustCompile(`(?i)\bmedia-type=(?:"[^"]*"|'[^']*')`)
	opfPropertiesRe    = regexp.MustCompile(`(?i)\s+properties=(?:"[^"]*"|'[^']*')`)
	opfCoverMetaRe     = regexp.MustCompile(`(?i)<(?:\w+:)?meta\b[^>]*\bname=["']cover["'][^>]*>`)
	opfMetadataCloseRe = regexp.MustCompile(`(?i)</(?:\w+:)?metadata\s*>`)
)

func findAttrValue(tag, attr string) string {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(attr) + `\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	m := re.FindStringSubmatch(tag)
	if len(m) >= 3 {
		if m[1] != "" {
			return m[1]
		}
		return m[2]
	}
	return ""
}

func fixOPF(text string, logFn func(tag, msg string)) string {
	text = renameBasenameRefs(text)

	var coverID string
	var fallbackCoverID string

	text = opfItemTagRe.ReplaceAllStringFunc(text, func(tag string) string {
		href := findAttrValue(tag, "href")
		id := findAttrValue(tag, "id")
		props := findAttrValue(tag, "properties")

		if strings.HasSuffix(strings.ToLower(href), ".jpg") || renameRe.MatchString(href) {
			tag = opfMediaTypeRe.ReplaceAllString(tag, `media-type="image/jpeg"`)
		}

		mt := findAttrValue(tag, "media-type")
		if strings.HasPrefix(strings.ToLower(mt), "image/") {
			if strings.Contains(props, "cover-image") && coverID == "" {
				coverID = id
			}
			if fallbackCoverID == "" && (strings.Contains(strings.ToLower(id), "cover") || strings.Contains(strings.ToLower(href), "cover")) {
				fallbackCoverID = id
			}
		}

		if props != "" && strings.Contains(props, "svg") {
			var kept []string
			for _, p := range strings.Fields(props) {
				if p != "svg" {
					kept = append(kept, p)
				}
			}
			if len(kept) == 0 {
				tag = opfPropertiesRe.ReplaceAllString(tag, "")
			} else {
				tag = opfPropertiesRe.ReplaceAllString(tag, fmt.Sprintf(` properties="%s"`, strings.Join(kept, " ")))
			}
		}
		return tag
	})

	if coverID == "" {
		coverID = fallbackCoverID
	}
	if coverID != "" && !opfCoverMetaRe.MatchString(text) {
		if opfMetadataCloseRe.MatchString(text) {
			text = opfMetadataCloseRe.ReplaceAllStringFunc(text, func(closeTag string) string {
				logFn("FIX", "OPF cover meta")
				return fmt.Sprintf(`  <meta name="cover" content="%s"/>`+"\n  "+closeTag, coverID)
			})
		}
	}

	return text
}

var (
	ncxUidRe        = regexp.MustCompile(`(?i)(<meta\s+name=["']dtb:uid["']\s+content=)["'][^"']*["']`)
	opfUniqueIDRe   = regexp.MustCompile(`(?i)<(?:\w+:)?package[^>]*unique-identifier=["']([^"']+)["']`)
	opfAnyIdentRe   = regexp.MustCompile(`(?is)<dc:identifier[^>]*>([^<]+)</dc:identifier>`)
)

func extractOPFIdentifier(opfText string) string {
	if m := opfUniqueIDRe.FindStringSubmatch(opfText); len(m) >= 2 {
		uid := regexp.QuoteMeta(m[1])
		idRe := regexp.MustCompile(`(?is)<dc:identifier[^>]*\bid=["']` + uid + `["'][^>]*>([^<]+)</dc:identifier>`)
		if m2 := idRe.FindStringSubmatch(opfText); len(m2) >= 2 {
			return strings.TrimSpace(m2[1])
		}
	}
	if m3 := opfAnyIdentRe.FindStringSubmatch(opfText); len(m3) >= 2 {
		return strings.TrimSpace(m3[1])
	}
	return ""
}

func syncNCXIdentifier(text, identifier string) string {
	if identifier == "" {
		return text
	}
	return ncxUidRe.ReplaceAllString(text, `${1}"`+identifier+`"`)
}

// --- Text & Spine Splitting for Low-RAM CrossPoint Firmware ---

var voidTags = map[string]bool{
	"meta": true, "link": true, "img": true, "br": true, "hr": true,
	"image": true, "input": true, "col": true, "source": true,
}

var inlineWrapTags = map[string]bool{
	"span": true, "em": true, "i": true, "b": true, "strong": true,
	"a": true, "u": true, "small": true, "sub": true, "sup": true,
}

var tagNameRe = regexp.MustCompile(`^</?([a-zA-Z0-9_:-]+)`)

// parseNodes splits HTML/XHTML markup into top-level balanced nodes (tags or text runs).
func parseNodes(s string) ([]string, error) {
	var nodes []string
	i, n := 0, len(s)
	for i < n {
		if s[i] == '<' {
			depth, j := 0, i
			for {
				k := strings.IndexByte(s[j:], '>')
				if k == -1 {
					return nil, fmt.Errorf("unterminated tag")
				}
				k += j
				tag := s[j : k+1]
				var name string
				if m := tagNameRe.FindStringSubmatch(tag); len(m) >= 2 {
					name = strings.ToLower(m[1])
					if idx := strings.IndexByte(name, ':'); idx >= 0 {
						name = name[idx+1:]
					}
				}
				if strings.HasPrefix(tag, "<!--") {
					endComment := strings.Index(s[j:], "-->")
					if endComment == -1 {
						return nil, fmt.Errorf("unterminated comment")
					}
					k = j + endComment + 2
				} else if strings.HasPrefix(tag, "</") {
					depth--
				} else if !strings.HasSuffix(tag, "/>") && !voidTags[name] && !strings.HasPrefix(tag, "<!") && !strings.HasPrefix(tag, "<?") {
					depth++
				}
				if depth <= 0 {
					nodes = append(nodes, s[i:k+1])
					i = k + 1
					break
				}
				nextLt := strings.IndexByte(s[k:], '<')
				if nextLt == -1 {
					return nil, fmt.Errorf("unbalanced markup")
				}
				j = k + nextLt
			}
		} else {
			j := strings.IndexByte(s[i:], '<')
			if j == -1 {
				nodes = append(nodes, s[i:])
				i = n
			} else {
				nodes = append(nodes, s[i:i+j])
				i += j
			}
		}
	}
	return nodes, nil
}

// splitTextSentences splits a text run into pieces of ~target bytes at sentence/space boundaries.
func splitTextSentences(text string, target int) []string {
	var pieces []string
	for len(text) > target {
		cut := strings.LastIndex(text[:target], ". ")
		if cut != -1 {
			cut += 2
		} else {
			cut = strings.LastIndexByte(text[:target], ' ') + 1
			if cut <= 0 {
				break
			}
		}
		pieces = append(pieces, text[:cut])
		text = text[cut:]
	}
	pieces = append(pieces, text)
	return pieces
}

var outerElementRe = regexp.MustCompile(`(?is)^(<([a-zA-Z0-9_:-]+)\b[^>]*>)(.*)(</([a-zA-Z0-9_:-]+)\s*>)$`)

func groupInline(inner string) ([]string, error) {
	tokens, err := parseNodes(inner)
	if err != nil {
		return nil, err
	}
	var groups []string
	var cur []string
	curLen := 0

	hasNonEmpty := func(parts []string) bool {
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				return true
			}
		}
		return false
	}

	for _, tok := range tokens {
		var pieces []string
		if len(tok) > paraLimit && !strings.HasPrefix(tok, "<") {
			pieces = splitTextSentences(tok, paraTarget)
		} else if len(tok) > paraLimit && strings.HasPrefix(tok, "<") {
			m := outerElementRe.FindStringSubmatch(tok)
			if len(m) >= 6 && strings.EqualFold(m[2], m[5]) && inlineWrapTags[strings.ToLower(m[2])] && !blockTagRe.MatchString(m[3]) {
				subGroups, err := groupInline(m[3])
				if err != nil {
					pieces = []string{tok}
				} else {
					for _, g := range subGroups {
						pieces = append(pieces, m[1]+g+m[4])
					}
				}
			} else {
				pieces = []string{tok}
			}
		} else {
			pieces = []string{tok}
		}

		for _, piece := range pieces {
			if curLen+len(piece) > paraTarget && strings.TrimSpace(piece) != "" && hasNonEmpty(cur) {
				groups = append(groups, strings.Join(cur, ""))
				cur = nil
				curLen = 0
			}
			cur = append(cur, piece)
			curLen += len(piece)
		}
	}
	if len(cur) > 0 {
		groups = append(groups, strings.Join(cur, ""))
	}
	return groups, nil
}

var (
	paraOuterRe = regexp.MustCompile(`(?is)^(<p\b[^>]*>)(.*)(</p\s*>)$`)
	idAttrStrip = regexp.MustCompile(`(?i)\s+(?:xml:)?id=(?:"[^"]*"|'[^']*')`)
)

func splitBigParagraphs(htmlStr string) (string, bool, error) {
	nodes, err := parseNodes(htmlStr)
	if err != nil {
		return htmlStr, false, err
	}
	var out []string
	changed := false

	for _, node := range nodes {
		var m []string
		if strings.HasPrefix(strings.ToLower(node), "<p") {
			m = paraOuterRe.FindStringSubmatch(node)
		}
		if len(m) < 4 || len(m[2]) <= paraLimit || blockTagRe.MatchString(m[2]) {
			if strings.HasPrefix(node, "<") && !strings.HasPrefix(strings.ToLower(node), "<p") &&
				!strings.HasPrefix(node, "<!") && !strings.HasPrefix(node, "</") && len(node) > paraLimit {
				mm := outerElementRe.FindStringSubmatch(node)
				if len(mm) >= 6 && strings.EqualFold(mm[2], mm[5]) && !voidTags[strings.ToLower(mm[2])] {
					inner, was, err := splitBigParagraphs(mm[3])
					if err == nil && was {
						out = append(out, mm[1]+inner+mm[4])
						changed = true
						continue
					}
				}
			}
			out = append(out, node)
			continue
		}

		openTag, inner, closeTag := m[1], m[2], m[3]
		groups, err := groupInline(inner)
		if err != nil || len(groups) < 2 {
			out = append(out, node)
			continue
		}
		contTag := idAttrStrip.ReplaceAllString(openTag, "")
		var b strings.Builder
		for k, g := range groups {
			if k == 0 {
				b.WriteString(openTag)
			} else {
				b.WriteString(contTag)
			}
			b.WriteString(g)
			b.WriteString(closeTag)
		}
		out = append(out, b.String())
		changed = true
	}
	return strings.Join(out, ""), changed, nil
}

func chunkNodes(nodes []string, target int) ([]string, error) {
	var chunks []string
	var cur []string
	curLen := 0

	flush := func() {
		content := strings.Join(cur, "")
		if strings.TrimSpace(content) != "" {
			chunks = append(chunks, content)
		} else if len(chunks) > 0 {
			chunks[len(chunks)-1] += content
		} else {
			return
		}
		cur = nil
		curLen = 0
	}

	for _, node := range nodes {
		if len(node) > splitLimit {
			flush()
			var pieces []string
			if strings.HasPrefix(node, "<") {
				mm := outerElementRe.FindStringSubmatch(node)
				if len(mm) >= 6 && strings.EqualFold(mm[2], mm[5]) {
					innerNodes, err := parseNodes(mm[3])
					if err != nil {
						return nil, err
					}
					subChunks, err := chunkNodes(innerNodes, target)
					if err != nil {
						return nil, err
					}
					for _, sub := range subChunks {
						pieces = append(pieces, mm[1]+sub+mm[4])
					}
				} else {
					pieces = []string{node}
				}
			} else {
				pieces = splitTextSentences(node, target)
			}
			if len(pieces) > 0 && len(cur) > 0 {
				pieces[0] = strings.Join(cur, "") + pieces[0]
				cur = nil
				curLen = 0
			}
			chunks = append(chunks, pieces...)
			continue
		}
		if curLen+len(node) > target && curLen > 0 && strings.TrimSpace(node) != "" {
			flush()
		}
		cur = append(cur, node)
		curLen += len(node)
	}
	flush()
	if len(cur) > 0 {
		chunks = append(chunks, strings.Join(cur, ""))
	}
	return chunks, nil
}

var bodyExtractRe = regexp.MustCompile(`(?is)(<body\b[^>]*>)(.*)(</body\s*>)`)

type xhtmlChunk struct {
	relName string
	content string
}

func splitXHTMLDoc(doc, baseName string) ([]xhtmlChunk, error) {
	loc := bodyExtractRe.FindStringSubmatchIndex(doc)
	if len(loc) < 8 {
		return nil, nil
	}
	body := doc[loc[4]:loc[5]]
	if len(body) <= splitLimit {
		return nil, nil
	}
	head := doc[:loc[4]]
	tail := doc[loc[5]:]
	nodes, err := parseNodes(body)
	if err != nil {
		return nil, err
	}
	chunks, err := chunkNodes(nodes, chunkTarget)
	if err != nil || len(chunks) < 2 {
		return nil, err
	}

	ext := path.Ext(baseName)
	base := strings.TrimSuffix(baseName, ext)
	out := make([]xhtmlChunk, 0, len(chunks))
	for k, chunk := range chunks {
		name := baseName
		if k > 0 {
			name = fmt.Sprintf("%s_ek%d%s", base, k, ext)
		}
		out = append(out, xhtmlChunk{
			relName: name,
			content: head + chunk + tail,
		})
	}
	return out, nil
}

var (
	htmlCommentRe = regexp.MustCompile(`(?is)<!--.*?-->`)
	anyTagRe      = regexp.MustCompile(`<[^>]*>`)
	wsCollapseRe  = regexp.MustCompile(`\s+`)
)

func visibleText(s string) string {
	body := s
	if m := bodyExtractRe.FindStringSubmatch(s); len(m) >= 3 {
		body = m[2]
	}
	body = pageListRe.ReplaceAllString(body, "")
	body = htmlCommentRe.ReplaceAllString(body, "")
	body = anyTagRe.ReplaceAllString(body, " ")
	return strings.TrimSpace(wsCollapseRe.ReplaceAllString(body, " "))
}

// OptimizeEPUB transforms an EPUB at inPath into a CrossPoint-optimized EPUB at outPath.
// It downscales images to the device screen (X3 528x792 or X4 480x800), converts them to
// grayscale JPEG (stored uncompressed for fast ESP32 decoding), unwraps SVG covers,
// injects defensive CSS, strips embedded fonts, and splits oversized paragraphs and chapters.
func OptimizeEPUB(inPath, outPath string, opts OptimizeOptions) (*OptimizeSummary, error) {
	start := time.Now()
	profile := ResolveProfile(opts.DeviceTarget, opts.DetectedDevice)
	if opts.Quality <= 0 || opts.Quality > 100 {
		opts.Quality = DefaultJPEGQuality
	}

	st, err := os.Stat(inPath)
	if err != nil {
		return nil, err
	}
	origSize := st.Size()

	summary := &OptimizeSummary{
		Name:     filepath.Base(inPath),
		OrigSize: origSize,
		NewSize:  origSize,
		Profile:  profile.Label,
	}
	logStep := func(tag, msg string) {
		summary.Steps = append(summary.Steps, fmt.Sprintf("[%s] %s", tag, msg))
	}

	logStep("INFO", fmt.Sprintf("%s (%s) — target %s %dx%d, quality %d%%, grayscale %s, auto-crop %s",
		summary.Name, humanBytes(origSize), profile.Label, profile.Width, profile.Height, opts.Quality,
		onOff(opts.Grayscale), onOff(opts.AutoCrop)))

	zr, err := zip.OpenReader(inPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	// Read all entries into memory map + ordered slice for clean multi-pass processing
	entries := make(map[string][]byte, len(zr.File))
	storeMethod := make(map[string]uint16, len(zr.File))
	var order []string
	sourceEntries := make(map[string][]byte, len(zr.File))

	var opfName string
	var identifier string

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		sourceEntries[f.Name] = raw
		if strings.HasSuffix(strings.ToLower(f.Name), ".opf") && opfName == "" {
			opfName = f.Name
			identifier = extractOPFIdentifier(string(raw))
		}
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if opts.SplitText && name == positionMapPath {
			continue
		}
		data, ok := sourceEntries[name]
		if !ok {
			continue
		}
		low := strings.ToLower(name)

		if name == "mimetype" {
			entries[name] = data
			storeMethod[name] = zip.Store
			order = append(order, name)
			continue
		}

		if rasterRe.MatchString(low) {
			jpegBytes, meta, err := processImage(data, profile, opts, name)
			if err == nil {
				summary.Images++
				if meta.cropped {
					summary.Cropped++
				}
				cropNote := ""
				if meta.cropped {
					cropNote = " [cropped]"
				}
				logStep("IMG", fmt.Sprintf("%s (%dx%d %s) -> %dx%d %s%s",
					path.Base(name), meta.origW, meta.origH, humanBytes(int64(meta.origSize)),
					meta.finalW, meta.finalH, humanBytes(int64(meta.finalSize)), cropNote))
				newName := renamedImagePath(name)
				entries[newName] = jpegBytes
				storeMethod[newName] = zip.Store
				order = append(order, newName)
			} else {
				summary.Errors++
				logStep("IMG-ERR", fmt.Sprintf("%s: %v (kept original)", path.Base(name), err))
				entries[name] = data
				storeMethod[name] = zip.Store
				order = append(order, name)
			}
		} else if xhtmlRe.MatchString(low) {
			text, fixes := fixXHTML(string(data), logStep)
			summary.Fixes += fixes
			entries[name] = []byte(text)
			storeMethod[name] = zip.Deflate
			order = append(order, name)
		} else if strings.HasSuffix(low, ".opf") {
			text := fixOPF(string(data), logStep)
			entries[name] = []byte(text)
			storeMethod[name] = zip.Deflate
			order = append(order, name)
		} else if strings.HasSuffix(low, ".ncx") {
			text := renameBasenameRefs(string(data))
			newText := syncNCXIdentifier(text, identifier)
			if newText != text {
				logStep("FIX", "NCX identifier synced")
				summary.Fixes++
			}
			entries[name] = []byte(newText)
			storeMethod[name] = zip.Deflate
			order = append(order, name)
		} else if strings.HasSuffix(low, ".css") {
			text := renameBasenameRefs(string(data))
			entries[name] = []byte(text)
			storeMethod[name] = zip.Deflate
			order = append(order, name)
		} else {
			entries[name] = data
			storeMethod[name] = zip.Deflate
			order = append(order, name)
		}
	}

	if opts.SplitText {
		entries[positionMapPath] = []byte(positionMapUnavailable)
		storeMethod[positionMapPath] = zip.Deflate
		order = append(order, positionMapPath)
		runTextSplitPass(entries, storeMethod, &order, opfName, sourceEntries, profile, opts, summary, logStep)
	}

	// Write final OCF-compliant EPUB (mimetype first, uncompressed)
	if err := writeOrderedZip(outPath, entries, storeMethod, order); err != nil {
		return nil, err
	}

	if stOut, err := os.Stat(outPath); err == nil {
		summary.NewSize = stOut.Size()
	}
	summary.ElapsedMs = time.Since(start).Milliseconds()
	saved := summary.OrigSize - summary.NewSize
	pct := 0.0
	if summary.OrigSize > 0 {
		pct = float64(saved) / float64(summary.OrigSize) * 100.0
	}
	logStep("DONE", fmt.Sprintf("Optimized %d image(s), %d fix(es) — %s -> %s (%+.0f%%) in %dms",
		summary.Images, summary.Fixes, humanBytes(summary.OrigSize), humanBytes(summary.NewSize), -pct, summary.ElapsedMs))

	return summary, nil
}

var (
	opfItemRefRe     = regexp.MustCompile(`(?is)<(?:\w+:)?itemref\b[^>]*\bidref=["']([^"']+)["'][^>]*>`)
	opfManifestEndRe = regexp.MustCompile(`(?i)</((?:\w+:)?)manifest\s*>`)
	anchorIDRe       = regexp.MustCompile(`\bid="([^"]+)"`)
	hrefFragRe       = regexp.MustCompile(`((?:href|src)=")([^"#]+)#([^"]+)"`)
	localFragRe      = regexp.MustCompile(`(href=")#([^"]+)"`)
	fontItemRemoveRe = regexp.MustCompile(`(?i)\s*<(?:\w+:)?item\b[^>]*\.(?:otf|ttf|woff2?)[^>]*/?>`)
)

func runTextSplitPass(
	entries map[string][]byte,
	storeMethod map[string]uint16,
	order *[]string,
	opfName string,
	sourceEntries map[string][]byte,
	profile DeviceProfile,
	opts OptimizeOptions,
	summary *OptimizeSummary,
	logStep func(tag, msg string),
) {
	if opfName == "" {
		return
	}
	originalOPFBytes, ok := entries[opfName]
	if !ok {
		return
	}
	opf := string(originalOPFBytes)
	opfDir := path.Dir(opfName)
	if opfDir == "." {
		opfDir = ""
	}

	type manifestEntry struct {
		href string
		tag  string
	}
	items := make(map[string]manifestEntry)
	for _, tag := range opfItemTagRe.FindAllString(opf, -1) {
		id := findAttrValue(tag, "id")
		href := findAttrValue(tag, "href")
		if id != "" && href != "" {
			items[id] = manifestEntry{href: href, tag: tag}
		}
	}

	var spineIDs []string
	for _, m := range opfItemRefRe.FindAllStringSubmatch(opf, -1) {
		if len(m) >= 2 {
			spineIDs = append(spineIDs, m[1])
		}
	}

	itemTagName := "item"
	if m := opfManifestEndRe.FindStringSubmatch(opf); len(m) >= 2 && m[1] != "" {
		itemTagName = m[1] + "item"
	}

	type anchorKey struct {
		base string
		frag string
	}
	anchorMap := make(map[anchorKey]string)
	chunkOrigin := make(map[string]string)
	fragments := make(map[string][]string)
	reverted := make(map[string]bool)

	for _, sid := range spineIDs {
		it, exists := items[sid]
		if !exists {
			continue
		}
		zname := path.Clean(it.href)
		if opfDir != "" {
			zname = path.Clean(path.Join(opfDir, it.href))
		}
		docBytes, exists := entries[zname]
		if !exists || !xhtmlRe.MatchString(zname) {
			continue
		}
		doc := string(docBytes)

		// Pass 0: extract base64 data-URI images into real optimized JPEG files
		if strings.Contains(doc, "data:image/") {
			zdir := path.Dir(zname)
			if zdir == "." {
				zdir = ""
			}
			doc = dataURIRe.ReplaceAllStringFunc(doc, func(m string) string {
				sub := dataURIRe.FindStringSubmatch(m)
				if len(sub) < 4 {
					return m
				}
				attrName := sub[1]
				ext := strings.ToLower(sub[2])
				b64Clean := wsCollapseRe.ReplaceAllString(sub[3], "")
				raw, err := base64.StdEncoding.DecodeString(b64Clean)
				if err != nil {
					return m
				}
				name := fmt.Sprintf("dataimg_%d", summary.DataImgs)
				summary.DataImgs++
				outData := raw
				outExt := ext
				if outExt == "jpeg" {
					outExt = "jpg"
				}
				mt := "image/" + outExt
				if outExt == "jpg" {
					mt = "image/jpeg"
				}
				if jpegBytes, _, err := processImage(raw, profile, opts, name); err == nil {
					outData = jpegBytes
					outExt = "jpg"
					mt = "image/jpeg"
				}
				fname := fmt.Sprintf("%s.%s", name, outExt)
				newZName := fname
				if zdir != "" {
					newZName = path.Join(zdir, fname)
				}
				entries[newZName] = outData
				storeMethod[newZName] = zip.Store
				*order = append(*order, newZName)

				relHref := newZName
				if opfDir != "" && strings.HasPrefix(newZName, opfDir+"/") {
					relHref = strings.TrimPrefix(newZName, opfDir+"/")
				}
				newItemTag := fmt.Sprintf(`<%s id="%s" href="%s" media-type="%s"/>`, itemTagName, name, relHref, mt)
				opf = opfManifestEndRe.ReplaceAllString(opf, newItemTag+"\n  $0")
				logStep("SPLIT", fmt.Sprintf("extracted %.1f KB data-URI image -> %s", float64(len(raw))/1024.0, fname))
				return fmt.Sprintf(`%s="%s"`, attrName, fname)
			})
			entries[zname] = []byte(doc)
		}

		origText := visibleText(doc)

		// Pass 1: paragraph splitting
		if loc := bodyExtractRe.FindStringSubmatchIndex(doc); len(loc) >= 8 {
			bodyContent := doc[loc[4]:loc[5]]
			newBody, changed, err := splitBigParagraphs(bodyContent)
			if err != nil {
				reverted[zname] = true
			} else if changed {
				newDoc := doc[:loc[4]] + newBody + doc[loc[5]:]
				if visibleText(newDoc) == origText {
					doc = newDoc
					entries[zname] = []byte(doc)
					summary.ParaSplits++
					summary.Fixes++
				} else {
					reverted[zname] = true
				}
			}
		}

		// Pass 2: spine XHTML file splitting
		pieces, err := splitXHTMLDoc(doc, path.Base(zname))
		if err != nil {
			reverted[zname] = true
			continue
		}
		if len(pieces) == 0 {
			continue
		}
		var visParts []string
		for _, p := range pieces {
			visParts = append(visParts, visibleText(p.content))
		}
		joinedVis := strings.TrimSpace(wsCollapseRe.ReplaceAllString(strings.Join(visParts, " "), " "))
		if joinedVis != origText {
			reverted[zname] = true
			continue
		}

		zdir := path.Dir(zname)
		if zdir == "." {
			zdir = ""
		}
		hdir := path.Dir(it.href)
		if hdir == "." {
			hdir = ""
		}

		partNames := make([]string, len(pieces))
		collision := false
		for k, p := range pieces {
			pn := p.relName
			if zdir != "" {
				pn = path.Join(zdir, p.relName)
			}
			partNames[k] = pn
			if k > 0 {
				if _, exists := entries[pn]; exists {
					collision = true
					break
				}
				if _, exists := items[fmt.Sprintf("%s_ek%d", sid, k)]; exists {
					collision = true
					break
				}
			}
		}
		if collision {
			reverted[zname] = true
			continue
		}

		fragments[zname] = partNames
		origBase := path.Base(it.href)

		for k, p := range pieces {
			newZName := partNames[k]
			entries[newZName] = []byte(p.content)
			storeMethod[newZName] = zip.Deflate
			chunkOrigin[newZName] = origBase
			if k > 0 {
				*order = append(*order, newZName)
			}
			for _, m := range anchorIDRe.FindAllStringSubmatch(p.content, -1) {
				if len(m) >= 2 {
					anchorMap[anchorKey{base: origBase, frag: m[1]}] = p.relName
				}
			}
		}

		newItems := []string{it.tag}
		var newIDs []string
		maxChunkLen := 0
		for k, p := range pieces {
			if len(p.content) > maxChunkLen {
				maxChunkLen = len(p.content)
			}
			if k == 0 {
				continue
			}
			pieceHref := p.relName
			if hdir != "" {
				pieceHref = path.Join(hdir, p.relName)
			}
			pieceID := fmt.Sprintf("%s_ek%d", sid, k)
			newIDs = append(newIDs, pieceID)
			newItems = append(newItems, fmt.Sprintf(`<%s id="%s" href="%s" media-type="application/xhtml+xml"/>`, itemTagName, pieceID, pieceHref))
		}

		opf = strings.Replace(opf, it.tag, strings.Join(newItems, "\n    "), 1)
		refRe := regexp.MustCompile(`(?is)(<(?:\w+:)?itemref\b[^>]*\s+idref=["'])` + regexp.QuoteMeta(sid) + `(["'][^>]*>(?:\s*</(?:\w+:)?itemref\s*>)?)`)
		opf = refRe.ReplaceAllStringFunc(opf, func(m string) string {
			sub := refRe.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			refs := []string{m}
			for _, pid := range newIDs {
				cont := sub[1] + pid + sub[2]
				cont = idAttrStrip.ReplaceAllString(cont, "")
				refs = append(refs, cont)
			}
			return strings.Join(refs, "\n    ")
		})

		summary.FileSplits++
		summary.Fixes++
		logStep("SPLIT", fmt.Sprintf("%s -> %d files (max %d B)", path.Base(zname), len(pieces), maxChunkLen))
	}

	// Remap fragment links (#anchor) across all XHTML and NCX files, and drop page-list navs
	for _, n := range *order {
		low := strings.ToLower(n)
		if !xhtmlRe.MatchString(low) && !strings.HasSuffix(low, ".ncx") {
			continue
		}
		b, ok := entries[n]
		if !ok {
			continue
		}
		s := string(b)
		before := s

		if len(anchorMap) > 0 {
			s = hrefFragRe.ReplaceAllStringFunc(s, func(m string) string {
				sub := hrefFragRe.FindStringSubmatch(m)
				if len(sub) < 4 {
					return m
				}
				pre, fname, frag := sub[1], sub[2], sub[3]
				if newBase, ok := anchorMap[anchorKey{base: path.Base(fname), frag: frag}]; ok && newBase != path.Base(fname) {
					d := path.Dir(fname)
					if d != "" && d != "." {
						fname = path.Join(d, newBase)
					} else {
						fname = newBase
					}
				}
				return fmt.Sprintf(`%s%s#%s"`, pre, fname, frag)
			})

			selfOrigin := chunkOrigin[n]
			if selfOrigin == "" {
				selfOrigin = path.Base(n)
			}
			s = localFragRe.ReplaceAllStringFunc(s, func(m string) string {
				sub := localFragRe.FindStringSubmatch(m)
				if len(sub) < 3 {
					return m
				}
				pre, frag := sub[1], sub[2]
				if newBase, ok := anchorMap[anchorKey{base: selfOrigin, frag: frag}]; ok && newBase != path.Base(n) {
					return fmt.Sprintf(`%s%s#%s"`, pre, newBase, frag)
				}
				return m
			})
		}

		s = pageListRe.ReplaceAllString(s, "")
		if s != before {
			entries[n] = []byte(s)
		}
	}

	// Remove embedded fonts and @font-face CSS rules
	var keptOrder []string
	for _, n := range *order {
		if fontRe.MatchString(n) {
			delete(entries, n)
			delete(storeMethod, n)
			summary.Fonts++
			summary.Fixes++
			continue
		}
		if strings.HasSuffix(strings.ToLower(n), ".css") {
			if b, ok := entries[n]; ok {
				s := string(b)
				s2 := fontFaceRe.ReplaceAllString(s, "")
				if s2 != s {
					entries[n] = []byte(s2)
				}
			}
		}
		keptOrder = append(keptOrder, n)
	}
	*order = keptOrder

	if summary.Fonts > 0 {
		opf = fontItemRemoveRe.ReplaceAllString(opf, "")
		logStep("SPLIT", fmt.Sprintf("removed %d embedded font(s)", summary.Fonts))
	}
	if summary.ParaSplits > 0 {
		logStep("SPLIT", fmt.Sprintf("split oversized paragraphs in %d file(s)", summary.ParaSplits))
	}

	entries[opfName] = []byte(opf)
	entries[positionMapPath] = buildPositionMap(sourceEntries, entries, opfName, originalOPFBytes, fragments, reverted)
}

func buildPositionMap(
	source map[string][]byte,
	entries map[string][]byte,
	opfName string,
	originalOPF []byte,
	fragments map[string][]string,
	reverted map[string]bool,
) []byte {
	sourceSpine, err1 := parseSpinePaths(source[opfName], opfName)
	origSpine, err2 := parseSpinePaths(originalOPF, opfName)
	optSpine, err3 := parseSpinePaths(entries[opfName], opfName)
	if err1 != nil || err2 != nil || err3 != nil || len(sourceSpine) == 0 {
		return []byte(positionMapUnavailable)
	}
	if len(sourceSpine) != len(origSpine) {
		return []byte(positionMapUnavailable)
	}
	for i := range sourceSpine {
		if sourceSpine[i] != origSpine[i] {
			return []byte(positionMapUnavailable)
		}
	}

	var flatExpected []string
	groups := make([][]string, len(sourceSpine))
	for i, name := range sourceSpine {
		if g, ok := fragments[name]; ok && len(g) > 0 {
			groups[i] = g
		} else {
			groups[i] = []string{name}
		}
		flatExpected = append(flatExpected, groups[i]...)
	}
	if len(optSpine) != len(flatExpected) {
		return []byte(positionMapUnavailable)
	}
	for i := range optSpine {
		if optSpine[i] != flatExpected[i] {
			return []byte(positionMapUnavailable)
		}
	}

	var ranges []any
	var sourceText []any
	allNil := true
	identity := len(fragments) == 0

	for idx, name := range sourceSpine {
		group := groups[idx]
		if reverted[name] {
			for range group {
				ranges = append(ranges, nil)
			}
			sourceText = append(sourceText, nil)
			identity = false
			continue
		}
		srcBytes, ok := source[name]
		if !ok {
			for range group {
				ranges = append(ranges, nil)
			}
			sourceText = append(sourceText, nil)
			identity = false
			continue
		}
		srcText := visibleText(string(srcBytes))
		var partTexts []string
		validGroup := true
		for _, partName := range group {
			pb, ok := entries[partName]
			if !ok {
				validGroup = false
				break
			}
			partTexts = append(partTexts, visibleText(string(pb)))
		}
		joinedPartText := strings.TrimSpace(wsCollapseRe.ReplaceAllString(strings.Join(partTexts, " "), " "))
		if !validGroup || joinedPartText != srcText {
			for range group {
				ranges = append(ranges, nil)
			}
			sourceText = append(sourceText, nil)
			identity = false
			continue
		}

		sourceText = append(sourceText, [][]any{{"/text()[1]", len(srcText)}})
		startOffset := 0
		for _, pt := range partTexts {
			ranges = append(ranges, []int{idx, startOffset, len(pt)})
			startOffset += len(pt)
			allNil = false
		}
	}

	if allNil {
		return []byte(positionMapUnavailable)
	}
	status := "mapped"
	if identity {
		status = "identity"
	} else {
		for _, r := range ranges {
			if r == nil {
				status = "partial"
				break
			}
		}
	}
	payload := map[string]any{
		"version":    1,
		"status":     status,
		"sourceText": sourceText,
		"spine":      ranges,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return []byte(positionMapUnavailable)
	}
	return b
}

func parseSpinePaths(opfBytes []byte, opfName string) ([]string, error) {
	if len(opfBytes) == 0 {
		return nil, fmt.Errorf("empty opf")
	}
	var pkg xmlOPFPackage
	if err := xml.Unmarshal(opfBytes, &pkg); err != nil {
		return nil, err
	}
	manifest := make(map[string]string, len(pkg.Manifest.Items))
	for _, it := range pkg.Manifest.Items {
		if it.ID == "" || it.Href == "" {
			return nil, fmt.Errorf("invalid manifest item")
		}
		manifest[it.ID] = it.Href
	}
	opfDir := path.Dir(opfName)
	if opfDir == "." {
		opfDir = ""
	}
	var names []string
	for _, ref := range pkg.Spine.Itemrefs {
		href, ok := manifest[ref.IDRef]
		if !ok {
			return nil, fmt.Errorf("missing spine idref")
		}
		u, err := url.Parse(href)
		if err != nil || u.Path == "" {
			return nil, fmt.Errorf("bad spine href")
		}
		p := path.Clean(u.Path)
		if opfDir != "" {
			p = path.Clean(path.Join(opfDir, u.Path))
		}
		names = append(names, p)
	}
	return names, nil
}

func extractBodyPlainText(raw []byte) string {
	s := string(raw)
	if m := bodyExtractRe.FindStringSubmatch(s); len(m) >= 3 {
		s = m[2]
	}
	s = htmlCommentRe.ReplaceAllString(s, "")
	return anyTagRe.ReplaceAllString(s, "")
}

func writeOrderedZip(outPath string, entries map[string][]byte, storeMethod map[string]uint16, order []string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	written := make(map[string]bool, len(entries))

	writeEntry := func(name string, data []byte, method uint16) error {
		hdr := &zip.FileHeader{
			Name:   name,
			Method: method,
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	if mimeData, ok := entries["mimetype"]; ok {
		if err := writeEntry("mimetype", mimeData, zip.Store); err != nil {
			zw.Close()
			return err
		}
		written["mimetype"] = true
	}

	for _, name := range order {
		if written[name] {
			continue
		}
		data, ok := entries[name]
		if !ok {
			continue
		}
		method, ok := storeMethod[name]
		if !ok {
			method = zip.Deflate
		}
		if err := writeEntry(name, data, method); err != nil {
			zw.Close()
			return err
		}
		written[name] = true
	}

	for name, data := range entries {
		if written[name] {
			continue
		}
		method, ok := storeMethod[name]
		if !ok {
			method = zip.Deflate
		}
		if err := writeEntry(name, data, method); err != nil {
			zw.Close()
			return err
		}
		written[name] = true
	}

	return zw.Close()
}

func humanBytes(n int64) string {
	val := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB"} {
		if val < 1024.0 || unit == "GB" {
			if unit == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.1f %s", val, unit)
		}
		val /= 1024.0
	}
	return fmt.Sprintf("%.1f GB", val)
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}
