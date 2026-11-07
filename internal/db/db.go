package db

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
)

// Store wraps database/sql with dialect helpers for SQLite and PostgreSQL.
type Store struct {
	SQL    *sql.DB
	Driver string // "sqlite" or "postgres"
}

// Open initializes a database connection to either SQLite (default) or PostgreSQL and runs migrations.
func Open(driver, dsn string) (*Store, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	var sqlDriver string

	switch driver {
	case "postgres", "postgresql", "pgx":
		driver = "postgres"
		sqlDriver = "pgx"
	default:
		driver = "sqlite"
		sqlDriver = "sqlite3"
		// Ensure parent directory exists if DSN is a file path
		if !strings.HasPrefix(dsn, ":memory:") && !strings.HasPrefix(dsn, "file:") {
			if dir := filepath.Dir(dsn); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, fmt.Errorf("create sqlite dir: %w", err)
				}
			}
			if !strings.Contains(dsn, "?") {
				dsn = dsn + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
			}
		}
	}

	rawDB, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s db: %w", driver, err)
	}

	if driver == "sqlite" {
		rawDB.SetMaxOpenConns(1)
	} else {
		rawDB.SetMaxOpenConns(15)
		rawDB.SetMaxIdleConns(5)
		rawDB.SetConnMaxLifetime(30 * time.Minute)
	}

	if err := rawDB.Ping(); err != nil {
		rawDB.Close()
		return nil, fmt.Errorf("ping %s db: %w", driver, err)
	}

	store := &Store{
		SQL:    rawDB,
		Driver: driver,
	}

	if err := store.Migrate(); err != nil {
		rawDB.Close()
		return nil, fmt.Errorf("migrate %s db: %w", driver, err)
	}

	return store, nil
}

// Close closes the underlying sql.DB.
func (s *Store) Close() error {
	if s.SQL != nil {
		return s.SQL.Close()
	}
	return nil
}

// Rebind converts '?' placeholders into '$1, $2, ...' when running against PostgreSQL.
func (s *Store) Rebind(query string) string {
	if s.Driver != "postgres" {
		return query
	}
	var out strings.Builder
	out.Grow(len(query) + 16)
	argIdx := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(argIdx))
			argIdx++
		} else {
			out.WriteByte(query[i])
		}
	}
	return out.String()
}

// Exec executes a query with dialect-aware placeholder rebinding.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.SQL.Exec(s.Rebind(query), args...)
}

// Query executes a query that returns rows with dialect-aware placeholder rebinding.
func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.SQL.Query(s.Rebind(query), args...)
}

// QueryRow executes a query expected to return at most one row.
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.SQL.QueryRow(s.Rebind(query), args...)
}

