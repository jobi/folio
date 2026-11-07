package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User represents a Tailscale-authenticated user and their linked KOReader / CrossPoint sync account.
type User struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	PasswordKey    string `json:"password_md5,omitempty"`
	TailscaleLogin string `json:"tailscale_login"`
	DisplayName    string `json:"display_name"`
	ProfilePicURL  string `json:"profile_pic_url,omitempty"`
	TailscaleNode  string `json:"tailscale_node,omitempty"`
	TailscaleIP    string `json:"tailscale_ip,omitempty"`
	AuthSource     string `json:"auth_source,omitempty"`
	CreatedAt      int64  `json:"created_at"`
}

// Book represents an EPUB book in the library with full metadata and KOReader hashes.
type Book struct {
	ID             int64              `json:"id"`
	UUID           string             `json:"uuid"`
	Title          string             `json:"title"`
	Subtitle       string             `json:"subtitle"`
	Authors        string             `json:"authors"`
	AuthorList     []string           `json:"author_list"`
	Series         string             `json:"series"`
	SeriesIndex    float64            `json:"series_index"`
	Tags           string             `json:"tags"`
	TagList        []string           `json:"tag_list"`
	Description    string             `json:"description"`
	Publisher      string             `json:"publisher"`
	PublishedDate  string             `json:"published_date"`
	Language       string             `json:"language"`
	ISBN           string             `json:"isbn"`
	FilePath       string             `json:"-"`
	FileName       string             `json:"file_name"`
	FileSize       int64              `json:"file_size"`
	CoverPath      string             `json:"-"`
	HasCover       bool               `json:"has_cover"`
	CoverURL       string             `json:"cover_url"`
	KOMD5Partial   string             `json:"ko_md5_partial"`
	KOMD5Alt       string             `json:"ko_md5_alt"`
	KOMD5Filename  string             `json:"ko_md5_filename"`
	ChapterCount   int                `json:"chapter_count"`
	PageCount      int                `json:"page_count"`
	ReadingStatus  string             `json:"reading_status"`
	AddedAt        int64              `json:"added_at"`
	UpdatedAt      int64              `json:"updated_at"`
	LatestProgress *ReadingProgress   `json:"latest_progress,omitempty"`
	DeviceProgress []*ReadingProgress `json:"device_progress,omitempty"`
}

// CompactPosition mirrors CrossPoint Reader's /api/v1/progress position object.
type CompactPosition struct {
	PctQ   uint32 `json:"pctQ"`
	Spine  uint16 `json:"spine"`
	Page   uint16 `json:"page"`
	Pages  uint16 `json:"pages"`
	Para   uint16 `json:"para,omitempty"`
	Li     uint16 `json:"li,omitempty"`
	Anchor string `json:"anchor,omitempty"`
	XPath  string `json:"xpath,omitempty"`
}

// SyncMetadata mirrors KOReader / CrossPoint optional metadata payload in PUT /syncs/progress.
type SyncMetadata struct {
	Filename string `json:"filename,omitempty"`
	Title    string `json:"title,omitempty"`
	Authors  string `json:"authors,omitempty"`
	Language string `json:"language,omitempty"`
}

// ReadingProgress represents a reading progress checkpoint from KOReader, CrossPoint, or the Web Reader.
type ReadingProgress struct {
	ID           int64            `json:"id"`
	UserID       int64            `json:"user_id,omitempty"`
	DocumentHash string           `json:"document"`
	BookID       int64            `json:"book_id,omitempty"`
	BookTitle    string           `json:"book_title,omitempty"`
	BookAuthors  string           `json:"book_authors,omitempty"`
	Device       string           `json:"device"`
	DeviceID     string           `json:"device_id"`
	Progress     string           `json:"progress"`
	Percentage   float64          `json:"percentage"`
	Position     *CompactPosition `json:"position,omitempty"`
	Metadata     *SyncMetadata    `json:"metadata,omitempty"`
	Timestamp    int64            `json:"timestamp"`
	Seq          int64            `json:"seq,omitempty"`
}

// Bookmark represents a synced bookmark (compatible with CrossPoint /api/v1/bookmarks and Web Reader).
type Bookmark struct {
	ID            string  `json:"id"`
	UserID        int64   `json:"-"`
	DocumentHash  string  `json:"document,omitempty"`
	BookID        int64   `json:"book_id,omitempty"`
	XPath         string  `json:"xpath"`
	Percentage    float64 `json:"percentage"`
	Summary       string  `json:"summary"`
	SpineIndex    int     `json:"si"`
	PageInChapter int     `json:"pp"`
	ChapterPages  int     `json:"pc"`
	ChapterTitle  string  `json:"chapter_title,omitempty"`
	Deleted       int     `json:"deleted"`
	UpdatedAt     int64   `json:"updated_at"`
}

// Clipping represents a highlighted excerpt or note (compatible with CrossPoint /api/v1/clippings).
type Clipping struct {
	ID           string `json:"id"`
	UserID       int64  `json:"-"`
	DocumentHash string `json:"document,omitempty"`
	BookID       int64  `json:"book_id,omitempty"`
	Spine        int    `json:"spine"`
	Para         int    `json:"para,omitempty"`
	Chapter      string `json:"chapter"`
	Text         string `json:"text"`
	Note         string `json:"note,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	Deleted      int    `json:"deleted"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Device represents a configured e-reader target such as an Xteink X3 running CrossPoint Reader.
type Device struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	DeviceModel       string `json:"device_model"`
	Host              string `json:"host"`
	HTTPPort          int    `json:"http_port"`
	WSPort            int    `json:"ws_port"`
	TargetPath        string `json:"target_path"`
	PreferredProtocol string `json:"preferred_protocol"` // "auto", "websocket", "http", "webdav"
	IsDefault         bool   `json:"is_default"`
	LastStatusJSON    string `json:"last_status_json,omitempty"`
	LastSeenAt        int64  `json:"last_seen_at"`
	CreatedAt         int64  `json:"created_at"`
}

