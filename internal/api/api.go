package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/folio-books/folio/internal/config"
	"github.com/folio-books/folio/internal/crosspoint"
	"github.com/folio-books/folio/internal/db"
	"github.com/folio-books/folio/internal/epub"
	"github.com/folio-books/folio/internal/tailscale"
)

// Server handles all REST API requests for the Folio web UI, reader, and Xteink X3 device manager.
type Server struct {
	Store    *db.Store
	Cfg      *config.Config
	CPClient *crosspoint.Client
	TSAuth   *tailscale.Authenticator
}

func NewServer(store *db.Store, cfg *config.Config) *Server {
	return &Server{
		Store:    store,
		Cfg:      cfg,
		CPClient: crosspoint.NewClient(),
		TSAuth:   tailscale.NewAuthenticator(store, cfg),
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/tailscale", s.handleTailscaleAuth)
	mux.HandleFunc("/api/system/info", s.handleSystemInfo)
	mux.HandleFunc("/api/system/users", s.handleSystemUsers)
	mux.HandleFunc("/api/books", s.handleBooks)
	mux.HandleFunc("/api/books/upload", s.handleUploadBooks)
	mux.HandleFunc("/api/books/", s.handleBookSubroutes)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/devices/discover", s.handleDiscoverDevices)
	mux.HandleFunc("/api/devices/", s.handleDeviceSubroutes)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func (s *Server) baseURL(r *http.Request) string {
	if s.Cfg.BaseURL != "" {
		return s.Cfg.BaseURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = fmt.Sprintf("localhost:%d", s.Cfg.Port)
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func (s *Server) handleTailscaleAuth(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sess := tailscale.FromContext(r.Context())
		if sess == nil && s.TSAuth != nil {
			sess = s.TSAuth.Resolve(r)
		}
		writeJSON(w, http.StatusOK, sess)

	case http.MethodPost:
		var req struct {
			Action      string `json:"action"` // "switch_identity" or "rotate_passkey"
			LoginName   string `json:"login_name"`
			DisplayName string `json:"display_name"`
			NodeName    string `json:"node_name"`
			TailscaleIP string `json:"tailscale_ip"`
			AuthMode    string `json:"auth_mode"`
			NewPassword string `json:"new_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if req.Action == "rotate_passkey" {
			sess := tailscale.FromContext(r.Context())
			if sess == nil && s.TSAuth != nil {
				sess = s.TSAuth.Resolve(r)
			}
			var userID int64 = 1
			if sess != nil && sess.User != nil {
				userID = sess.User.ID
			}
			pass := strings.TrimSpace(req.NewPassword)
			if pass == "" {
				b := make([]byte, 6)
				_, _ = rand.Read(b)
				pass = "ts-" + hex.EncodeToString(b)
			}
			u, err := s.Store.RotateUserPassKey(userID, pass)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if sess != nil {
				sess.User = u
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status":         "rotated",
				"plain_passkey":  pass,
				"password_md5":   u.PasswordKey,
				"tailscale":      sess,
			})
			return
		}

		sess, err := s.TSAuth.UpdateSimIdentity(req.LoginName, req.DisplayName, req.NodeName, req.TailscaleIP, req.AuthMode)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sess)

	default:
		writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	base := s.baseURL(r)
	books, _ := s.Store.ListBooks("", "", "", "", "", "recent")
	users, _ := s.Store.ListUsers()
	recentProgress, _ := s.Store.ListAllProgress(20)
	devices, _ := s.Store.ListDevices()
	pushHistory, _ := s.Store.ListPushHistory(15)

	sess := tailscale.FromContext(r.Context())
	if sess == nil && s.TSAuth != nil {
		sess = s.TSAuth.Resolve(r)
	}

	// Collect unique series, tags, and authors for UI filter pills
	seriesSet := make(map[string]int)
	tagSet := make(map[string]int)
	for _, b := range books {
		if b.Series != "" {
			seriesSet[b.Series]++
		}
		for _, t := range b.TagList {
			tagSet[t]++
		}
	}

	defaultSyncUser := s.Cfg.DefaultSyncUser
	if sess != nil && sess.User != nil && sess.User.Username != "" {
		defaultSyncUser = sess.User.Username
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"app_name":           "Folio",
		"version":            "1.3.0",
		"db_driver":          s.Store.Driver,
		"book_count":         len(books),
		"device_count":       len(devices),
		"base_url":           base,
		"opds_v1_url":        base + "/opds",
		"opds_v2_url":        base + "/opds/v2",
		"kosync_url":         base,
		"kosync_alt_url":     base + "/kosync",
		"default_sync_user":  defaultSyncUser,
		"tailscale":          sess,
		"users":              users,
		"recent_progress":    recentProgress,
		"push_history":       pushHistory,
		"series_counts":      seriesSet,
		"tag_counts":         tagSet,
		"mock_x3_enabled":    s.Cfg.EnableMockX3,
		"mock_x3_http_port":  s.Cfg.MockX3HTTPPort,
		"mock_x3_ws_port":    s.Cfg.MockX3WSPort,
	})
}

func (s *Server) handleSystemUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, err := s.Store.ListUsers()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		req.Username = strings.TrimSpace(req.Username)
		req.Password = strings.TrimSpace(req.Password)
		if req.Username == "" || req.Password == "" {
			writeErr(w, http.StatusBadRequest, "Username and password are required")
			return
		}
		passKey := db.MD5Hex(req.Password)
		u, err := s.Store.CreateUser(req.Username, passKey)
		if err == db.ErrUserExists {
			// Update existing user's password key
			_, err = s.Store.Exec(`UPDATE users SET password_key = ? WHERE LOWER(username) = ?`,
				passKey, strings.ToLower(req.Username))
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"username":     req.Username,
				"password_md5": passKey,
				"updated":      true,
			})
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"user":         u,
			"password_md5": passKey,
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleBooks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	q := r.URL.Query().Get("q")
	tag := r.URL.Query().Get("tag")
	series := r.URL.Query().Get("series")
	author := r.URL.Query().Get("author")
	status := r.URL.Query().Get("status")
	sortBy := r.URL.Query().Get("sort")

	books, err := s.Store.ListBooks(q, tag, series, author, status, sortBy)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if books == nil {
		books = []*db.Book{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"books": books,
		"total": len(books),
	})
}

func (s *Server) handleUploadBooks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "Failed to parse multipart form: "+err.Error())
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "No EPUB file provided in 'files' or 'file' field")
		return
	}

	booksDir := filepath.Join(s.Cfg.DataDir, "books")
	coversDir := filepath.Join(s.Cfg.DataDir, "covers")
	_ = os.MkdirAll(booksDir, 0o755)
	_ = os.MkdirAll(coversDir, 0o755)

	var imported []*db.Book
	var errorsList []string

	for _, fh := range files {
		src, err := fh.Open()
		if err != nil {
			errorsList = append(errorsList, fh.Filename+": "+err.Error())
			continue
		}

		safeFileName := sanitizeFilename(fh.Filename)
		if !strings.HasSuffix(strings.ToLower(safeFileName), ".epub") {
			safeFileName += ".epub"
		}
		destPath := filepath.Join(booksDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), safeFileName))

		out, err := os.Create(destPath)
		if err != nil {
			src.Close()
			errorsList = append(errorsList, fh.Filename+": "+err.Error())
			continue
		}
		written, err := io.Copy(out, src)
		out.Close()
		src.Close()
		if err != nil {
			_ = os.Remove(destPath)
			errorsList = append(errorsList, fh.Filename+": "+err.Error())
			continue
		}

		book, err := s.ImportEPUBFile(destPath, safeFileName, written)
		if err != nil {
			_ = os.Remove(destPath)
			errorsList = append(errorsList, fh.Filename+": "+err.Error())
			continue
		}
		imported = append(imported, book)
	}

	if len(imported) == 0 && len(errorsList) > 0 {
		writeErr(w, http.StatusBadRequest, strings.Join(errorsList, "; "))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"imported": imported,
		"errors":   errorsList,
	})
}

// ImportEPUBFile parses an EPUB on disk, saves its extracted cover, and inserts the book into the database.
func (s *Server) ImportEPUBFile(epubPath, originalFileName string, fileSize int64) (*db.Book, error) {
	parsed, err := epub.ParseEPUB(epubPath, originalFileName)
	if err != nil {
		return nil, err
	}

	uuid := parsed.UUID
	if uuid == "" || len(uuid) < 6 {
		uuid = randomUUID()
	} else {
		// Ensure uniqueness in DB
		var exists int
		if err := s.Store.QueryRow(`SELECT COUNT(*) FROM books WHERE uuid = ?`, uuid).Scan(&exists); err == nil && exists > 0 {
			uuid = randomUUID()
		}
	}

	coversDir := filepath.Join(s.Cfg.DataDir, "covers")
	_ = os.MkdirAll(coversDir, 0o755)
	var coverPath string
	if len(parsed.CoverBytes) > 0 {
		ext := parsed.CoverExt
		if ext == "" {
			ext = ".png"
		}
		coverFilePrefix := sanitizeFilename(uuid)
		if coverFilePrefix == "" {
			coverFilePrefix = randomUUID()
		}
		coverPath = filepath.Join(coversDir, coverFilePrefix+ext)
		_ = os.WriteFile(coverPath, parsed.CoverBytes, 0o644)
	}

	if fileSize <= 0 {
		if st, err := os.Stat(epubPath); err == nil {
			fileSize = st.Size()
		}
	}

	book := &db.Book{
		UUID:          uuid,
		Title:         parsed.Title,
		Subtitle:      parsed.Subtitle,
		Authors:       strings.Join(parsed.Authors, ", "),
		Series:        parsed.Series,
		SeriesIndex:   parsed.SeriesIndex,
		Tags:          strings.Join(parsed.Tags, ", "),
		Description:   parsed.Description,
		Publisher:     parsed.Publisher,
		PublishedDate: parsed.PublishedDate,
		Language:      parsed.Language,
		ISBN:          parsed.ISBN,
		FilePath:      epubPath,
		FileName:      originalFileName,
		FileSize:      fileSize,
		CoverPath:     coverPath,
		KOMD5Partial:  parsed.KOMD5Partial,
		KOMD5Alt:      parsed.KOMD5Alt,
		KOMD5Filename: parsed.KOMD5Filename,
		ChapterCount:  parsed.ChapterCount,
		PageCount:     parsed.PageCount,
		ReadingStatus: "unread",
	}

	return s.Store.CreateBook(book)
}

func (s *Server) handleBookSubroutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/books/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "Book ID required")
		return
	}

	bookID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid book ID")
		return
	}

	book, err := s.Store.GetBook(bookID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Book not found")
		return
	}

	if len(parts) == 1 || parts[1] == "" {
		switch r.Method {
		case http.MethodGet:
			u, _ := s.Store.GetDefaultUser()
			var bookmarks []*db.Bookmark
			var clippings []*db.Clipping
			if u != nil {
				bookmarks, _ = s.Store.ListBookmarks(u.ID, book.KOMD5Partial, 0)
				clippings, _ = s.Store.ListClippings(u.ID, book.KOMD5Partial, 0)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"book":      book,
				"bookmarks": bookmarks,
				"clippings": clippings,
			})
		case http.MethodPut:
			s.handleUpdateBookMetadata(w, r, book)
		case http.MethodDelete:
			_ = os.Remove(book.FilePath)
			if book.CoverPath != "" {
				_ = os.Remove(book.CoverPath)
			}
			if err := s.Store.DeleteBook(book.ID); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	action := parts[1]
	switch action {
	case "cover":
		if r.Method == http.MethodGet {
			s.handleServeCover(w, r, book)
		} else if r.Method == http.MethodPost {
			s.handleUploadCover(w, r, book)
		} else {
			writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case "download":
		s.handleDownloadEPUB(w, r, book)
	case "manifest":
		manifest, err := epub.GetBookManifest(book.FilePath, book.ID, book.Title, book.Authors, book.KOMD5Partial)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, manifest)
	case "chapter":
		idx := 0
		if len(parts) >= 3 {
			idx, _ = strconv.Atoi(parts[2])
		}
		ch, err := epub.GetChapterContent(book.FilePath, book.ID, idx)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ch)
	case "asset":
		if len(parts) < 3 || parts[2] == "" {
			http.NotFound(w, r)
			return
		}
		data, mimeType, err := epub.ReadAsset(book.FilePath, parts[2])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleUpdateBookMetadata(w http.ResponseWriter, r *http.Request, book *db.Book) {
	var req struct {
		Title         *string  `json:"title"`
		Subtitle      *string  `json:"subtitle"`
		Authors       *string  `json:"authors"`
		Series        *string  `json:"series"`
		SeriesIndex   *float64 `json:"series_index"`
		Tags          *string  `json:"tags"`
		Description   *string  `json:"description"`
		Publisher     *string  `json:"publisher"`
		PublishedDate *string  `json:"published_date"`
		Language      *string  `json:"language"`
		ISBN          *string  `json:"isbn"`
		ReadingStatus *string  `json:"reading_status"`
		FileName      *string  `json:"file_name"`
		RepackEPUB    bool     `json:"repack_epub"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		book.Title = strings.TrimSpace(*req.Title)
	}
	if req.Subtitle != nil {
		book.Subtitle = strings.TrimSpace(*req.Subtitle)
	}
	if req.Authors != nil {
		book.Authors = strings.TrimSpace(*req.Authors)
		if book.Authors == "" {
			book.Authors = "Unknown Author"
		}
	}
	if req.Series != nil {
		book.Series = strings.TrimSpace(*req.Series)
	}
	if req.SeriesIndex != nil {
		book.SeriesIndex = *req.SeriesIndex
	}
	if req.Tags != nil {
		book.Tags = strings.TrimSpace(*req.Tags)
	}
	if req.Description != nil {
		book.Description = strings.TrimSpace(*req.Description)
	}
	if req.Publisher != nil {
		book.Publisher = strings.TrimSpace(*req.Publisher)
	}
	if req.PublishedDate != nil {
		book.PublishedDate = strings.TrimSpace(*req.PublishedDate)
	}
	if req.Language != nil && strings.TrimSpace(*req.Language) != "" {
		book.Language = strings.TrimSpace(*req.Language)
	}
	if req.ISBN != nil {
		book.ISBN = strings.TrimSpace(*req.ISBN)
	}
	if req.ReadingStatus != nil && *req.ReadingStatus != "" {
		book.ReadingStatus = *req.ReadingStatus
	}
	if req.FileName != nil && strings.TrimSpace(*req.FileName) != "" {
		fn := sanitizeFilename(*req.FileName)
		if !strings.HasSuffix(strings.ToLower(fn), ".epub") {
			fn += ".epub"
		}
		book.FileName = fn
		book.KOMD5Filename = epub.ComputeFilenameMD5(fn)
	}

	book.PopulateComputed()

	// Repack metadata into the .epub archive if requested (default true when editing metadata)
	if req.RepackEPUB {
		meta := &epub.ParsedEPUB{
			UUID:          book.UUID,
			Title:         book.Title,
			Subtitle:      book.Subtitle,
			Authors:       book.AuthorList,
			Series:        book.Series,
			SeriesIndex:   book.SeriesIndex,
			Tags:          book.TagList,
			Description:   book.Description,
			Publisher:     book.Publisher,
			PublishedDate: book.PublishedDate,
			Language:      book.Language,
			ISBN:          book.ISBN,
		}
		if err := epub.RepackEPUBMetadata(book.FilePath, meta, nil, ""); err == nil {
			if newPartial, err := epub.ComputeKOReaderPartialMD5(book.FilePath); err == nil {
				book.KOMD5Partial = newPartial
			}
			if newAlt, err := epub.ComputeAltPartialMD5(book.FilePath); err == nil {
				book.KOMD5Alt = newAlt
			}
			if st, err := os.Stat(book.FilePath); err == nil {
				book.FileSize = st.Size()
			}
		}
	}

	if err := s.Store.UpdateBook(book); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Store.GetBook(book.ID)
	writeJSON(w, http.StatusOK, map[string]any{"book": book})
}

func (s *Server) handleServeCover(w http.ResponseWriter, r *http.Request, book *db.Book) {
	if book.CoverPath != "" {
		if data, err := os.ReadFile(book.CoverPath); err == nil && len(data) > 0 {
			ext := strings.ToLower(filepath.Ext(book.CoverPath))
			switch ext {
			case ".jpg", ".jpeg":
				w.Header().Set("Content-Type", "image/jpeg")
			case ".webp":
				w.Header().Set("Content-Type", "image/webp")
			case ".svg":
				w.Header().Set("Content-Type", "image/svg+xml")
			default:
				w.Header().Set("Content-Type", "image/png")
			}
			w.Header().Set("Cache-Control", "public, max-age=86400")
			_, _ = w.Write(data)
			return
		}
	}
	pngBytes := epub.GenerateTypographicCoverPNG(book.Title, book.Authors, book.Series)
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pngBytes)
}

func (s *Server) handleUploadCover(w http.ResponseWriter, r *http.Request, book *db.Book) {
	var coverBytes []byte
	var ext string

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		// Regenerate typographic cover
		coverBytes = epub.GenerateTypographicCoverPNG(book.Title, book.Authors, book.Series)
		ext = ".png"
	} else {
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, "Invalid multipart cover upload")
			return
		}
		file, header, err := r.FormFile("cover")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "Missing 'cover' file field")
			return
		}
		defer file.Close()
		coverBytes, err = io.ReadAll(io.LimitReader(file, 16<<20))
		if err != nil || len(coverBytes) == 0 {
			writeErr(w, http.StatusBadRequest, "Empty cover file")
			return
		}
		ext = strings.ToLower(filepath.Ext(header.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" && ext != ".svg" {
			ext = ".png"
		}
	}

	coversDir := filepath.Join(s.Cfg.DataDir, "covers")
	_ = os.MkdirAll(coversDir, 0o755)
	newCoverPath := filepath.Join(coversDir, fmt.Sprintf("%s_%d%s", book.UUID, time.Now().Unix(), ext))
	if err := os.WriteFile(newCoverPath, coverBytes, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to save cover: "+err.Error())
		return
	}
	if book.CoverPath != "" && book.CoverPath != newCoverPath {
		_ = os.Remove(book.CoverPath)
	}
	book.CoverPath = newCoverPath
	book.PopulateComputed()

	// Embed the new cover image inside the .epub file as well so OPDS & Xteink X3 receive it!
	meta := &epub.ParsedEPUB{
		UUID:          book.UUID,
		Title:         book.Title,
		Subtitle:      book.Subtitle,
		Authors:       book.AuthorList,
		Series:        book.Series,
		SeriesIndex:   book.SeriesIndex,
		Tags:          book.TagList,
		Description:   book.Description,
		Publisher:     book.Publisher,
		PublishedDate: book.PublishedDate,
		Language:      book.Language,
		ISBN:          book.ISBN,
	}
	if err := epub.RepackEPUBMetadata(book.FilePath, meta, coverBytes, ext); err == nil {
		if newPartial, err := epub.ComputeKOReaderPartialMD5(book.FilePath); err == nil {
			book.KOMD5Partial = newPartial
		}
		if newAlt, err := epub.ComputeAltPartialMD5(book.FilePath); err == nil {
			book.KOMD5Alt = newAlt
		}
		if st, err := os.Stat(book.FilePath); err == nil {
			book.FileSize = st.Size()
		}
	}

	if err := s.Store.UpdateBook(book); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"book": book})
}