// Migrate creates all required tables and indexes for both SQLite and PostgreSQL.
func (s *Store) Migrate() error {
	autoIncPK := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if s.Driver == "postgres" {
		autoIncPK = "BIGSERIAL PRIMARY KEY"
	}

	schema := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS users (
			id %s,
			username TEXT NOT NULL UNIQUE,
			password_key TEXT NOT NULL,
			tailscale_login TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			profile_pic_url TEXT NOT NULL DEFAULT '',
			tailscale_node TEXT NOT NULL DEFAULT '',
			tailscale_ip TEXT NOT NULL DEFAULT '',
			auth_source TEXT NOT NULL DEFAULT 'tailscale',
			created_at BIGINT NOT NULL
		)`, autoIncPK),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS books (
			id %s,
			uuid TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			subtitle TEXT NOT NULL DEFAULT '',
			authors TEXT NOT NULL DEFAULT 'Unknown Author',
			series TEXT NOT NULL DEFAULT '',
			series_index REAL NOT NULL DEFAULT 0,
			tags TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			publisher TEXT NOT NULL DEFAULT '',
			published_date TEXT NOT NULL DEFAULT '',
			language TEXT NOT NULL DEFAULT 'en',
			isbn TEXT NOT NULL DEFAULT '',
			file_path TEXT NOT NULL,
			file_name TEXT NOT NULL,
			file_size BIGINT NOT NULL DEFAULT 0,
			cover_path TEXT NOT NULL DEFAULT '',
			ko_md5_partial TEXT NOT NULL DEFAULT '',
			ko_md5_alt TEXT NOT NULL DEFAULT '',
			ko_md5_filename TEXT NOT NULL DEFAULT '',
			chapter_count INTEGER NOT NULL DEFAULT 1,
			page_count INTEGER NOT NULL DEFAULT 100,
			reading_status TEXT NOT NULL DEFAULT 'unread',
			added_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`, autoIncPK),

		`CREATE INDEX IF NOT EXISTS idx_books_ko_partial ON books(ko_md5_partial)`,
		`CREATE INDEX IF NOT EXISTS idx_books_ko_alt ON books(ko_md5_alt)`,
		`CREATE INDEX IF NOT EXISTS idx_books_ko_filename ON books(ko_md5_filename)`,

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS book_hash_aliases (
			id %s,
			book_id BIGINT NOT NULL,
			document_hash TEXT NOT NULL UNIQUE,
			created_at BIGINT NOT NULL
		)`, autoIncPK),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS reading_progress (
			id %s,
			user_id BIGINT NOT NULL,
			document_hash TEXT NOT NULL,
			book_id BIGINT NOT NULL DEFAULT 0,
			device TEXT NOT NULL DEFAULT 'WebReader',
			device_id TEXT NOT NULL DEFAULT 'folio-web',
			progress TEXT NOT NULL DEFAULT '',
			percentage REAL NOT NULL DEFAULT 0,
			position_json TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '',
			timestamp BIGINT NOT NULL,
			seq BIGINT NOT NULL DEFAULT 0,
			UNIQUE(user_id, document_hash, device_id)
		)`, autoIncPK),

		`CREATE INDEX IF NOT EXISTS idx_progress_doc ON reading_progress(user_id, document_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_progress_book ON reading_progress(book_id)`,

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS bookmarks (
			id TEXT NOT NULL,
			user_id BIGINT NOT NULL,
			document_hash TEXT NOT NULL,
			book_id BIGINT NOT NULL DEFAULT 0,
			xpath TEXT NOT NULL DEFAULT '',
			percentage REAL NOT NULL DEFAULT 0,
			summary TEXT NOT NULL DEFAULT '',
			spine_index INTEGER NOT NULL DEFAULT 0,
			page_in_chapter INTEGER NOT NULL DEFAULT 0,
			chapter_pages INTEGER NOT NULL DEFAULT 0,
			chapter_title TEXT NOT NULL DEFAULT '',
			deleted INTEGER NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (user_id, document_hash, id)
		)`),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS clippings (
			id TEXT NOT NULL,
			user_id BIGINT NOT NULL,
			document_hash TEXT NOT NULL,
			book_id BIGINT NOT NULL DEFAULT 0,
			spine INTEGER NOT NULL DEFAULT 0,
			para INTEGER NOT NULL DEFAULT 0,
			chapter TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			deleted INTEGER NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (user_id, document_hash, id)
		)`),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS devices (
			id %s,
			name TEXT NOT NULL,
			device_model TEXT NOT NULL DEFAULT 'Xteink X3',
			host TEXT NOT NULL,
			http_port INTEGER NOT NULL DEFAULT 80,
			ws_port INTEGER NOT NULL DEFAULT 81,
			target_path TEXT NOT NULL DEFAULT '/Books',
			preferred_protocol TEXT NOT NULL DEFAULT 'auto',
			is_default INTEGER NOT NULL DEFAULT 0,
			last_status_json TEXT NOT NULL DEFAULT '',
			last_seen_at BIGINT NOT NULL DEFAULT 0,
			created_at BIGINT NOT NULL
		)`, autoIncPK),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS push_history (
			id %s,
			book_id BIGINT NOT NULL,
			book_title TEXT NOT NULL,
			device_id BIGINT NOT NULL,
			device_name TEXT NOT NULL,
			device_host TEXT NOT NULL,
			protocol TEXT NOT NULL,
			remote_path TEXT NOT NULL,
			bytes_sent BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL
		)`, autoIncPK),
	}

	for _, stmt := range schema {
		if _, err := s.SQL.Exec(stmt); err != nil {
			return fmt.Errorf("exec migration (%s): %w", stmt[:40], err)
		}
	}

	// Ensure existing databases have the Tailscale identity columns on users
	alterCols := []string{
		`ALTER TABLE users ADD COLUMN tailscale_login TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN profile_pic_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN tailscale_node TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN tailscale_ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN auth_source TEXT NOT NULL DEFAULT 'tailscale'`,
	}
	for _, stmt := range alterCols {
		_, _ = s.SQL.Exec(stmt)
	}

	return nil
}

// MD5Hex returns the lowercase 32-character hex MD5 digest of a string.
func MD5Hex(input string) string {
	sum := md5.Sum([]byte(input))
	return hex.EncodeToString(sum[:])
}

// EnsureDefaultUser creates a default Tailscale + KOReader sync account if no users exist yet.
func (s *Store) EnsureDefaultUser(username, plainPassword string) error {
	var count int
	if err := s.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		// Ensure the first user has a default Tailscale login populated if blank
		_, _ = s.Exec(`UPDATE users SET
			tailscale_login = CASE WHEN tailscale_login = '' THEN username || '@tailnet.ts.net' ELSE tailscale_login END,
			display_name = CASE WHEN display_name = '' THEN 'Tailscale Reader' ELSE display_name END,
			tailscale_node = CASE WHEN tailscale_node = '' THEN 'folio-host.tailnet.ts.net' ELSE tailscale_node END,
			tailscale_ip = CASE WHEN tailscale_ip = '' THEN '100.101.102.103' ELSE tailscale_ip END
			WHERE id = (SELECT MIN(id) FROM users)`)
		return nil
	}
	passKey := MD5Hex(plainPassword)
	tsLogin := username
	if !strings.Contains(tsLogin, "@") {
		tsLogin = username + "@tailnet.ts.net"
	}
	_, err := s.Exec(
		`INSERT INTO users (username, password_key, tailscale_login, display_name, tailscale_node, tailscale_ip, auth_source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		username, passKey, tsLogin, "Tailscale Reader", "folio-host.tailnet.ts.net", "100.101.102.103", "tailscale", time.Now().Unix(),
	)
	return err
}