// PushHistory records book pushes to Xteink X3 / CrossPoint devices.
type PushHistory struct {
	ID         int64  `json:"id"`
	BookID     int64  `json:"book_id"`
	BookTitle  string `json:"book_title"`
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`
	DeviceHost string `json:"device_host"`
	Protocol   string `json:"protocol"`
	RemotePath string `json:"remote_path"`
	BytesSent  int64  `json:"bytes_sent"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	CreatedAt  int64  `json:"created_at"`
}

const bookColumns = `id, uuid, title, subtitle, authors, series, series_index, tags, description,
	publisher, published_date, language, isbn, file_path, file_name, file_size, cover_path,
	ko_md5_partial, ko_md5_alt, ko_md5_filename, chapter_count, page_count, reading_status, added_at, updated_at`

func scanBook(scanner interface{ Scan(dest ...any) error }) (*Book, error) {
	var b Book
	err := scanner.Scan(
		&b.ID, &b.UUID, &b.Title, &b.Subtitle, &b.Authors, &b.Series, &b.SeriesIndex,
		&b.Tags, &b.Description, &b.Publisher, &b.PublishedDate, &b.Language, &b.ISBN,
		&b.FilePath, &b.FileName, &b.FileSize, &b.CoverPath,
		&b.KOMD5Partial, &b.KOMD5Alt, &b.KOMD5Filename,
		&b.ChapterCount, &b.PageCount, &b.ReadingStatus, &b.AddedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	b.PopulateComputed()
	return &b, nil
}

// PopulateComputed populates derived slices and URLs for JSON responses.
func (b *Book) PopulateComputed() {
	b.AuthorList = splitCSV(b.Authors)
	if len(b.AuthorList) == 0 {
		b.AuthorList = []string{"Unknown Author"}
	}
	b.TagList = splitCSV(b.Tags)
	b.HasCover = b.CoverPath != ""
	b.CoverURL = fmt.Sprintf("/api/books/%d/cover?v=%d", b.ID, b.UpdatedAt)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// CreateBook inserts a new book record and registers its KOReader hashes in book_hash_aliases.
func (s *Store) CreateBook(b *Book) (*Book, error) {
	now := time.Now().Unix()
	if b.AddedAt == 0 {
		b.AddedAt = now
	}
	b.UpdatedAt = now
	if b.Language == "" {
		b.Language = "en"
	}
	if b.ReadingStatus == "" {
		b.ReadingStatus = "unread"
	}

	if s.Driver == "postgres" {
		err := s.QueryRow(`INSERT INTO books (
			uuid, title, subtitle, authors, series, series_index, tags, description,
			publisher, published_date, language, isbn, file_path, file_name, file_size, cover_path,
			ko_md5_partial, ko_md5_alt, ko_md5_filename, chapter_count, page_count, reading_status, added_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			b.UUID, b.Title, b.Subtitle, b.Authors, b.Series, b.SeriesIndex, b.Tags, b.Description,
			b.Publisher, b.PublishedDate, b.Language, b.ISBN, b.FilePath, b.FileName, b.FileSize, b.CoverPath,
			b.KOMD5Partial, b.KOMD5Alt, b.KOMD5Filename, b.ChapterCount, b.PageCount, b.ReadingStatus, b.AddedAt, b.UpdatedAt,
		).Scan(&b.ID)
		if err != nil {
			return nil, err
		}
	} else {
		res, err := s.Exec(`INSERT INTO books (
			uuid, title, subtitle, authors, series, series_index, tags, description,
			publisher, published_date, language, isbn, file_path, file_name, file_size, cover_path,
			ko_md5_partial, ko_md5_alt, ko_md5_filename, chapter_count, page_count, reading_status, added_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.UUID, b.Title, b.Subtitle, b.Authors, b.Series, b.SeriesIndex, b.Tags, b.Description,
			b.Publisher, b.PublishedDate, b.Language, b.ISBN, b.FilePath, b.FileName, b.FileSize, b.CoverPath,
			b.KOMD5Partial, b.KOMD5Alt, b.KOMD5Filename, b.ChapterCount, b.PageCount, b.ReadingStatus, b.AddedAt, b.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		b.ID, _ = res.LastInsertId()
	}

	s.RegisterBookHashes(b.ID, b.KOMD5Partial, b.KOMD5Alt, b.KOMD5Filename)
	// Also link any existing unlinked reading_progress rows that match these hashes
	s.relinkOrphanProgress(b)
	b.PopulateComputed()
	return b, nil
}

// RegisterBookHashes stores document hash aliases for a book so old hashes continue resolving after metadata edits.
func (s *Store) RegisterBookHashes(bookID int64, hashes ...string) {
	now := time.Now().Unix()
	for _, h := range hashes {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if s.Driver == "postgres" {
			_, _ = s.Exec(`INSERT INTO book_hash_aliases (book_id, document_hash, created_at)
				VALUES (?, ?, ?) ON CONFLICT (document_hash) DO UPDATE SET book_id = EXCLUDED.book_id`,
				bookID, h, now)
		} else {
			_, _ = s.Exec(`INSERT OR REPLACE INTO book_hash_aliases (book_id, document_hash, created_at)
				VALUES (?, ?, ?)`, bookID, h, now)
		}
	}
}

func (s *Store) relinkOrphanProgress(b *Book) {
	for _, h := range []string{b.KOMD5Partial, b.KOMD5Alt, b.KOMD5Filename} {
		if h != "" {
			_, _ = s.Exec(`UPDATE reading_progress SET book_id = ? WHERE document_hash = ? AND book_id = 0`, b.ID, h)
			_, _ = s.Exec(`UPDATE bookmarks SET book_id = ? WHERE document_hash = ? AND book_id = 0`, b.ID, h)
			_, _ = s.Exec(`UPDATE clippings SET book_id = ? WHERE document_hash = ? AND book_id = 0`, b.ID, h)
		}
	}
}

// UpdateBook updates a book's metadata, cover path, or hashes.
func (s *Store) UpdateBook(b *Book) error {
	b.UpdatedAt = time.Now().Unix()
	_, err := s.Exec(`UPDATE books SET
		title = ?, subtitle = ?, authors = ?, series = ?, series_index = ?,
		tags = ?, description = ?, publisher = ?, published_date = ?, language = ?,
		isbn = ?, file_path = ?, file_name = ?, file_size = ?, cover_path = ?,
		ko_md5_partial = ?, ko_md5_alt = ?, ko_md5_filename = ?,
		chapter_count = ?, page_count = ?, reading_status = ?, updated_at = ?
		WHERE id = ?`,
		b.Title, b.Subtitle, b.Authors, b.Series, b.SeriesIndex,
		b.Tags, b.Description, b.Publisher, b.PublishedDate, b.Language,
		b.ISBN, b.FilePath, b.FileName, b.FileSize, b.CoverPath,
		b.KOMD5Partial, b.KOMD5Alt, b.KOMD5Filename,
		b.ChapterCount, b.PageCount, b.ReadingStatus, b.UpdatedAt, b.ID,
	)
	if err != nil {
		return err
	}
	s.RegisterBookHashes(b.ID, b.KOMD5Partial, b.KOMD5Alt, b.KOMD5Filename)
	b.PopulateComputed()
	return nil
}

// DeleteBook deletes a book and its hash aliases from the database.
func (s *Store) DeleteBook(id int64) error {
	_, _ = s.Exec(`DELETE FROM book_hash_aliases WHERE book_id = ?`, id)
	_, err := s.Exec(`DELETE FROM books WHERE id = ?`, id)
	return err
}

// GetBook returns a single book by ID, enriched with its latest reading progress across all devices.
func (s *Store) GetBook(id int64) (*Book, error) {
	b, err := scanBook(s.QueryRow(`SELECT `+bookColumns+` FROM books WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	s.enrichBookProgress(b)
	return b, nil
}

// ListBooks returns books filtered by search query, tag, series, author, or status.
func (s *Store) ListBooks(search, filterTag, filterSeries, filterAuthor, filterStatus, sortBy string) ([]*Book, error) {
	var where []string
	var args []any

	if q := strings.TrimSpace(search); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, `(LOWER(title) LIKE ? OR LOWER(authors) LIKE ? OR LOWER(series) LIKE ? OR LOWER(tags) LIKE ? OR LOWER(isbn) LIKE ?)`)
		args = append(args, like, like, like, like, like)
	}
	if t := strings.TrimSpace(filterTag); t != "" {
		where = append(where, `LOWER(tags) LIKE ?`)
		args = append(args, "%"+strings.ToLower(t)+"%")
	}
	if ser := strings.TrimSpace(filterSeries); ser != "" {
		where = append(where, `LOWER(series) = ?`)
		args = append(args, strings.ToLower(ser))
	}
	if auth := strings.TrimSpace(filterAuthor); auth != "" {
		where = append(where, `LOWER(authors) LIKE ?`)
		args = append(args, "%"+strings.ToLower(auth)+"%")
	}
	if st := strings.TrimSpace(filterStatus); st != "" && st != "all" {
		where = append(where, `reading_status = ?`)
		args = append(args, st)
	}

	query := `SELECT ` + bookColumns + ` FROM books`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}

	switch sortBy {
	case "title":
		query += ` ORDER BY LOWER(title) ASC`
	case "author":
		query += ` ORDER BY LOWER(authors) ASC, LOWER(title) ASC`
	case "series":
		query += ` ORDER BY CASE WHEN series = '' THEN 1 ELSE 0 END, LOWER(series) ASC, series_index ASC`
	case "updated":
		query += ` ORDER BY updated_at DESC`
	default:
		query += ` ORDER BY added_at DESC, id DESC`
	}

	rows, err := s.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []*Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, b := range books {
		s.enrichBookProgress(b)
	}
	return books, nil
}

func (s *Store) enrichBookProgress(b *Book) {
	rows, err := s.Query(`SELECT id, user_id, document_hash, book_id, device, device_id,
		progress, percentage, position_json, metadata_json, timestamp, seq
		FROM reading_progress
		WHERE book_id = ? OR document_hash IN (?, ?, ?)
		ORDER BY timestamp DESC`,
		b.ID, b.KOMD5Partial, b.KOMD5Alt, b.KOMD5Filename)
	if err != nil {
		return
	}
	defer rows.Close()

	var all []*ReadingProgress
	for rows.Next() {
		rp := scanProgressRow(rows)
		if rp != nil {
			all = append(all, rp)
		}
	}
	if len(all) > 0 {
		b.LatestProgress = all[0]
		b.DeviceProgress = all
		if b.ReadingStatus == "unread" && all[0].Percentage > 0 {
			if all[0].Percentage >= 0.98 {
				b.ReadingStatus = "finished"
			} else {
				b.ReadingStatus = "reading"
			}
		}
	}
}

func scanProgressRow(scanner interface{ Scan(dest ...any) error }) *ReadingProgress {
	var rp ReadingProgress
	var posJSON, metaJSON string
	err := scanner.Scan(
		&rp.ID, &rp.UserID, &rp.DocumentHash, &rp.BookID,
		&rp.Device, &rp.DeviceID, &rp.Progress, &rp.Percentage,
		&posJSON, &metaJSON, &rp.Timestamp, &rp.Seq,
	)
	if err != nil {
		return nil
	}
	if posJSON != "" {
		var pos CompactPosition
		if json.Unmarshal([]byte(posJSON), &pos) == nil {
			rp.Position = &pos
		}
	}
	if metaJSON != "" {
		var meta SyncMetadata
		if json.Unmarshal([]byte(metaJSON), &meta) == nil {
			rp.Metadata = &meta
		}
	}
	return &rp
}

// FindBookByDocumentHash resolves a KOReader / CrossPoint document hash or metadata to a Book.
func (s *Store) FindBookByDocumentHash(docHash string, meta *SyncMetadata) (*Book, error) {
	docHash = strings.TrimSpace(docHash)
	if docHash != "" {
		b, err := scanBook(s.QueryRow(`SELECT `+bookColumns+` FROM books
			WHERE ko_md5_partial = ? OR ko_md5_alt = ? OR ko_md5_filename = ? LIMIT 1`,
			docHash, docHash, docHash))
		if err == nil {
			return b, nil
		}
		var bookID int64
		if err := s.QueryRow(`SELECT book_id FROM book_hash_aliases WHERE document_hash = ?`, docHash).Scan(&bookID); err == nil && bookID > 0 {
			return s.GetBook(bookID)
		}
	}
	if meta != nil {
		if meta.Filename != "" {
			b, err := scanBook(s.QueryRow(`SELECT `+bookColumns+` FROM books WHERE LOWER(file_name) = ? LIMIT 1`, strings.ToLower(meta.Filename)))
			if err == nil {
				return b, nil
			}
		}
		if meta.Title != "" {
			b, err := scanBook(s.QueryRow(`SELECT `+bookColumns+` FROM books WHERE LOWER(title) = ? LIMIT 1`, strings.ToLower(meta.Title)))
			if err == nil {
				return b, nil
			}
		}
	}
	return nil, sql.ErrNoRows
}

// --- User / Auth methods ---

var ErrUserExists = errors.New("username already exists")

const userColumns = `id, username, password_key, COALESCE(tailscale_login, ''), COALESCE(display_name, ''), COALESCE(profile_pic_url, ''), COALESCE(tailscale_node, ''), COALESCE(tailscale_ip, ''), COALESCE(auth_source, 'local'), created_at`

func scanUser(scanner interface{ Scan(dest ...any) error }) (*User, error) {
	var u User
	err := scanner.Scan(
		&u.ID, &u.Username, &u.PasswordKey,
		&u.TailscaleLogin, &u.DisplayName, &u.ProfilePicURL,
		&u.TailscaleNode, &u.TailscaleIP, &u.AuthSource, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) CreateUser(username, passwordKey string) (*User, error) {
	username = strings.TrimSpace(username)
	now := time.Now().Unix()

	var existing int
	if err := s.QueryRow(`SELECT COUNT(*) FROM users WHERE LOWER(username) = ?`, strings.ToLower(username)).Scan(&existing); err == nil && existing > 0 {
		return nil, ErrUserExists
	}

	var id int64
	if s.Driver == "postgres" {
		err := s.QueryRow(`INSERT INTO users (username, password_key, display_name, auth_source, created_at) VALUES (?, ?, ?, 'kosync', ?) RETURNING id`,
			username, strings.ToLower(passwordKey), username, now).Scan(&id)
		if err != nil {
			return nil, err
		}
	} else {
		res, err := s.Exec(`INSERT INTO users (username, password_key, display_name, auth_source, created_at) VALUES (?, ?, ?, 'kosync', ?)`,
			username, strings.ToLower(passwordKey), username, now)
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
	}

	return &User{
		ID:          id,
		Username:    username,
		PasswordKey: strings.ToLower(passwordKey),
		DisplayName: username,
		AuthSource:  "kosync",
		CreatedAt:   now,
	}, nil
}

// UpsertTailscaleUser links or creates a Folio user account from a verified Tailscale WhoIs or Serve identity.
// If the user is logging in via Tailscale for the first time and a single default user exists without a Tailscale login,
// it links to the primary account so Web Reader and KOReader / Xteink X3 devices share reading progress automatically.
func (s *Store) UpsertTailscaleUser(tsLogin, displayName, profilePic, nodeName, nodeIP, authSource, defaultPassKey string) (*User, error) {
	tsLogin = strings.ToLower(strings.TrimSpace(tsLogin))
	if tsLogin == "" {
		return s.GetDefaultUser()
	}
	if displayName == "" {
		displayName = tsLogin
	}
	if authSource == "" {
		authSource = "tailscale"
	}

	// Derive a clean short username from login (e.g. "alice@example.com" -> "alice")
	shortUser := tsLogin
	if idx := strings.Index(shortUser, "@"); idx > 0 {
		shortUser = shortUser[:idx]
	}

	// 1. Check if a user already has this tailscale_login or matching username
	u, err := scanUser(s.QueryRow(`SELECT `+userColumns+` FROM users WHERE LOWER(tailscale_login) = ? OR LOWER(username) = ? ORDER BY id ASC LIMIT 1`,
		tsLogin, shortUser))
	if err == nil && u != nil {
		_, _ = s.Exec(`UPDATE users SET tailscale_login = ?, display_name = ?, profile_pic_url = ?, tailscale_node = ?, tailscale_ip = ?, auth_source = ? WHERE id = ?`,
			tsLogin, displayName, profilePic, nodeName, nodeIP, authSource, u.ID)
		u.TailscaleLogin = tsLogin
		u.DisplayName = displayName
		u.ProfilePicURL = profilePic
		u.TailscaleNode = nodeName
		u.TailscaleIP = nodeIP
		u.AuthSource = authSource
		return u, nil
	}

	// 2. If the primary user (id=1) has no tailscale_login yet or matches reader@tailnet.ts.net, bind it
	if defUser, err := s.GetDefaultUser(); err == nil && defUser != nil &&
		(defUser.TailscaleLogin == "" || defUser.TailscaleLogin == "reader@tailnet.ts.net") &&
		(shortUser == "reader" || defUser.TailscaleLogin == "") {
		_, _ = s.Exec(`UPDATE users SET tailscale_login = ?, display_name = ?, profile_pic_url = ?, tailscale_node = ?, tailscale_ip = ?, auth_source = ? WHERE id = ?`,
			tsLogin, displayName, profilePic, nodeName, nodeIP, authSource, defUser.ID)
		defUser.TailscaleLogin = tsLogin
		defUser.DisplayName = displayName
		defUser.ProfilePicURL = profilePic
		defUser.TailscaleNode = nodeName
		defUser.TailscaleIP = nodeIP
		defUser.AuthSource = authSource
		return defUser, nil
	}

	// 3. Otherwise create a new user account for this tailnet member
	passKey := defaultPassKey
	if passKey == "" {
		passKey = MD5Hex(shortUser + "-tailscale-sync")
	}
	now := time.Now().Unix()
	var id int64
	if s.Driver == "postgres" {
		err := s.QueryRow(`INSERT INTO users (username, password_key, tailscale_login, display_name, profile_pic_url, tailscale_node, tailscale_ip, auth_source, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			shortUser, passKey, tsLogin, displayName, profilePic, nodeName, nodeIP, authSource, now).Scan(&id)
		if err != nil {
			return s.GetDefaultUser()
		}
	} else {
		res, err := s.Exec(`INSERT INTO users (username, password_key, tailscale_login, display_name, profile_pic_url, tailscale_node, tailscale_ip, auth_source, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			shortUser, passKey, tsLogin, displayName, profilePic, nodeName, nodeIP, authSource, now)
		if err != nil {
			return s.GetDefaultUser()
		}
		id, _ = res.LastInsertId()
	}

	return &User{
		ID:             id,
		Username:       shortUser,
		PasswordKey:    passKey,
		TailscaleLogin: tsLogin,
		DisplayName:    displayName,
		ProfilePicURL:  profilePic,
		TailscaleNode:  nodeName,
		TailscaleIP:    nodeIP,
		AuthSource:     authSource,
		CreatedAt:      now,
	}, nil
}

func (s *Store) RotateUserPassKey(userID int64, plainPassword string) (*User, error) {
	key := MD5Hex(strings.TrimSpace(plainPassword))
	if _, err := s.Exec(`UPDATE users SET password_key = ? WHERE id = ?`, key, userID); err != nil {
		return nil, err
	}
	return scanUser(s.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, userID))
}

func (s *Store) AuthenticateUser(username, passwordKey string) (*User, error) {
	q := strings.ToLower(strings.TrimSpace(username))
	u, err := scanUser(s.QueryRow(`SELECT `+userColumns+` FROM users WHERE LOWER(username) = ? OR LOWER(tailscale_login) = ? LIMIT 1`, q, q))
	if err != nil {
		return nil, err
	}
	// Accept either the MD5 hex key (standard KOReader x-auth-key) or plain password hashed to MD5
	given := strings.ToLower(strings.TrimSpace(passwordKey))
	if u.PasswordKey != given && u.PasswordKey != MD5Hex(passwordKey) {
		return nil, sql.ErrNoRows
	}
	return u, nil
}

func (s *Store) GetDefaultUser() (*User, error) {
	return scanUser(s.QueryRow(`SELECT ` + userColumns + ` FROM users ORDER BY id ASC LIMIT 1`))
}

func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.Query(`SELECT ` + userColumns + ` FROM users ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

// --- Progress Sync methods ---

func (s *Store) UpsertProgress(rp *ReadingProgress) (*ReadingProgress, error) {
	if rp.Timestamp <= 0 {
		rp.Timestamp = time.Now().Unix()
	}
	if rp.Device == "" {
		rp.Device = "KOReader"
	}
	if rp.DeviceID == "" {
		rp.DeviceID = rp.Device
	}

	// Attempt to match book if BookID == 0
	if rp.BookID == 0 {
		if b, err := s.FindBookByDocumentHash(rp.DocumentHash, rp.Metadata); err == nil && b != nil {
			rp.BookID = b.ID
			s.RegisterBookHashes(b.ID, rp.DocumentHash)
		}
	} else {
		s.RegisterBookHashes(rp.BookID, rp.DocumentHash)
	}

	var posStr, metaStr string
	if rp.Position != nil {
		if b, err := json.Marshal(rp.Position); err == nil {
			posStr = string(b)
		}
	}
	if rp.Metadata != nil {
		if b, err := json.Marshal(rp.Metadata); err == nil {
			metaStr = string(b)
		}
	}

	var nextSeq int64
	_ = s.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM reading_progress`).Scan(&nextSeq)
	rp.Seq = nextSeq

	if s.Driver == "postgres" {
		_, err := s.Exec(`INSERT INTO reading_progress (
			user_id, document_hash, book_id, device, device_id, progress, percentage, position_json, metadata_json, timestamp, seq
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, document_hash, device_id) DO UPDATE SET
			book_id = CASE WHEN EXCLUDED.book_id > 0 THEN EXCLUDED.book_id ELSE reading_progress.book_id END,
			device = EXCLUDED.device,
			progress = EXCLUDED.progress,
			percentage = EXCLUDED.percentage,
			position_json = CASE WHEN EXCLUDED.position_json != '' THEN EXCLUDED.position_json ELSE reading_progress.position_json END,
			metadata_json = CASE WHEN EXCLUDED.metadata_json != '' THEN EXCLUDED.metadata_json ELSE reading_progress.metadata_json END,
			timestamp = EXCLUDED.timestamp,
			seq = EXCLUDED.seq`,
			rp.UserID, rp.DocumentHash, rp.BookID, rp.Device, rp.DeviceID,
			rp.Progress, rp.Percentage, posStr, metaStr, rp.Timestamp, rp.Seq,
		)
		if err != nil {
			return nil, err
		}
	} else {
		_, err := s.Exec(`INSERT INTO reading_progress (
			user_id, document_hash, book_id, device, device_id, progress, percentage, position_json, metadata_json, timestamp, seq
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, document_hash, device_id) DO UPDATE SET
			book_id = CASE WHEN excluded.book_id > 0 THEN excluded.book_id ELSE reading_progress.book_id END,
			device = excluded.device,
			progress = excluded.progress,
			percentage = excluded.percentage,
			position_json = CASE WHEN excluded.position_json != '' THEN excluded.position_json ELSE reading_progress.position_json END,
			metadata_json = CASE WHEN excluded.metadata_json != '' THEN excluded.metadata_json ELSE reading_progress.metadata_json END,
			timestamp = excluded.timestamp,
			seq = excluded.seq`,
			rp.UserID, rp.DocumentHash, rp.BookID, rp.Device, rp.DeviceID,
			rp.Progress, rp.Percentage, posStr, metaStr, rp.Timestamp, rp.Seq,
		)
		if err != nil {
			return nil, err
		}
	}

	// Update book reading_status automatically if matched
	if rp.BookID > 0 {
		status := "reading"
		if rp.Percentage >= 0.98 {
			status = "finished"
		} else if rp.Percentage <= 0.001 {
			status = "unread"
		}
		_, _ = s.Exec(`UPDATE books SET reading_status = ?, updated_at = ? WHERE id = ?`, status, time.Now().Unix(), rp.BookID)
	}

	return rp, nil
}

// GetLatestProgressForDocument returns the newest progress across all devices for a given document hash (or alias).
func (s *Store) GetLatestProgressForDocument(userID int64, docHash string) (*ReadingProgress, error) {
	// Also check if docHash belongs to a book in our library so we unify progress across filename/partial MD5 modes!
	var bookID int64
	if b, err := s.FindBookByDocumentHash(docHash, nil); err == nil && b != nil {
		bookID = b.ID
	}

	var row *sql.Row
	if bookID > 0 {
		row = s.QueryRow(`SELECT id, user_id, document_hash, book_id, device, device_id,
			progress, percentage, position_json, metadata_json, timestamp, seq
			FROM reading_progress
			WHERE user_id = ? AND (document_hash = ? OR book_id = ?)
			ORDER BY timestamp DESC LIMIT 1`, userID, docHash, bookID)
	} else {
		row = s.QueryRow(`SELECT id, user_id, document_hash, book_id, device, device_id,
			progress, percentage, position_json, metadata_json, timestamp, seq
			FROM reading_progress
			WHERE user_id = ? AND document_hash = ?
			ORDER BY timestamp DESC LIMIT 1`, userID, docHash)
	}

	rp := scanProgressRow(row)
	if rp == nil {
		return nil, sql.ErrNoRows
	}
	// Always echo back the requested document hash so KOReader/CrossPoint recognizes it
	rp.DocumentHash = docHash
	return rp, nil
}

// ListDeviceProgressForDocument returns all device rows for a document, newest first.
func (s *Store) ListDeviceProgressForDocument(userID int64, docHash string) ([]*ReadingProgress, error) {
	var bookID int64
	if b, err := s.FindBookByDocumentHash(docHash, nil); err == nil && b != nil {
		bookID = b.ID
	}

	var rows *sql.Rows
	var err error
	if bookID > 0 {
		rows, err = s.Query(`SELECT id, user_id, document_hash, book_id, device, device_id,
			progress, percentage, position_json, metadata_json, timestamp, seq
			FROM reading_progress
			WHERE user_id = ? AND (document_hash = ? OR book_id = ?)
			ORDER BY timestamp DESC`, userID, docHash, bookID)
	} else {
		rows, err = s.Query(`SELECT id, user_id, document_hash, book_id, device, device_id,
			progress, percentage, position_json, metadata_json, timestamp, seq
			FROM reading_progress
			WHERE user_id = ? AND document_hash = ?
			ORDER BY timestamp DESC`, userID, docHash)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*ReadingProgress
	for rows.Next() {
		if rp := scanProgressRow(rows); rp != nil {
			list = append(list, rp)
		}
	}
	return list, nil
}

// ListAllProgress returns recent reading progress entries joined with book titles.
func (s *Store) ListAllProgress(limit int) ([]*ReadingProgress, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Query(`SELECT rp.id, rp.user_id, rp.document_hash, rp.book_id, rp.device, rp.device_id,
		rp.progress, rp.percentage, rp.position_json, rp.metadata_json, rp.timestamp, rp.seq,
		COALESCE(b.title, ''), COALESCE(b.authors, '')
		FROM reading_progress rp
		LEFT JOIN books b ON rp.book_id = b.id
		ORDER BY rp.timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*ReadingProgress
	for rows.Next() {
		var rp ReadingProgress
		var posJSON, metaJSON string
		if err := rows.Scan(
			&rp.ID, &rp.UserID, &rp.DocumentHash, &rp.BookID, &rp.Device, &rp.DeviceID,
			&rp.Progress, &rp.Percentage, &posJSON, &metaJSON, &rp.Timestamp, &rp.Seq,
			&rp.BookTitle, &rp.BookAuthors,
		); err != nil {
			continue
		}
		if posJSON != "" {
			var pos CompactPosition
			if json.Unmarshal([]byte(posJSON), &pos) == nil {
				rp.Position = &pos
			}
		}
		if metaJSON != "" {
			var meta SyncMetadata
			if json.Unmarshal([]byte(metaJSON), &meta) == nil {
				rp.Metadata = &meta
				if rp.BookTitle == "" {
					rp.BookTitle = meta.Title
				}
				if rp.BookAuthors == "" {
					rp.BookAuthors = meta.Authors
				}
			}
		}
		list = append(list, &rp)
	}
	return list, nil
}

// --- Bookmarks & Clippings ---

func (s *Store) ListBookmarks(userID int64, docHash string, since int64) ([]*Bookmark, error) {
	rows, err := s.Query(`SELECT id, user_id, document_hash, book_id, xpath, percentage, summary,
		spine_index, page_in_chapter, chapter_pages, chapter_title, deleted, updated_at
		FROM bookmarks
		WHERE user_id = ? AND document_hash = ? AND updated_at > ?
		ORDER BY updated_at ASC`, userID, docHash, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Bookmark
	for rows.Next() {
		var bm Bookmark
		if err := rows.Scan(&bm.ID, &bm.UserID, &bm.DocumentHash, &bm.BookID, &bm.XPath,
			&bm.Percentage, &bm.Summary, &bm.SpineIndex, &bm.PageInChapter, &bm.ChapterPages,
			&bm.ChapterTitle, &bm.Deleted, &bm.UpdatedAt); err == nil {
			out = append(out, &bm)
		}
	}
	return out, nil
}

func (s *Store) UpsertBookmark(bm *Bookmark) error {
	bm.UpdatedAt = time.Now().Unix()
	if bm.BookID == 0 {
		if b, err := s.FindBookByDocumentHash(bm.DocumentHash, nil); err == nil && b != nil {
			bm.BookID = b.ID
		}
	}
	if s.Driver == "postgres" {
		_, err := s.Exec(`INSERT INTO bookmarks (
			id, user_id, document_hash, book_id, xpath, percentage, summary,
			spine_index, page_in_chapter, chapter_pages, chapter_title, deleted, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, document_hash, id) DO UPDATE SET
			xpath = CASE WHEN EXCLUDED.deleted = 1 THEN bookmarks.xpath ELSE EXCLUDED.xpath END,
			percentage = CASE WHEN EXCLUDED.deleted = 1 THEN bookmarks.percentage ELSE EXCLUDED.percentage END,
			summary = CASE WHEN EXCLUDED.deleted = 1 THEN bookmarks.summary ELSE EXCLUDED.summary END,
			spine_index = CASE WHEN EXCLUDED.deleted = 1 THEN bookmarks.spine_index ELSE EXCLUDED.spine_index END,
			page_in_chapter = CASE WHEN EXCLUDED.deleted = 1 THEN bookmarks.page_in_chapter ELSE EXCLUDED.page_in_chapter END,
			chapter_pages = CASE WHEN EXCLUDED.deleted = 1 THEN bookmarks.chapter_pages ELSE EXCLUDED.chapter_pages END,
			chapter_title = CASE WHEN EXCLUDED.deleted = 1 THEN bookmarks.chapter_title ELSE EXCLUDED.chapter_title END,
			deleted = EXCLUDED.deleted,
			updated_at = EXCLUDED.updated_at`,
			bm.ID, bm.UserID, bm.DocumentHash, bm.BookID, bm.XPath, bm.Percentage, bm.Summary,
			bm.SpineIndex, bm.PageInChapter, bm.ChapterPages, bm.ChapterTitle, bm.Deleted, bm.UpdatedAt)
		return err
	}
	_, err := s.Exec(`INSERT INTO bookmarks (
		id, user_id, document_hash, book_id, xpath, percentage, summary,
		spine_index, page_in_chapter, chapter_pages, chapter_title, deleted, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(user_id, document_hash, id) DO UPDATE SET
		xpath = CASE WHEN excluded.deleted = 1 THEN bookmarks.xpath ELSE excluded.xpath END,
		percentage = CASE WHEN excluded.deleted = 1 THEN bookmarks.percentage ELSE excluded.percentage END,
		summary = CASE WHEN excluded.deleted = 1 THEN bookmarks.summary ELSE excluded.summary END,
		spine_index = CASE WHEN excluded.deleted = 1 THEN bookmarks.spine_index ELSE excluded.spine_index END,
		page_in_chapter = CASE WHEN excluded.deleted = 1 THEN bookmarks.page_in_chapter ELSE excluded.page_in_chapter END,
		chapter_pages = CASE WHEN excluded.deleted = 1 THEN bookmarks.chapter_pages ELSE excluded.chapter_pages END,
		chapter_title = CASE WHEN excluded.deleted = 1 THEN bookmarks.chapter_title ELSE excluded.chapter_title END,
		deleted = excluded.deleted,
		updated_at = excluded.updated_at`,
		bm.ID, bm.UserID, bm.DocumentHash, bm.BookID, bm.XPath, bm.Percentage, bm.Summary,
		bm.SpineIndex, bm.PageInChapter, bm.ChapterPages, bm.ChapterTitle, bm.Deleted, bm.UpdatedAt)
	return err
}

func (s *Store) ListClippings(userID int64, docHash string, since int64) ([]*Clipping, error) {
	rows, err := s.Query(`SELECT id, user_id, document_hash, book_id, spine, para, chapter, text, note, created_at, deleted, updated_at
		FROM clippings
		WHERE user_id = ? AND document_hash = ? AND updated_at > ?
		ORDER BY updated_at ASC`, userID, docHash, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Clipping
	for rows.Next() {
		var c Clipping
		if err := rows.Scan(&c.ID, &c.UserID, &c.DocumentHash, &c.BookID, &c.Spine, &c.Para,
			&c.Chapter, &c.Text, &c.Note, &c.CreatedAt, &c.Deleted, &c.UpdatedAt); err == nil {
			out = append(out, &c)
		}
	}
	return out, nil
}

func (s *Store) UpsertClipping(c *Clipping) error {
	now := time.Now().Unix()
	c.UpdatedAt = now
	if c.CreatedAt == 0 {
		c.CreatedAt = now
	}
	if c.BookID == 0 {
		if b, err := s.FindBookByDocumentHash(c.DocumentHash, nil); err == nil && b != nil {
			c.BookID = b.ID
		}
	}
	if s.Driver == "postgres" {
		_, err := s.Exec(`INSERT INTO clippings (
			id, user_id, document_hash, book_id, spine, para, chapter, text, note, created_at, deleted, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, document_hash, id) DO UPDATE SET
			deleted = EXCLUDED.deleted,
			note = CASE WHEN EXCLUDED.note != '' THEN EXCLUDED.note ELSE clippings.note END,
			updated_at = EXCLUDED.updated_at`,
			c.ID, c.UserID, c.DocumentHash, c.BookID, c.Spine, c.Para, c.Chapter, c.Text, c.Note, c.CreatedAt, c.Deleted, c.UpdatedAt)
		return err
	}
	_, err := s.Exec(`INSERT INTO clippings (
		id, user_id, document_hash, book_id, spine, para, chapter, text, note, created_at, deleted, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(user_id, document_hash, id) DO UPDATE SET
		deleted = excluded.deleted,
		note = CASE WHEN excluded.note != '' THEN excluded.note ELSE clippings.note END,
		updated_at = excluded.updated_at`,
		c.ID, c.UserID, c.DocumentHash, c.BookID, c.Spine, c.Para, c.Chapter, c.Text, c.Note, c.CreatedAt, c.Deleted, c.UpdatedAt)
	return err
}

// --- Xteink X3 / CrossPoint Reader Devices ---

func (s *Store) ListDevices() ([]*Device, error) {
	rows, err := s.Query(`SELECT id, name, device_model, host, http_port, ws_port, target_path,
		preferred_protocol, is_default, last_status_json, last_seen_at, created_at
		FROM devices ORDER BY is_default DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Device
	for rows.Next() {
		var d Device
		var isDef int
		if err := rows.Scan(&d.ID, &d.Name, &d.DeviceModel, &d.Host, &d.HTTPPort, &d.WSPort,
			&d.TargetPath, &d.PreferredProtocol, &isDef, &d.LastStatusJSON, &d.LastSeenAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.IsDefault = isDef == 1
		out = append(out, &d)
	}
	return out, nil
}

func (s *Store) GetDevice(id int64) (*Device, error) {
	var d Device
	var isDef int
	err := s.QueryRow(`SELECT id, name, device_model, host, http_port, ws_port, target_path,
		preferred_protocol, is_default, last_status_json, last_seen_at, created_at
		FROM devices WHERE id = ?`, id).Scan(
		&d.ID, &d.Name, &d.DeviceModel, &d.Host, &d.HTTPPort, &d.WSPort,
		&d.TargetPath, &d.PreferredProtocol, &isDef, &d.LastStatusJSON, &d.LastSeenAt, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	d.IsDefault = isDef == 1
	return &d, nil
}

func (s *Store) SaveDevice(d *Device) (*Device, error) {
	now := time.Now().Unix()
	if d.HTTPPort <= 0 {
		d.HTTPPort = 80
	}
	if d.WSPort <= 0 {
		d.WSPort = 81
	}
	if d.TargetPath == "" {
		d.TargetPath = "/Books"
	}
	if !strings.HasPrefix(d.TargetPath, "/") {
		d.TargetPath = "/" + d.TargetPath
	}
	if d.PreferredProtocol == "" {
		d.PreferredProtocol = "auto"
	}
	if d.DeviceModel == "" {
		d.DeviceModel = "CrossPoint Reader"
	}

	isDef := 0
	if d.IsDefault {
		isDef = 1
		_, _ = s.Exec(`UPDATE devices SET is_default = 0`)
	}

	if d.ID > 0 {
		_, err := s.Exec(`UPDATE devices SET
			name = ?, device_model = ?, host = ?, http_port = ?, ws_port = ?,
			target_path = ?, preferred_protocol = ?, is_default = ?
			WHERE id = ?`,
			d.Name, d.DeviceModel, d.Host, d.HTTPPort, d.WSPort,
			d.TargetPath, d.PreferredProtocol, isDef, d.ID)
		return d, err
	}

	d.CreatedAt = now
	if s.Driver == "postgres" {
		err := s.QueryRow(`INSERT INTO devices (
			name, device_model, host, http_port, ws_port, target_path, preferred_protocol, is_default, last_status_json, last_seen_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			d.Name, d.DeviceModel, d.Host, d.HTTPPort, d.WSPort, d.TargetPath, d.PreferredProtocol, isDef, d.LastStatusJSON, d.LastSeenAt, d.CreatedAt,
		).Scan(&d.ID)
		return d, err
	}
	res, err := s.Exec(`INSERT INTO devices (
		name, device_model, host, http_port, ws_port, target_path, preferred_protocol, is_default, last_status_json, last_seen_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Name, d.DeviceModel, d.Host, d.HTTPPort, d.WSPort, d.TargetPath, d.PreferredProtocol, isDef, d.LastStatusJSON, d.LastSeenAt, d.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	d.ID, _ = res.LastInsertId()
	return d, nil
}

func (s *Store) UpdateDeviceStatus(id int64, statusJSON string) {
	_, _ = s.Exec(`UPDATE devices SET last_status_json = ?, last_seen_at = ? WHERE id = ?`,
		statusJSON, time.Now().Unix(), id)
}

func (s *Store) DeleteDevice(id int64) error {
	_, err := s.Exec(`DELETE FROM devices WHERE id = ?`, id)
	return err
}

func (s *Store) RecordPushHistory(p *PushHistory) {
	p.CreatedAt = time.Now().Unix()
	_, _ = s.Exec(`INSERT INTO push_history (
		book_id, book_title, device_id, device_name, device_host, protocol, remote_path, bytes_sent, status, message, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.BookID, p.BookTitle, p.DeviceID, p.DeviceName, p.DeviceHost, p.Protocol, p.RemotePath, p.BytesSent, p.Status, p.Message, p.CreatedAt,
	)
}

func (s *Store) ListPushHistory(limit int) ([]*PushHistory, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.Query(`SELECT id, book_id, book_title, device_id, device_name, device_host,
		protocol, remote_path, bytes_sent, status, message, created_at
		FROM push_history ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*PushHistory
	for rows.Next() {
		var p PushHistory
		if err := rows.Scan(&p.ID, &p.BookID, &p.BookTitle, &p.DeviceID, &p.DeviceName,
			&p.DeviceHost, &p.Protocol, &p.RemotePath, &p.BytesSent, &p.Status, &p.Message, &p.CreatedAt); err == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}