func (s *Server) handleDownloadEPUB(w http.ResponseWriter, r *http.Request, book *db.Book) {
	w.Header().Set("Content-Type", "application/epub+zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, book.FileName))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeFile(w, r, book.FilePath)
}

// --- Xteink X3 / CrossPoint Reader Device Endpoints ---

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		devices, err := s.Store.ListDevices()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		history, _ := s.Store.ListPushHistory(20)
		writeJSON(w, http.StatusOK, map[string]any{
			"devices":      devices,
			"push_history": history,
		})
	case http.MethodPost:
		var dev db.Device
		if err := json.NewDecoder(r.Body).Decode(&dev); err != nil {
			writeErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		dev.Name = strings.TrimSpace(dev.Name)
		dev.Host = strings.TrimSpace(dev.Host)
		if dev.Name == "" || dev.Host == "" {
			writeErr(w, http.StatusBadRequest, "Device name and host/IP are required")
			return
		}
		saved, err := s.Store.SaveDevice(&dev)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device": saved})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleDiscoverDevices(w http.ResponseWriter, r *http.Request) {
	extraPorts := []int{}
	if s.Cfg.EnableMockX3 {
		extraPorts = append(extraPorts, 18134)
	}
	found := crosspoint.DiscoverDevices(900*time.Millisecond, extraPorts...)
	if found == nil {
		found = []crosspoint.DiscoveredDevice{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"discovered": found,
	})
}

func (s *Server) handleDeviceSubroutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "Device ID required")
		return
	}

	devID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid device ID")
		return
	}
	dev, err := s.Store.GetDevice(devID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Device not found")
		return
	}

	if len(parts) == 1 || parts[1] == "" {
		if r.Method == http.MethodDelete {
			if err := s.Store.DeleteDevice(dev.ID); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
			return
		}
		if r.Method == http.MethodPut {
			var updated db.Device
			if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
				writeErr(w, http.StatusBadRequest, "Invalid JSON")
				return
			}
			updated.ID = dev.ID
			saved, err := s.Store.SaveDevice(&updated)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"device": saved})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device": dev})
		return
	}

	action := parts[1]
	switch action {
	case "status":
		status, rawJSON, err := s.CPClient.GetStatus(dev.Host, dev.HTTPPort)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"online": false,
				"error":  err.Error(),
				"device": dev,
			})
			return
		}
		s.Store.UpdateDeviceStatus(dev.ID, rawJSON)
		dev.LastStatusJSON = rawJSON
		dev.LastSeenAt = time.Now().Unix()
		writeJSON(w, http.StatusOK, map[string]any{
			"online": true,
			"status": status,
			"device": dev,
		})

	case "files":
		dirPath := r.URL.Query().Get("path")
		if dirPath == "" {
			dirPath = dev.TargetPath
		}
		files, err := s.CPClient.ListFiles(dev.Host, dev.HTTPPort, dirPath)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":  dirPath,
			"files": files,
		})

	case "mkdir":
		var req struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := s.CPClient.EnsureFolder(dev.Host, dev.HTTPPort, req.Path); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "delete-file":
		var req struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := s.CPClient.DeleteRemoteFile(dev.Host, dev.HTTPPort, req.Path); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "push":
		var req struct {
			BookID     int64  `json:"book_id"`
			TargetPath string `json:"target_path"`
			Protocol   string `json:"protocol"`
			Overwrite  *bool  `json:"overwrite"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BookID <= 0 {
			writeErr(w, http.StatusBadRequest, "Valid book_id is required")
			return
		}
		book, err := s.Store.GetBook(req.BookID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "Book not found")
			return
		}
		targetDir := strings.TrimSpace(req.TargetPath)
		if targetDir == "" {
			targetDir = dev.TargetPath
		}
		proto := strings.TrimSpace(req.Protocol)
		if proto == "" {
			proto = dev.PreferredProtocol
		}
		overwrite := true
		if req.Overwrite != nil {
			overwrite = *req.Overwrite
		}

		res, err := s.CPClient.PushBook(dev.Host, dev.HTTPPort, dev.WSPort, targetDir, book.FileName, book.FilePath, proto, overwrite)
		if err != nil {
			s.Store.RecordPushHistory(&db.PushHistory{
				BookID:     book.ID,
				BookTitle:  book.Title,
				DeviceID:   dev.ID,
				DeviceName: dev.Name,
				DeviceHost: fmt.Sprintf("%s:%d", dev.Host, dev.HTTPPort),
				Protocol:   proto,
				RemotePath: filepath.Join(targetDir, book.FileName),
				Status:     "error",
				Message:    err.Error(),
			})
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}

		s.Store.RecordPushHistory(&db.PushHistory{
			BookID:     book.ID,
			BookTitle:  book.Title,
			DeviceID:   dev.ID,
			DeviceName: dev.Name,
			DeviceHost: fmt.Sprintf("%s:%d", dev.Host, dev.HTTPPort),
			Protocol:   res.Protocol,
			RemotePath: res.RemotePath,
			BytesSent:  res.BytesSent,
			Status:     "success",
			Message:    res.Message,
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"result": res,
			"book":   book,
			"device": dev,
		})

	case "configure-opds":
		base := s.baseURL(r)
		var req struct {
			CatalogName string `json:"catalog_name"`
			OPDSURL     string `json:"opds_url"`
			KOSyncURL   string `json:"kosync_url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.CatalogName == "" {
			req.CatalogName = "Folio EPUB Library"
		}
		if req.OPDSURL == "" {
			req.OPDSURL = base + "/opds"
		}
		if req.KOSyncURL == "" {
			req.KOSyncURL = base
		}

		if err := s.CPClient.RegisterOPDSOnDevice(dev.Host, dev.HTTPPort, req.CatalogName, req.OPDSURL, "", ""); err != nil {
			writeErr(w, http.StatusBadGateway, "Failed to register OPDS on device: "+err.Error())
			return
		}
		_ = s.CPClient.ConfigureKOSyncOnDevice(dev.Host, dev.HTTPPort, req.KOSyncURL)

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"catalog_url": req.OPDSURL,
			"kosync_url":  req.KOSyncURL,
			"message":     fmt.Sprintf("Configured OPDS catalog (%s) and KOReader sync on %s", req.OPDSURL, dev.Name),
		})

	default:
		http.NotFound(w, r)
	}
}

var unsafeCharsRe = regexp.MustCompile(`[^a-zA-Z0-9._\-\s]+`)

func sanitizeFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	cleaned := unsafeCharsRe.ReplaceAllString(base, "_")
	cleaned = strings.ReplaceAll(cleaned, " ", "_")
	if cleaned == "" || cleaned == "." {
		return "book.epub"
	}
	return cleaned
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}
